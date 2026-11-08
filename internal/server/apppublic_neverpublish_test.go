package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

// S451: a manifest with publish: never is refused a public name on every
// path, is not offered one, and an existing name can still be removed.

func installKumaStack(t *testing.T, srv *Server) {
	t.Helper()
	if _, err := srv.appPublic.st.DB().Exec(`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES ('uptime-kuma', 'compose.yaml', 'x', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func TestNeverPublishRefusedOnEveryPlanPath(t *testing.T) {
	srv, _, _, _ := appPublicRig(t)
	for name, body := range map[string]map[string]any{
		"plan":                {"enabled": true},
		"named":               {"enabled": true, "hostname": "uptime-kuma.example.com"},
		"before install":      {"enabled": true, "before_install": true},
		"anticipated sign-in": {"enabled": true, "before_install": true, "anticipate_sign_in": true},
	} {
		recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/uptime-kuma/public/plan", body)
		if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "never published") {
			t.Errorf("%s: %d %s", name, recorder.Code, recorder.Body.String())
		}
	}
	// Apply carries the same refusal even with a digest.
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/uptime-kuma/public/apply", map[string]any{"enabled": true, "digest": "abc"})
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "never published") {
		t.Errorf("apply: %d %s", recorder.Code, recorder.Body.String())
	}
	// A package without the field still plans (Immich).
	if recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/plan", map[string]any{"enabled": true}); recorder.Code != http.StatusOK {
		t.Errorf("immich: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestNeverPublishStatusOffersNothing(t *testing.T) {
	srv, _, _, _ := appPublicRig(t)
	installKumaStack(t, srv)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/uptime-kuma/public", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status: %d %s", recorder.Code, recorder.Body.String())
	}
	var out appPublicStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.NeverPublish || out.Available || out.Proposed != "" || !strings.Contains(out.Reason, "never published") {
		t.Errorf("status = %+v", out)
	}
}

func TestNeverPublishExistingNameCanBeRemovedAndIsDrift(t *testing.T) {
	srv, st, _, _ := appPublicRig(t)
	installKumaStack(t, srv)
	set, _ := loadEdgeRoutes(st)
	set.Routes = append(set.Routes, docker.Route{Host: "uptime-kuma.lan", Target: docker.Target{Host: "uptime-kuma", Port: 3001},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Tunnel: []docker.Hostname{"uptime-kuma.example.com"}})
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	// Doctor reports the drift.
	checks := neverPublishChecks(DoctorLiveConfig{Store: st, CatalogDir: filepath.Join("..", "..", "catalog")})
	if len(checks) != 1 || checks[0].Status != "fail" || !strings.Contains(checks[0].Detail, "uptime-kuma.example.com") {
		t.Errorf("doctor = %+v", checks)
	}
	// Status keeps the Remove offer but not the add form.
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/uptime-kuma/public", nil)
	var out appPublicStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.NeverPublish || !out.Available {
		t.Errorf("status = %+v", out)
	}
	// Removal is planned, not refused.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/uptime-kuma/public/plan", map[string]any{"enabled": false, "hostname": "uptime-kuma.example.com"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("remove plan: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestNeverPublishDoctorCleanWithoutName(t *testing.T) {
	srv, st, _, _ := appPublicRig(t)
	_ = srv
	checks := neverPublishChecks(DoctorLiveConfig{Store: st, CatalogDir: filepath.Join("..", "..", "catalog")})
	if len(checks) != 1 || checks[0].Status != "ok" {
		t.Errorf("doctor = %+v", checks)
	}
}

// No manifest prose claims "never published" unless its publish form is
// never (ADR-061; the S451 never_publish field is gone).
func TestNeverPublishFieldMatchesProse(t *testing.T) {
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range entries.Names() {
		m, _ := entries.Get(name)
		text := strings.ToLower(m.Description + " " + strings.Join(m.Exposure.Endpoints[0].Warnings, " "))
		claims := strings.Contains(text, "never published") || strings.Contains(text, "can never be published") || strings.Contains(text, "refused by design")
		if claims && m.PublishForm() != catalog.PublishNever {
			t.Errorf("%s: prose claims never-published but publish is %s", name, m.PublishForm())
		}
	}
	m, _ := entries.Get("uptime-kuma")
	if m.PublishForm() != catalog.PublishNever || m.PublicRefusal() == "" {
		t.Error("uptime-kuma must be never_publish")
	}
}

// The install itself refuses a public name for a never-publish app, both
// the wizard's setup.public and the bare public_name.
func TestNeverPublishInstallRefusesPublicName(t *testing.T) {
	installRigMealieExtra = "publish: never\n"
	t.Cleanup(func() { installRigMealieExtra = "" })
	a := installHandlerRig(t)
	post := func(body map[string]any) (int, string) {
		raw, _ := json.Marshal(body)
		rec := a.req(http.MethodPost, "/api/v1/catalog/mealie/install", string(raw), a.owner.token)
		return rec.Code, rec.Body.String()
	}
	base := map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 9000}}
	for name, extra := range map[string]map[string]any{
		"public_name":  {"public_name": "mealie.example.com"},
		"setup.public": {"setup": map[string]any{"public": map[string]any{"hostname": "mealie.example.com", "route": "mealie.lan", "digest": "x"}}},
	} {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		for k, v := range extra {
			body[k] = v
		}
		if code, got := post(body); code != http.StatusUnprocessableEntity || !strings.Contains(got, "never published") {
			t.Errorf("%s: %d %s", name, code, got)
		}
	}
}
