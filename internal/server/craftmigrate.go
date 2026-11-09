package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// MigrateCraftRoutes moves every craft app still answering at a name of
// its own to its path under the appliance's names (ADR-045 §5, as the
// owner amended it on 2026-10-27). A craft app has no settings that name
// its address, so the move is only the edge route: its path, the prefix
// strip and the package's redirects. It returns the apps it moved; the
// edge is reconciled and the move audited only when there are any.
func MigrateCraftRoutes(ctx context.Context, st *store.Store, catalogDir string) ([]string, error) {
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable: %w", err)
	}
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return nil, err
	}
	var moved []string
	for i, r := range set.Routes {
		if r.CraftApp == "" || r.IsPath() {
			continue
		}
		m, err := entries.Get(r.CraftApp)
		if err != nil || !m.ServedByPath() {
			continue
		}
		r.Path = m.AppPath()
		r.StripPrefix = m.StripPrefix
		r.Redirects = append([]docker.PathRedirect(nil), m.Redirects...)
		r.Tunnel = nil
		r.Published = false
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("move %s to %s: %w", m.Title, r.Path, err)
		}
		set.Routes[i] = r
		moved = append(moved, fmt.Sprintf("%s to %s", r.CraftApp, r.Path))
	}
	if len(moved) == 0 {
		return nil, nil
	}
	record := func(outcome, detail string) {
		_, _ = audit.New(st).Record(audit.Entry{Actor: "system", Action: "craft.move", Outcome: outcome, Detail: detail})
	}
	detail := "moved " + strings.Join(moved, ", ") + " (ADR-045)"
	if err := reconcileEdgeSet(ctx, st, set); err != nil {
		record(audit.OutcomeError, detail+": "+err.Error())
		return nil, err
	}
	record(audit.OutcomeOK, detail)
	return moved, nil
}

// RunCraftMigration runs MigrateCraftRoutes at start, retrying while the
// edge cannot be reconciled (Docker may arrive after the daemon).
func RunCraftMigration(ctx context.Context, st *store.Store, catalogDir string, retry time.Duration, warn func(error)) {
	for {
		_, err := MigrateCraftRoutes(ctx, st, catalogDir)
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
