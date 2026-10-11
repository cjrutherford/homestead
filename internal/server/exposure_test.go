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
	"homestead/internal/wizard"
)

func exposureTestServer(t *testing.T, withReview bool) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if withReview {
		wiz := exposure.NewWizard(wizard.New(s))
		raw, err := json.Marshal(docker.Route{
			Host:     "app.example.com",
			Target:   docker.Target{Host: "app", Port: 8080},
			Exposure: docker.ExposureLocal,
			TLS:      docker.TLSInternal,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		payload := map[string]any{
			"route": decoded, "to": "public",
			"zone": "example.com", "public_ip": "203.0.113.7",
		}
		if err := wiz.Save("done", payload); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	srv := New("127.0.0.1:0")
	srv.MountExposure(s, 8080)
	return srv
}

func TestExposureReview(t *testing.T) {
	srv := exposureTestServer(t, true)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/exposure/review", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var plan struct {
		Host string `json:"Host"`
		To   string `json:"To"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if plan.Host != "app.example.com" || plan.To != "public" {
		t.Fatalf("plan = %+v", plan)
	}
	// Nothing saved reads as missing, never empty.
	bare := exposureTestServer(t, false)
	recorder = serveRequest(t, bare, http.MethodGet, "/api/v1/exposure/review", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestExposureApprove(t *testing.T) {
	srv := exposureTestServer(t, true)
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/exposure/approvals",
		map[string]any{"actor": "owner", "networks": []string{"203.0.113.0/24"}})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "app.example.com") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/exposure/approvals", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "owner") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	// Missing actors refuse before planning anything.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/exposure/approvals",
		map[string]any{"networks": []string{"203.0.113.0/24"}})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Approval without review reads as missing.
	bare := exposureTestServer(t, false)
	recorder = serveRequest(t, bare, http.MethodPost, "/api/v1/exposure/approvals",
		map[string]any{"actor": "owner"})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestExposureApply(t *testing.T) {
	srv := exposureTestServer(t, true)
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/exposure/approvals",
		map[string]any{"actor": "owner", "networks": []string{"203.0.113.0/24"}})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/exposure/apply",
		map[string]any{"host": "app.example.com"})
	// No daemon runs firewall or proxy here: the route accepted a
	// reviewed approval and returned an operation; execution fails
	// async on privilege.
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
	// Unapproved hosts refuse before running anything.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/exposure/apply",
		map[string]any{"host": "other.example.com"})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Malformed bodies refuse.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/exposure/apply", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
}
