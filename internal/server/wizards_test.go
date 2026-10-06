package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/store"
	"homestead/internal/wizard"
)

func wizardServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New("127.0.0.1:0")
	srv.MountWizards(wizard.New(st))
	return srv
}

func TestWizardCrud(t *testing.T) {
	srv := wizardServer(t)
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/wizards/claim", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want 404", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/wizards/claim", `{"step":"admin","payload":{}}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"step":"admin"`) {
		t.Fatalf("body = %s, want step", rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, "/api/v1/wizards/claim", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"step":"admin"`) {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodDelete, "/api/v1/wizards/claim", "", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodGet, "/api/v1/wizards/claim", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE status = %d, want 404", rec.Code)
	}
}

func TestWizardValidation(t *testing.T) {
	srv := wizardServer(t)
	for _, tc := range []struct {
		name    string
		method  string
		target  string
		payload string
		status  int
	}{
		{"malformed", "PUT", "/api/v1/wizards/claim", "{oops", http.StatusBadRequest},
		{"empty step", "PUT", "/api/v1/wizards/claim", `{"step":""}`, http.StatusBadRequest},
		{"bad id get", "GET", "/api/v1/wizards/Bad!", "", http.StatusBadRequest},
		{"bad id delete", "DELETE", "/api/v1/wizards/Bad!", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.payload))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}
