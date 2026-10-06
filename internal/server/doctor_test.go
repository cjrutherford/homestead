package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoctorEndpoint(t *testing.T) {
	srv := New("127.0.0.1:0")
	srv.MountDoctor()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/doctor", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"overall"`, `"checks"`, `"os-distro"`, `"memory"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}
