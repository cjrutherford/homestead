package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"homestead/internal/store"
)

func edgeTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New("127.0.0.1:0")
	srv.MountEdge(s)
	return srv
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
