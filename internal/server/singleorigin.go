package server

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// Single-origin apps (ADR-060): a package that declares
// single_origin_sign_in has its published route's LAN and .local names
// redirect to the public name. The marker on a route is derived from the
// catalog on every edge render, never taken from a client, so apps
// installed before the package declared it take it on the next render.

// singleOriginCatalog is the catalog directory the edge render stamps from;
// MountCatalog sets it.
var singleOriginCatalog atomic.Value

func setSingleOriginCatalog(dir string) { singleOriginCatalog.Store(dir) }

func singleOriginCatalogDir() string {
	dir, _ := singleOriginCatalog.Load().(string)
	return dir
}

// stampSingleOrigin sets SingleOrigin on the routes of every single-origin
// package and clears it everywhere else, reporting whether any route
// changed. A catalog that cannot be read leaves the routes alone.
func stampSingleOrigin(catalogDir string, routes []docker.Route) bool {
	if catalogDir == "" {
		return false
	}
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return false
	}
	want := map[docker.Hostname]bool{}
	for _, name := range entries.Names() {
		m, err := entries.Get(name)
		if err != nil || !m.SingleOriginSignIn {
			continue
		}
		for _, r := range appsso.AppRoutes(m, routes) {
			if !r.IsPath() {
				want[r.Host] = true
			}
		}
	}
	changed := false
	for i := range routes {
		if routes[i].SingleOrigin != want[routes[i].Host] {
			routes[i].SingleOrigin = want[routes[i].Host]
			changed = true
		}
	}
	return changed
}

// MigrateSingleOrigin re-renders the edge once when the stored table's
// markers are out of date with the catalog (an appliance upgraded to a
// package that declares the marker), through the same reconcile the tunnel
// setup uses. It does nothing when the table is current.
func MigrateSingleOrigin(ctx context.Context, st *store.Store, catalogDir string) error {
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return err
	}
	if !stampSingleOrigin(catalogDir, set.Routes) {
		return nil
	}
	var redirected []string
	for _, r := range set.Routes {
		if to, from := r.LANRedirect(); to != "" {
			names := make([]string, len(from))
			for i, n := range from {
				names[i] = string(n)
			}
			redirected = append(redirected, strings.Join(names, ", ")+" to "+to)
		}
	}
	detail := "single-origin sign-in markers updated (ADR-060)"
	if len(redirected) > 0 {
		detail += "; LAN names redirect: " + strings.Join(redirected, "; ")
	}
	if err := reconcileEdgeSet(ctx, st, set); err != nil {
		_, _ = audit.New(st).Record(audit.Entry{Actor: "system", Action: "edge.single-origin", Outcome: audit.OutcomeError, Detail: fmt.Sprintf("%s: %v", detail, err)})
		return err
	}
	_, _ = audit.New(st).Record(audit.Entry{Actor: "system", Action: "edge.single-origin", Outcome: audit.OutcomeOK, Detail: detail})
	return nil
}

// RunSingleOriginMigration runs MigrateSingleOrigin at start, retrying while
// the edge cannot be reconciled (Docker may arrive after the daemon).
func RunSingleOriginMigration(ctx context.Context, st *store.Store, catalogDir string, retry time.Duration, warn func(error)) {
	for {
		err := MigrateSingleOrigin(ctx, st, catalogDir)
		if err == nil {
			return
		}
		if warn != nil {
			warn(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}
