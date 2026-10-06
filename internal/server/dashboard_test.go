package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"homestead/internal/dashboard"
)

func TestDashboardMountServesUI(t *testing.T) {
	srv := New("127.0.0.1:0")
	srv.MountDashboard(dashboard.New(fstest.MapFS{
		"index.html": {Data: []byte("<title>Homestead</title>")},
	}))
	for _, target := range []string{"/", "/status"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Homestead") {
			t.Fatalf("GET %s = %d %q, want shell", target, rec.Code, rec.Body.String())
		}
	}
}

func TestDashboardMountKeepsApiPrecedence(t *testing.T) {
	srv := New("127.0.0.1:0")
	srv.MountDashboard(dashboard.New(fstest.MapFS{
		"index.html": {Data: []byte("<title>Homestead</title>")},
	}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status"`) {
		t.Fatalf("GET /healthz = %d %q, want API", rec.Code, rec.Body.String())
	}
}
