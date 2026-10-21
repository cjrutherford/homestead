package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/store"
)

type appPublicView struct {
	Action   string             `json:"action"`
	Hostname string             `json:"hostname"`
	Route    string             `json:"route"`
	DNS      []tunnelPlanChange `json:"dns"`
	Before   []string           `json:"connector_before"`
	After    []string           `json:"connector_after"`
	Warnings []string           `json:"warnings"`
	Digest   string             `json:"digest"`
}

// appPublicRig is a recorded tunnel carrying Authentik, with Immich
// installed on the edge and the real catalog.
func appPublicRig(t *testing.T) (*Server, *store.Store, *fakeCloudflare, func() []string) {
	t.Helper()
	srv, st, secrets := tunnelTestServer(t)
	srv.MountAppPublic(st, secrets, filepath.Join("..", "..", "catalog"))
	cf := newFakeCloudflare(t)
	fakeConnector(t)
	var calls []string
	tunnelSystemctl = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	fakeEdge(t)
	dir := t.TempDir()
	old := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(dir, name) }
	t.Cleanup(func() { stackDirFor = old })
	if _, err := st.DB().Exec(`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES ('immich', 'compose.yaml', 'x', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Set("dns_cloudflare_api_token", "sealed-token"); err != nil {
		t.Fatal(err)
	}
	seedTunnelEdge(t, srv, st, true, true)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Host == "authentik.lan" {
			set.Routes[i].Tunnel = []docker.Hostname{"auth.example.com"}
		}
	}
	set.Routes = append(set.Routes, docker.Route{Host: "immich.lan", Target: docker.Target{Host: "server", Port: 2283},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	if err := router.RecordRoute(st, router.TunnelRoute{Hostname: "auth.example.com", TunnelID: "tid", Service: tunnelEntryService}, "homestead"); err != nil {
		t.Fatal(err)
	}
	if err := router.SetTunnelAccount(st, "tid", "abcd0000000000000000000000000001", "zone1"); err != nil {
		t.Fatal(err)
	}
	cf.tunnels["tid"] = "homestead"
	return srv, st, cf, func() []string { out := calls; calls = nil; return out }
}

func planAppPublic(t *testing.T, srv *Server, body map[string]any) appPublicView {
	t.Helper()
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/plan", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", recorder.Code, recorder.Body.String())
	}
	var plan appPublicView
	if err := json.Unmarshal(recorder.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func applyAppPublic(t *testing.T, srv *Server, st *store.Store, enabled bool, digest string) {
	t.Helper()
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply", map[string]any{"enabled": enabled, "digest": digest})
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", recorder.Code, recorder.Body.String())
	}
	if op := waitOperation(t, st, recorder.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("apply op = %s: %s", op.Status, op.Error)
	}
}

func TestAppPublicRefusals(t *testing.T) {
	srv, _, _, _ := appPublicRig(t)
	for _, c := range []struct {
		path string
		body map[string]any
		code int
		want string
	}{
		{"immich", map[string]any{"enabled": true, "hostname": "photos.lan"}, http.StatusBadRequest, "local name"},
		{"immich", map[string]any{"enabled": true, "hostname": "photos.other.org"}, http.StatusUnprocessableEntity, "no Cloudflare zone"},
		{"immich", map[string]any{"enabled": true, "route": "nope.lan"}, http.StatusBadRequest, "not one of"},
		{"immich", map[string]any{"enabled": true, "hostname": "auth.example.com"}, http.StatusUnprocessableEntity, "already a name"},
		{"immich", map[string]any{"enabled": false}, http.StatusUnprocessableEntity, "has no public name"},
		{"immich", map[string]any{"enabled": true, "digest": "x"}, http.StatusBadRequest, "no digest"},
		{"immich", map[string]any{}, http.StatusBadRequest, "enabled"},
		{"authentik", map[string]any{"enabled": true}, http.StatusUnprocessableEntity, "tunnel setup"},
		{"jellyfin", map[string]any{"enabled": true}, http.StatusUnprocessableEntity, "Install"},
		{"ghost", map[string]any{"enabled": true}, http.StatusNotFound, "unknown"},
	} {
		recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/"+c.path+"/public/plan", c.body)
		if recorder.Code != c.code || !strings.Contains(recorder.Body.String(), c.want) {
			t.Errorf("%s %v: %d %s, want %d %q", c.path, c.body, recorder.Code, recorder.Body.String(), c.code, c.want)
		}
	}
}

// A public name is added and removed as one: CNAME, edge name, connector
// entry. The record it replaced comes back on removal.
func TestAppPublicAddRemove(t *testing.T) {
	srv, st, cf, systemctl := appPublicRig(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/immich/public", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"proposed":"immich.example.com"`) ||
		!strings.Contains(recorder.Body.String(), `"available":true`) {
		t.Fatalf("status: %d %s", recorder.Code, recorder.Body.String())
	}
	cf.addRecord("A", "immich.example.com", "203.0.113.7")
	plan := planAppPublic(t, srv, map[string]any{"enabled": true})
	if plan.Action != "add" || plan.Hostname != "immich.example.com" || plan.Route != "immich.lan" ||
		len(plan.DNS) != 1 || plan.DNS[0].Action != "replace" || plan.DNS[0].Content != "tid.cfargotunnel.com" ||
		fmt.Sprint(plan.Before) != "[auth.example.com]" || fmt.Sprint(plan.After) != "[auth.example.com immich.example.com]" {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.Contains(strings.Join(plan.Warnings, " "), "no Authentik check") ||
		!strings.Contains(strings.Join(plan.Warnings, " "), "App sign-in through Authentik is off") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
	if stale := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply", map[string]any{"enabled": true, "digest": "stale"}); stale.Code != http.StatusConflict {
		t.Fatalf("stale: %d", stale.Code)
	}
	applyAppPublic(t, srv, st, true, plan.Digest)
	if len(cf.records) != 1 {
		t.Fatalf("records = %v", cf.records)
	}
	for _, record := range cf.records {
		if record["type"] != "CNAME" || record["content"] != "tid.cfargotunnel.com" {
			t.Fatalf("record = %v", record)
		}
	}
	set, _ := loadEdgeRoutes(st)
	names := tunnelNameMap(set.Routes)
	if fmt.Sprint(names["immich.lan"]) != "[immich.example.com]" || fmt.Sprint(names["authentik.lan"]) != "[auth.example.com]" {
		t.Fatalf("edge names = %v", names)
	}
	config, _ := os.ReadFile(connectorConfigPath())
	if !strings.Contains(string(config), "hostname: immich.example.com") || !strings.Contains(string(config), "hostname: auth.example.com") {
		t.Fatalf("config = %s", config)
	}
	if calls := systemctl(); fmt.Sprint(calls) != "[is-active --quiet homestead-tunnel restart homestead-tunnel]" {
		t.Fatalf("systemctl = %v", calls)
	}
	// Nothing changed, so the running connector is left alone.
	if err := syncConnector(st, "tid"); err != nil {
		t.Fatal(err)
	}
	if calls := systemctl(); fmt.Sprint(calls) != "[is-active --quiet homestead-tunnel]" {
		t.Fatalf("unchanged sync systemctl = %v", calls)
	}

	plan = planAppPublic(t, srv, map[string]any{"enabled": false})
	if plan.Action != "remove" || plan.Hostname != "immich.example.com" || len(plan.DNS) != 2 ||
		plan.DNS[0].Action != "delete" || plan.DNS[1].Action != "restore" || fmt.Sprint(plan.After) != "[auth.example.com]" {
		t.Fatalf("remove plan = %+v", plan)
	}
	applyAppPublic(t, srv, st, false, plan.Digest)
	if len(cf.records) != 1 {
		t.Fatalf("records = %v", cf.records)
	}
	for _, record := range cf.records {
		if record["type"] != "A" || record["content"] != "203.0.113.7" {
			t.Fatalf("restored = %v", record)
		}
	}
	set, _ = loadEdgeRoutes(st)
	if names := tunnelNameMap(set.Routes); len(names["immich.lan"]) != 0 || len(names["authentik.lan"]) != 1 {
		t.Fatalf("edge names = %v", names)
	}
	if config, _ := os.ReadFile(connectorConfigPath()); strings.Contains(string(config), "immich") {
		t.Fatalf("config = %s", config)
	}
	routes, _ := router.ListRoutes(st)
	if len(routes) != 1 {
		t.Fatalf("routes = %v", routes)
	}
	for _, action := range []string{"app.public.add", "app.public.remove"} {
		var n int
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM audit_entries WHERE action = ?`, action).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s audit = %d, %v", action, n, err)
		}
	}
}
