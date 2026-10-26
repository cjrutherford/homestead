package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/store"
)

type appPublicView struct {
	Action   string               `json:"action"`
	Hostname string               `json:"hostname"`
	Route    string               `json:"route"`
	Zone     string               `json:"zone"`
	DNS      []tunnelPlanChange   `json:"dns"`
	Before   []string             `json:"connector_before"`
	After    []string             `json:"connector_after"`
	Coverage []publicNameCoverage `json:"coverage"`
	Warnings []string             `json:"warnings"`
	Digest   string               `json:"digest"`
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
	if err := router.SetTunnelAccount(st, "tid", "abcd0000000000000000000000000001", "zone1", ""); err != nil {
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

// An adopted tunnel without a setup domain (S302): the stored zone name
// proposes the app's public name, and the remove plan names the zone,
// from the store or else from Cloudflare.
func TestAppPublicAdoptedTunnelZone(t *testing.T) {
	srv, st, _, _ := appPublicRig(t)
	srv.setupDomain = func() string { return "" }
	status := func() string {
		recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/immich/public", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status: %d %s", recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}
	if body := status(); strings.Contains(body, `"proposed"`) {
		t.Fatalf("no zone name yet, status = %s", body)
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/plan", map[string]any{"enabled": true})
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "no domain") {
		t.Fatalf("plan without a name: %d %s", recorder.Code, recorder.Body.String())
	}
	plan := planAppPublic(t, srv, map[string]any{"enabled": true, "hostname": "immich.example.com"})
	if plan.Zone != "example.com" {
		t.Fatalf("add plan zone = %q", plan.Zone)
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply",
		map[string]any{"enabled": true, "hostname": "immich.example.com", "digest": plan.Digest})
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", recorder.Code, recorder.Body.String())
	}
	if op := waitOperation(t, st, recorder.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("apply op = %s: %s", op.Status, op.Error)
	}
	if name, err := router.TunnelZoneName(st, "tid"); err != nil || name != "example.com" {
		t.Fatalf("stored zone name = %q, %v", name, err)
	}
	if body := status(); !strings.Contains(body, `"proposed":"immich.example.com"`) {
		t.Fatalf("status = %s", body)
	}
	if plan := planAppPublic(t, srv, map[string]any{"enabled": false}); plan.Zone != "example.com" {
		t.Fatalf("remove plan zone (stored) = %q", plan.Zone)
	}
	if _, err := st.DB().Exec(`UPDATE tunnel_routes SET zone_name = ''`); err != nil {
		t.Fatal(err)
	}
	if plan := planAppPublic(t, srv, map[string]any{"enabled": false}); plan.Zone != "example.com" {
		t.Fatalf("remove plan zone (Cloudflare) = %q", plan.Zone)
	}
	// An empty zone name keeps the stored one.
	if err := router.SetTunnelAccount(st, "tid", "abcd0000000000000000000000000001", "zone1", "example.com"); err != nil {
		t.Fatal(err)
	}
	if err := router.SetTunnelAccount(st, "tid", "abcd0000000000000000000000000001", "zone1", ""); err != nil {
		t.Fatal(err)
	}
	if name, _ := router.TunnelZoneName(st, "tid"); name != "example.com" {
		t.Fatalf("zone name after empty update = %q", name)
	}
}

// S312: a public app name two levels deep plans with a certificate
// warning, never a refusal, and the status shows the lamp.
func TestAppPublicCertificateCoverage(t *testing.T) {
	srv, st, cf, _ := appPublicRig(t)
	plan := planAppPublic(t, srv, map[string]any{"enabled": true})
	if len(plan.Coverage) != 1 || !plan.Coverage[0].Covered || strings.Contains(strings.Join(plan.Warnings, " "), "no certificate") {
		t.Fatalf("one level: %+v", plan)
	}
	plan = planAppPublic(t, srv, map[string]any{"enabled": true, "hostname": "photos.homestead.example.com"})
	if plan.Hostname != "photos.homestead.example.com" || len(plan.DNS) != 1 || plan.Coverage[0].Covered ||
		!strings.Contains(strings.Join(plan.Warnings, " "), router.UncoveredText("photos.homestead.example.com", "example.com")) {
		t.Fatalf("two levels: %+v", plan)
	}
	cf.denyCerts = true
	plan = planAppPublic(t, srv, map[string]any{"enabled": true, "hostname": "photos.homestead.example.com"})
	if plan.Coverage[0].Checked || !strings.Contains(strings.Join(plan.Warnings, " "), router.CertificatePermission) {
		t.Fatalf("unreadable: %+v", plan)
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply",
		map[string]any{"enabled": true, "hostname": "photos.homestead.example.com", "digest": plan.Digest})
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", recorder.Code, recorder.Body.String())
	}
	if op := waitOperation(t, st, recorder.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("apply op = %s: %s", op.Status, op.Error)
	}
	cf.denyCerts = false
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/immich/public", nil)
	var status struct {
		Coverage []publicNameCoverage `json:"coverage"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Coverage) != 1 || status.Coverage[0].Hostname != "photos.homestead.example.com" || status.Coverage[0].Covered || !status.Coverage[0].Checked {
		t.Fatalf("status coverage = %+v", status.Coverage)
	}
	cf.certHosts = append(cf.certHosts, "*.homestead.example.com")
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/immich/public", nil)
	if !strings.Contains(recorder.Body.String(), `"covered":true,"by":"certificate pack"`) {
		t.Fatalf("status = %s", recorder.Body.String())
	}
}

// installJellyfin marks Jellyfin installed and gives it its edge route,
// as the install and the wizard's route step do.
func installJellyfin(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES ('jellyfin', 'compose.yaml', 'x', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	set, _ := loadEdgeRoutes(st)
	set.Routes = append(set.Routes, docker.Route{Host: "jellyfin.lan", Target: docker.Target{Host: "jellyfin", Port: 8096},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
}

// S313: the install wizard and `apps install --public` plan the public
// name before the install, on the route the install creates. The plan is
// never applied as such: once the app is installed the same request,
// without before_install, must produce the same digest.
func TestAppPublicPlannedBeforeInstall(t *testing.T) {
	srv, st, _, _ := appPublicRig(t)
	var trusted [][]string
	old := trustAppHosts
	trustAppHosts = func(_ context.Context, m catalog.Manifest, address string, add, remove []string) error {
		trusted = append(trusted, append([]string{m.Name, address}, add...))
		return nil
	}
	t.Cleanup(func() { trustAppHosts = old })
	for _, c := range []struct {
		body map[string]any
		code int
		want string
	}{
		{map[string]any{"enabled": true, "before_install": true}, http.StatusBadRequest, "names the route"},
		{map[string]any{"enabled": false, "before_install": true}, http.StatusBadRequest, "only adding"},
		{map[string]any{"enabled": true, "before_install": true, "route": "authentik.lan"}, http.StatusUnprocessableEntity, "already a name"},
	} {
		recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/public/plan", c.body)
		if recorder.Code != c.code || !strings.Contains(recorder.Body.String(), c.want) {
			t.Errorf("%v: %d %s", c.body, recorder.Code, recorder.Body.String())
		}
	}
	// The install wizard is offered the proposed name before the install.
	if recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/jellyfin/public", nil); !strings.Contains(recorder.Body.String(), `"proposed":"jellyfin.`) ||
		!strings.Contains(recorder.Body.String(), `"available":false`) {
		t.Fatalf("status before install: %s", recorder.Body.String())
	}
	body := map[string]any{"enabled": true, "before_install": true, "route": "jellyfin.lan", "hostname": "jellyfin.homestead.example.com"}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/public/plan", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", recorder.Code, recorder.Body.String())
	}
	var before appPublicView
	if err := json.Unmarshal(recorder.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Route != "jellyfin.lan" || before.Hostname != "jellyfin.homestead.example.com" || before.Digest == "" {
		t.Fatalf("plan = %+v", before)
	}
	// The name is two levels below the zone: a warning, never a refusal.
	if !strings.Contains(strings.Join(before.Warnings, " "), "no certificate for jellyfin.homestead.example.com") {
		t.Fatalf("warnings = %v", before.Warnings)
	}
	// Not installed yet: the plan cannot be applied, with or without the flag.
	applyBody := map[string]any{"enabled": true, "route": "jellyfin.lan", "hostname": "jellyfin.homestead.example.com", "digest": before.Digest}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/public/apply", applyBody); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply before install: %d %s", rec.Code, rec.Body.String())
	}
	early := map[string]any{"enabled": true, "before_install": true, "route": "jellyfin.lan", "digest": before.Digest}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/public/apply", early); rec.Code != http.StatusBadRequest {
		t.Fatalf("apply with before_install: %d %s", rec.Code, rec.Body.String())
	}

	installJellyfin(t, st)
	after := planAppPublicFor(t, srv, "jellyfin", map[string]any{"enabled": true, "route": "jellyfin.lan", "hostname": "jellyfin.homestead.example.com"})
	if after.Digest != before.Digest {
		t.Fatalf("digest moved across the install:\nbefore %+v\nafter  %+v", before, after)
	}
	// Installed: before_install changes nothing.
	again := planAppPublicFor(t, srv, "jellyfin", body)
	if again.Digest != before.Digest {
		t.Fatalf("before_install on an installed app: %+v", again)
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/public/apply", applyBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("op = %s: %s", op.Status, op.Error)
	}
	if fmt.Sprint(trusted) != "[[jellyfin https://jellyfin.lan jellyfin.lan jellyfin.local jellyfin.homestead.example.com]]" {
		t.Fatalf("trusted = %v", trusted)
	}
	// A plan approved before something else changed is asked again.
	stale := map[string]any{"enabled": true, "route": "jellyfin.lan", "hostname": "jellyfin2.example.com", "digest": before.Digest}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/public/apply", stale); rec.Code != http.StatusConflict {
		t.Fatalf("stale: %d %s", rec.Code, rec.Body.String())
	}
}

