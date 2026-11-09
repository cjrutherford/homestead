package server

import (
	"context"
	"path/filepath"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/store"
)

// ADR-045 §5: a craft app installed on a name of its own moves to its
// path at start, with the edge reconciled once and the move audited; the
// other routes and an already moved craft app are left as they are.
func TestMigrateCraftRoutes(t *testing.T) {
	_, st, _, _ := appPublicRig(t)
	installApp(t, st, "cadcraft", docker.Route{Host: "cadcraft.lan", Target: docker.Target{Host: "cadcraft", Port: 8080},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, CraftApp: "cadcraft"})
	installApp(t, st, "deckcraft", docker.Route{Host: "deckcraft.lan", Target: docker.Target{Host: "deckcraft", Port: 8080},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, CraftApp: "deckcraft", Path: "/apps/deckcraft/", StripPrefix: true})
	reconciles := 0
	prev := reconcileEdgeSet
	reconcileEdgeSet = func(_ context.Context, st *store.Store, set docker.RouteSet) error {
		reconciles++
		return saveEdgeRoutes(st, set)
	}
	t.Cleanup(func() { reconcileEdgeSet = prev })
	dir := filepath.Join("..", "..", "catalog")

	moved, err := MigrateCraftRoutes(context.Background(), st, dir)
	if err != nil || len(moved) != 1 || moved[0] != "cadcraft to /apps/cadcraft/" || reconciles != 1 {
		t.Fatalf("moved = %v, err = %v, reconciles = %d", moved, err, reconciles)
	}
	r := movedRoute(t, st, "cadcraft.lan")
	if r.Path != "/apps/cadcraft/" || !r.StripPrefix || r.CraftApp != "cadcraft" || r.Published || len(r.Tunnel) != 0 {
		t.Fatalf("route = %+v", r)
	}
	if immich := movedRoute(t, st, "immich.lan"); immich.IsPath() {
		t.Fatalf("immich moved: %+v", immich)
	}
	var detail string
	if err := st.DB().QueryRow(`SELECT detail FROM audit_entries WHERE action = 'craft.move' AND outcome = 'ok'`).Scan(&detail); err != nil ||
		detail != "moved cadcraft to /apps/cadcraft/ (ADR-045)" {
		t.Fatalf("audit = %q, %v", detail, err)
	}

	moved, err = MigrateCraftRoutes(context.Background(), st, dir)
	if err != nil || len(moved) != 0 || reconciles != 1 {
		t.Fatalf("second pass: moved = %v, err = %v, reconciles = %d", moved, err, reconciles)
	}
}
