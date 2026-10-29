package server

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"homestead/internal/appsso"
	"homestead/internal/auth"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/people"
	"homestead/internal/store"
)

// The apps list (S337, ADR-050 §4-§5): the installed apps with a web
// address that a signed-in owner, or a given person, may open. One list
// for the dashboard and the CLI. Derived state like the craft apps page:
// catalog, store and the recorded edge route table; no network call.

// MyApp is one tile on the apps page.
type MyApp struct {
	// Name is the package; Title its display name; Icon the catalog icon
	// name the dashboard already uses for the app.
	Name  string `json:"name"`
	Title string `json:"title"`
	Icon  string `json:"icon"`
	// Role is "owner" for the owner's list, else the person's grant:
	// "user" or "admin".
	Role string `json:"role"`
	// URL is the app's public address when the request came in on a public
	// dashboard name and the app is published; otherwise its local address.
	URL string `json:"url"`
}

type myAppsSource struct {
	Catalog   func() (catalog.Catalog, error)
	Installed func(name string) bool
	Routes    func() ([]docker.Route, error)
}

// appURL is the address of an app for one request. public says the request
// arrived on a public dashboard name.
func appURL(m catalog.Manifest, all []docker.Route, public bool) (string, bool) {
	routes := appsso.AppRoutes(m, all)
	if len(routes) == 0 {
		if r, ok := routeFor(m.Name, all); ok {
			routes = []docker.Route{r}
		}
	}
	if len(routes) == 0 {
		return "", false
	}
	route := routes[0]
	if public {
		if route.IsPath() {
			if server := docker.PublicServerName(all); route.Published && server != "" {
				return docker.PathURL(server, route.Path), true
			}
		} else if len(route.Tunnel) > 0 {
			return "https://" + string(route.Tunnel[0]) + "/", true
		}
	}
	scheme := "https"
	if route.TLS == docker.TLSOff {
		scheme = "http"
	}
	if route.IsPath() {
		route = docker.ServedRoutes(all, []docker.Route{route})[0]
		return docker.PathURL(route.Host, route.Path), true
	}
	return fmt.Sprintf("%s://%s/", scheme, route.Host), true
}

// list builds the list. grants nil means the owner's (every app, role
// "owner"); otherwise only apps with a grant, in the grant's role.
func (src myAppsSource) list(public bool, grants map[string]string) ([]MyApp, error) {
	entries, err := src.Catalog()
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable: %w", err)
	}
	routes, err := src.Routes()
	if err != nil {
		return nil, err
	}
	out := []MyApp{}
	for _, name := range entries.Names() {
		// Authentik is the sign-in provider, not an app to open from here.
		if name == authentikPackage {
			continue
		}
		role := "owner"
		if grants != nil {
			if role = grants[name]; role == "" {
				continue
			}
		}
		m, err := entries.Get(name)
		if err != nil || !src.Installed(name) {
			continue
		}
		url, ok := appURL(m, routes, public)
		if !ok {
			continue
		}
		out = append(out, MyApp{Name: m.Name, Title: m.Title, Icon: m.Icon, Role: role, URL: url})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// MountMyApps registers the apps list routes.
func (s *Server) MountMyApps(st *store.Store, catalogDir string) {
	s.mountMyApps(myAppsSource{
		Catalog: func() (catalog.Catalog, error) { return catalog.Load(catalogDir) },
		Installed: func(name string) bool {
			ok, _ := catalog.Installed(st, name, stackDirFor(name))
			return ok
		},
		Routes: func() ([]docker.Route, error) {
			set, err := loadEdgeRoutes(st)
			return set.Routes, err
		},
	})
}

func (s *Server) mountMyApps(src myAppsSource) {
	s.mux.HandleFunc("GET /api/v1/me/apps", func(w http.ResponseWriter, r *http.Request) {
		// A person session gets that person's list (ADR-050 §4); the
		// person comes from the verified session, never the request.
		if op, ok := operatorFromContext(r.Context()); ok && op.Session.Role == auth.RolePerson {
			if grants, ok := s.personGrants(w, op.Session.PersonID); ok {
				s.writeMyApps(w, src, onPublicHost(r), grants)
			}
			return
		}
		s.writeMyApps(w, src, onPublicHost(r), nil)
	})
	s.mux.HandleFunc("GET /api/v1/people/{id}/apps", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "no such person", http.StatusNotFound)
			return
		}
		if s.people == nil {
			http.Error(w, "no such person", http.StatusNotFound)
			return
		}
		if grants, ok := s.personGrants(w, id); ok {
			s.writeMyApps(w, src, onPublicHost(r), grants)
		}
	})
}

// personGrants is a person's grants by app from Homestead's record (empty
// for a disabled person). It writes the error and returns false on failure.
func (s *Server) personGrants(w http.ResponseWriter, id int64) (map[string]string, bool) {
	if s.people == nil {
		http.Error(w, "no such person", http.StatusNotFound)
		return nil, false
	}
	active, gs, err := s.people.AppGrants(id)
	if errors.Is(err, people.ErrNotFound) {
		http.Error(w, "no such person", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		s.log.Warn("people apps failed", "error", err)
		http.Error(w, "the apps request failed", http.StatusInternalServerError)
		return nil, false
	}
	grants := map[string]string{}
	if active {
		for _, g := range gs {
			grants[g.App] = g.Role
		}
	}
	return grants, true
}

func (s *Server) writeMyApps(w http.ResponseWriter, src myAppsSource, public bool, grants map[string]string) {
	apps, err := src.list(public, grants)
	if err != nil {
		s.log.Warn("apps list failed", "error", err)
		http.Error(w, "the apps list failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"apps": apps})
}
