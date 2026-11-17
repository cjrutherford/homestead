package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
)

type baseURLView struct {
	Action      string   `json:"action"`
	AppSettings []string `json:"app_settings"`
	Digest      string   `json:"digest"`
}

// mealieRig installs Mealie on the edge with a stack whose BASE_URL is
// baseURL, and replaces the configure seam with a recorder (S407).
func mealieRig(t *testing.T, baseURL string, tunnel ...docker.Hostname) (*Server, *store.Store, *[]string) {
	t.Helper()
	srv, st, _, _ := appPublicRig(t)
	installApp(t, st, "mealie", docker.Route{Host: "mealie.lan", Target: docker.Target{Host: "mealie", Port: 9000},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Tunnel: tunnel})
	dir := stackDirFor("mealie")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  mealie:\n    environment:\n      BASE_URL: " + baseURL + "\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	old := moveConfigure
	moveConfigure = func(plan catalog.ConfigPlan, dir, project string, st *store.Store, secrets catalog.SecretStore) (operation.Definition, error) {
		return operation.Definition{Type: "catalog.configure", Steps: []operation.StepDef{
			{Name: "write-config", Run: func(context.Context) error {
				for _, c := range plan.Changes {
					calls = append(calls, "set "+c.Service+" BASE_URL="+c.Set["BASE_URL"])
				}
				return nil
			}},
			{Name: "up", Run: func(context.Context) error { calls = append(calls, "up"); return nil }},
		}}, nil
	}
	t.Cleanup(func() { moveConfigure = old })
	return srv, st, &calls
}

func planMealie(t *testing.T, srv *Server, body map[string]any) baseURLView {
	t.Helper()
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/mealie/public/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
	}
	var v baseURLView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func applyMealie(t *testing.T, srv *Server, st *store.Store, enabled bool, hostname, digest string) {
	t.Helper()
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/mealie/public/apply",
		map[string]any{"enabled": enabled, "hostname": hostname, "digest": digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("apply op = %s: %s", op.Status, op.Error)
	}
}

func TestAppPublicAddRewritesBaseURL(t *testing.T) {
	srv, st, calls := mealieRig(t, "https://mealie.lan")
	plan := planMealie(t, srv, map[string]any{"enabled": true, "hostname": "mealie.example.com"})
	joined := strings.Join(plan.AppSettings, "\n")
	if plan.Action != "add" || !strings.Contains(joined, "mealie: BASE_URL: https://mealie.lan -> https://mealie.example.com") {
		t.Fatalf("plan = %+v", plan)
	}
	// The digest covers the setting: a stack already on the public value
	// plans a different change.
	if err := os.WriteFile(filepath.Join(stackDirFor("mealie"), "compose.yaml"),
		[]byte("services:\n  mealie:\n    environment:\n      BASE_URL: https://mealie.example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	same := planMealie(t, srv, map[string]any{"enabled": true, "hostname": "mealie.example.com"})
	if same.Digest == plan.Digest || strings.Contains(strings.Join(same.AppSettings, "\n"), "BASE_URL") {
		t.Fatalf("current stack plan = %+v", same)
	}
	if err := os.WriteFile(filepath.Join(stackDirFor("mealie"), "compose.yaml"),
		[]byte("services:\n  mealie:\n    environment:\n      BASE_URL: https://mealie.lan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	applyMealie(t, srv, st, true, "mealie.example.com", plan.Digest)
	if got := strings.Join(*calls, ";"); got != "set mealie BASE_URL=https://mealie.example.com;up" {
		t.Fatalf("calls = %s", got)
	}
}

func TestAppPublicRemoveRestoresLocalBaseURL(t *testing.T) {
	srv, st, calls := mealieRig(t, "https://mealie.example.com", "mealie.example.com")
	plan := planMealie(t, srv, map[string]any{"enabled": false})
	if plan.Action != "remove" || !strings.Contains(strings.Join(plan.AppSettings, "\n"), "https://mealie.example.com -> https://mealie.lan") {
		t.Fatalf("plan = %+v", plan)
	}
	applyMealie(t, srv, st, false, "", plan.Digest)
	if got := strings.Join(*calls, ";"); got != "set mealie BASE_URL=https://mealie.lan;up" {
		t.Fatalf("calls = %s", got)
	}
}

// An app published before its base URL followed the public name is
// fixed by planning the same public name again.
func TestAppPublicRefreshesStaleBaseURL(t *testing.T) {
	srv, st, calls := mealieRig(t, "https://mealie.lan", "mealie.example.com")
	plan := planMealie(t, srv, map[string]any{"enabled": true, "hostname": "mealie.example.com"})
	if plan.Action != "refresh" || !strings.Contains(strings.Join(plan.AppSettings, "\n"), "https://mealie.lan -> https://mealie.example.com") {
		t.Fatalf("plan = %+v", plan)
	}
	applyMealie(t, srv, st, true, "mealie.example.com", plan.Digest)
	if got := strings.Join(*calls, ";"); got != "set mealie BASE_URL=https://mealie.example.com;up" {
		t.Fatalf("calls = %s", got)
	}
}

func TestAppPublicRefreshWithNothingToDoIsRefused(t *testing.T) {
	srv, _, _ := mealieRig(t, "https://mealie.example.com", "mealie.example.com")
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/mealie/public/plan",
		map[string]any{"enabled": true, "hostname": "mealie.example.com"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
	}
}
