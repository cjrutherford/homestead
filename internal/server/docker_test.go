package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/docker"
)

func dockerServer(t *testing.T, socketPath string) *Server {
	t.Helper()
	srv := New("127.0.0.1:0")
	srv.MountDocker(docker.NewClient(socketPath))
	return srv
}

func TestDockerHealthShape(t *testing.T) {
	// Real Engine socket unavailable in tests: point at absent socket and
	// assert the unreachable shape, never an error.
	dir := t.TempDir()
	srv := dockerServer(t, filepath.Join(dir, "absent.sock"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/docker/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"reachable":false`, `"containers":[]`, `"images":[]`, `"networks":[]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}
