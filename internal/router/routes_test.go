package router

import (
	"path/filepath"
	"testing"

	"homestead/internal/store"
)

func routesTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRouteRecords(t *testing.T) {
	st := routesTestStore(t)
	route := TunnelRoute{Hostname: "app.example.com", TunnelID: "tunnel-id", Service: "http://127.0.0.1:8080"}
	if err := RecordRoute(st, route, "homestead"); err != nil {
		t.Fatalf("RecordRoute: %v", err)
	}
	routes, err := ListRoutes(st)
	if err != nil {
		t.Fatalf("ListRoutes: %v", err)
	}
	if len(routes) != 1 || routes[0].Hostname != "app.example.com" || routes[0].Service != "http://127.0.0.1:8080" {
		t.Fatalf("routes = %+v", routes)
	}
	// Re-setup replaces the route for the hostname.
	route.Service = "http://127.0.0.1:9090"
	if err := RecordRoute(st, route, "homestead"); err != nil {
		t.Fatalf("RecordRoute: %v", err)
	}
	routes, err = ListRoutes(st)
	if err != nil {
		t.Fatalf("ListRoutes: %v", err)
	}
	if len(routes) != 1 || routes[0].Service != "http://127.0.0.1:9090" {
		t.Fatalf("routes = %+v", routes)
	}
	// Teardown converges: unknown hostnames delete cleanly.
	if err := DeleteRoute(st, "missing.example.com"); err != nil {
		t.Fatalf("DeleteRoute: %v", err)
	}
	if err := DeleteRoute(st, "app.example.com"); err != nil {
		t.Fatalf("DeleteRoute: %v", err)
	}
	routes, err = ListRoutes(st)
	if err != nil {
		t.Fatalf("ListRoutes: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("routes = %+v", routes)
	}
	// Invalid routes never persist.
	if err := RecordRoute(st, TunnelRoute{}, "homestead"); err == nil {
		t.Fatal("empty route recorded")
	}
	if err := RecordRoute(nil, route, "homestead"); err == nil {
		t.Fatal("nil store accepted")
	}
}