// S313: Nextcloud's plan says what its trusted domains get; a failure in
// the app-hosts step stops the add before anything is published.
func TestAppPublicAppHostsStepFirst(t *testing.T) {
	srv, st, cf, _ := appPublicRig(t)
	old := trustAppHosts
	trustAppHosts = func(context.Context, catalog.Manifest, string, []string, []string) error {
		return fmt.Errorf("nextcloud is not set up")
	}
	t.Cleanup(func() { trustAppHosts = old })
	plan := planAppPublic(t, srv, map[string]any{"enabled": true})
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply", map[string]any{"enabled": true, "digest": plan.Digest})
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", recorder.Code, recorder.Body.String())
	}
	if op := waitOperation(t, st, recorder.Body.Bytes()); op.Status == operation.StatusSucceeded || !strings.Contains(op.Error, "not set up") {
		t.Fatalf("op = %s: %s", op.Status, op.Error)
	}
	if len(cf.records) != 0 {
		t.Fatalf("published anyway: %v", cf.records)
	}
}

func planAppPublicFor(t *testing.T, srv *Server, app string, body map[string]any) appPublicView {
	t.Helper()
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/"+app+"/public/plan", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", recorder.Code, recorder.Body.String())
	}
	var plan appPublicView
	if err := json.Unmarshal(recorder.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}
