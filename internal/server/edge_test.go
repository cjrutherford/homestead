package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/exposure"
	"homestead/internal/store"
)

func edgeTestServer(t *testing.T) *Server {
	srv, _ := edgeTestRig(t)
	return srv
}

func edgeTestRig(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New("127.0.0.1:0")
	srv.MountEdge(s)
	return srv, s
}

func TestEdgeDeploy(t *testing.T) {
	srv := edgeTestServer(t)
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/edge/deploy", map[string]any{})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var started struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if started.ID == 0 {
		t.Fatal("no operation id")
	}
}

func TestEdgeRoutes(t *testing.T) {
	srv := edgeTestServer(t)
	route := map[string]any{
		"Host":     "app.lan",
		"Target":   map[string]any{"Host": "app", "Port": 8080},
		"Exposure": "local",
		"TLS":      "internal",
		"Health":   map[string]any{"Path": "/", "IntervalSeconds": 30},
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/edge/routes", map[string]any{"routes": []any{route}})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	// Invalid routes refuse before running anything.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/edge/routes",
		map[string]any{"routes": []any{map[string]any{"Host": "Bad Host!"}}})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Duplicate hosts refuse.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/edge/routes",
		map[string]any{"routes": []any{route, route}})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Malformed bodies refuse.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/edge/routes", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func publicRoute() docker.Route {
	return docker.Route{
		Host:     "wiki.example.com",
		Target:   docker.Target{Host: "wiki", Port: 80},
		Exposure: docker.ExposurePublic,
		TLS:      docker.TLSAuto,
	}
}

func routeBody(r docker.Route) map[string]any {
	return map[string]any{"routes": []any{r}}
}

func TestEdgeRoutesPublicNeedsApproval(t *testing.T) {
	srv, st := edgeTestRig(t)
	route := publicRoute()
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/edge/routes", routeBody(route))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "exposure approval") {
		t.Fatalf("unapproved public: %d %s", recorder.Code, recorder.Body.String())
	}
	plan := exposure.Plan{Host: route.Host, From: docker.ExposureLocal, To: docker.ExposurePublic, Edge: route}
	if err := exposure.RecordApproval(st, exposure.Approval{Host: route.Host, Plan: plan, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/edge/routes", routeBody(route))
	if recorder.Code != http.StatusOK {
		t.Fatalf("approved public: %d %s", recorder.Code, recorder.Body.String())
	}
	// A route that differs from the approved plan is refused.
	changed := route
	changed.Target.Port = 8080
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/edge/routes", routeBody(changed))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "differs from its approved plan") {
		t.Fatalf("changed public: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestEdgeRoutesRefusesPrivateAutoUpFront(t *testing.T) {
	srv := edgeTestServer(t)
	route := docker.Route{Host: "app.lan", Target: docker.Target{Host: "app", Port: 80}, Exposure: docker.ExposureLocal, TLS: docker.TLSAuto}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/edge/routes", routeBody(route))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "automatic TLS") {
		t.Fatalf("private auto: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestEdgeRoutesGetReadsRecordedTable(t *testing.T) {
	srv, st := edgeTestRig(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/edge/routes", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"routes":[]`) {
		t.Fatalf("empty: %d %s", recorder.Code, recorder.Body.String())
	}
	route := docker.Route{Host: "app.lan", Target: docker.Target{Host: "app", Port: 80}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{route}}); err != nil {
		t.Fatal(err)
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/edge/routes", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"Host":"app.lan"`) {
		t.Fatalf("recorded: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestInferCraftApps(t *testing.T) {
	routes := []docker.Route{
		{Host: "gridcraft.lan", Target: docker.Target{Host: "gridcraft", Port: 8080}},
		{Host: "wiki.lan", Target: docker.Target{Host: "wiki", Port: 8080}},
		{Host: "wordcraft.lan", Target: docker.Target{Host: "wordcraft", Port: 8080}},
		{Host: "g2.lan", Target: docker.Target{Host: "gridcraft", Port: 9000}},
		{Host: "x.lan", Target: docker.Target{Host: "pdfcraft", Port: 8080}, CraftApp: "keep"},
	}
	inferCraftApps(routes)
	got := []string{routes[0].CraftApp, routes[1].CraftApp, routes[2].CraftApp, routes[3].CraftApp, routes[4].CraftApp}
	if strings.Join(got, ",") != "gridcraft,,,,keep" {
		t.Fatalf("inferred = %v", got)
	}
}
