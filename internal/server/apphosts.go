package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// Keeping an app's own host allow-list in step with the names Homestead
// serves it on (S396). Nextcloud's trusted_domains is read from the
// environment only at its first install, so every later change to the
// names (an edge table change, an address step, an adoption) left it
// stale and https://nextcloud.lan answered 400. One reconcile adds every
// name the app's edge routes answer on, through the same trustAppHosts
// the move and public-name steps use. Names the owner added stay; names
// Homestead added earlier are not removed here (docs/DEFERRED.md).

// appHostsFor are the names the installed app is served on, local and
// public, and the address it is reached at.
func appHostsFor(st *store.Store, m catalog.Manifest) (names []string, address string, err error) {
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return nil, "", err
	}
	seen := map[string]bool{}
	for _, r := range docker.ServedRoutes(set.Routes, appsso.AppRoutes(m, set.Routes)) {
		for _, n := range r.AllNames() {
			name := strings.ToLower(string(n))
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		if address == "" && r.Exposure != docker.ExposurePublic {
			address = "https://" + string(r.Host)
			if r.IsPath() {
				address += strings.TrimSuffix(r.Path, "/")
			}
		}
	}
	if address == "" && len(names) > 0 {
		address = "https://" + names[0]
	}
	return names, address, nil
}

// hostListApps are the installed catalog apps that keep an allow-list.
func hostListApps(st *store.Store, catalogDir string) []catalog.Manifest {
	if catalogDir == "" {
		return nil
	}
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return nil
	}
	var out []catalog.Manifest
	for _, name := range entries.Names() {
		m, err := entries.Get(name)
		if err != nil || appsso.HostSettings(m, []string{"x"}, nil) == nil {
			continue
		}
		if installed, err := catalog.Installed(st, m.Name, stackDirFor(m.Name)); err != nil || !installed {
			continue
		}
		out = append(out, m)
	}
	return out
}

// appHostsSnapshot is each such app's served names now, to tell which
// apps' names an operation changed.
func appHostsSnapshot(st *store.Store, catalogDir string) map[string]string {
	out := map[string]string{}
	for _, m := range hostListApps(st, catalogDir) {
		if names, _, err := appHostsFor(st, m); err == nil {
			sort.Strings(names)
			out[m.Name] = strings.Join(names, " ")
		}
	}
	return out
}

// reconcileAppHosts adds the served names to each installed app's
// allow-list. With before, only apps whose names differ from it are
// touched (an edge change that left an app's names alone never needs
// the app running). Failures are returned, one per app.
func reconcileAppHosts(ctx context.Context, st *store.Store, catalogDir string, before map[string]string) error {
	var errs []error
	for _, m := range hostListApps(st, catalogDir) {
		names, address, err := appHostsFor(st, m)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(names) == 0 {
			continue
		}
		if before != nil {
			sorted := append([]string(nil), names...)
			sort.Strings(sorted)
			if prev, ok := before[m.Name]; ok && prev == strings.Join(sorted, " ") {
				continue
			}
		}
		if err := trustAppHosts(ctx, m, address, names, nil); err != nil {
			errs = append(errs, fmt.Errorf("%s did not take its names (%s) into its trusted domains: %w", m.Title, strings.Join(names, ", "), err))
		}
	}
	return errors.Join(errs...)
}
