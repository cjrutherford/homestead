package server

import (
	"fmt"
	"net/http"
	"sort"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// Craft apps start page (S281, ADR-031 option b). The page is derived
// state: installed craft packages from the catalog and the store, joined
// with the recorded edge route table. It reads only the catalog
// directory, the SQLite store and the stack directory marker; it makes
// no network call (the test asserts this), and there is nothing to
// regenerate, back up or drift-check.

// Reasons an installed craft app has no URL.
const (
	craftNoRouteReason = "No edge route serves this app yet. Add a route for it on the Edge page (or `homesteadctl edge routes set`) and it appears here."
)

// CraftApp is one installed craft package with its local URL.
type CraftApp struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// Blurb is the manifest's one-line purpose.
	Blurb   string `json:"blurb,omitempty"`
	Version string `json:"version"`
	// Status is "installed": the package has a stack. Running state stays
	// on the Apps and Status pages.
	Status string `json:"status"`
	// URL is empty (and URLReason says why) when no edge route serves
	// the app. The scheme follows the route's TLS mode: https unless the
	// owner set TLS off.
	URL       string `json:"url"`
	Host      string `json:"host,omitempty"`
	TLS       string `json:"tls,omitempty"`
	Exposure  string `json:"exposure,omitempty"`
	URLReason string `json:"url_reason,omitempty"`
}

// craftAppsSource is everything the handler reads. Every field is a
// local read; tests inject fakes and nothing here dials out.
type craftAppsSource struct {
	Catalog   func() (catalog.Catalog, error)
	Installed func(name string) bool
	Routes    func() ([]docker.Route, error)
}

// routeFor picks the route that serves a craft app: the route naming the
// app (CraftApp, set by install plans) or, for tables recorded earlier, an
// upstream whose service carries the app's name. An HTTPS route wins over
// a plain HTTP one; otherwise the first in table order.
func routeFor(app string, routes []docker.Route) (docker.Route, bool) {
	var found *docker.Route
	for i := range routes {
		r := &routes[i]
		if r.CraftApp != app && !(r.CraftApp == "" && r.Target.Host == app) {
			continue
		}
		if r.TLS != docker.TLSOff {
			return *r, true
		}
		if found == nil {
			found = r
		}
	}
	if found == nil {
		return docker.Route{}, false
	}
	return *found, true
}

// classifyCraftApp builds the row for one installed craft package.
func classifyCraftApp(m catalog.Manifest, routes []docker.Route) CraftApp {
	app := CraftApp{Name: m.Name, Title: m.Title, Blurb: m.Blurb, Version: m.Version, Status: "installed"}
	route, ok := routeFor(m.Name, routes)
	if !ok {
		app.URLReason = craftNoRouteReason
		return app
	}
	scheme := "https"
	if route.TLS == docker.TLSOff {
		scheme = "http"
	}
	app.Host = string(route.Host)
	app.TLS = string(route.TLS)
	app.Exposure = string(route.Exposure)
	app.URL = fmt.Sprintf("%s://%s/", scheme, route.Host)
	return app
}

// list returns the installed craft packages, sorted by title then name.
// Never nil.
func (src craftAppsSource) list() ([]CraftApp, error) {
	entries, err := src.Catalog()
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable: %w", err)
	}
	routes, err := src.Routes()
	if err != nil {
		return nil, err
	}
	out := []CraftApp{}
	for _, name := range entries.Names() {
		m, err := entries.Get(name)
		if err != nil || len(m.CraftImages()) == 0 || !src.Installed(name) {
			continue
		}
		out = append(out, classifyCraftApp(m, routes))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (src craftAppsSource) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apps, err := src.list()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"apps": apps})
	}
}

// storeCraftAppsSource wires the source to the real catalog directory and
// store.
func storeCraftAppsSource(st *store.Store, catalogDir string) craftAppsSource {
	return craftAppsSource{
		Catalog: func() (catalog.Catalog, error) { return catalog.Load(catalogDir) },
		Installed: func(name string) bool {
			ok, _ := catalog.Installed(st, name, stackDirFor(name))
			return ok
		},
		Routes: func() ([]docker.Route, error) {
			set, err := loadEdgeRoutes(st)
			return set.Routes, err
		},
	}
}
