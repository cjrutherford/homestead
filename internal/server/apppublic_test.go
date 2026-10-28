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
	// The setup wizard's domain resolves to the zone (S328/S329).
	srv.appPublic.setupZone = func() (string, string) { return "cloudflare", "example.com" }
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
	srv.appPublic.setupZone = nil
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
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "no Cloudflare zone") {
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
	cf.denyCerts = true
	plan = planAppPublic(t, srv, map[string]any{"enabled": true, "hostname": "photos.example.com"})
	if plan.Coverage[0].Checked || !strings.Contains(strings.Join(plan.Warnings, " "), router.CertificatePermission) {
		t.Fatalf("unreadable: %+v", plan)
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply",
		map[string]any{"enabled": true, "hostname": "photos.example.com", "digest": plan.Digest})
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
	if len(status.Coverage) != 1 || status.Coverage[0].Hostname != "photos.example.com" || !status.Coverage[0].Covered || !status.Coverage[0].Checked {
		t.Fatalf("status coverage = %+v", status.Coverage)
	}
}

// installJellyfin marks Jellyfin installed and gives it its edge route,
// as the install and the wizard's route step do.
func installJellyfin(t *testing.T, st *store.Store) {
	t.Helper()
	installHostApp(t, st, "jellyfin", "jellyfin", 8096)
}

// installHostApp records an installed app with a name of its own.
func installHostApp(t *testing.T, st *store.Store, app, service string, port int) {
	t.Helper()
	installApp(t, st, app, docker.Route{Host: docker.Hostname(app + ".lan"), Target: docker.Target{Host: service, Port: port},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
}

func installApp(t *testing.T, st *store.Store, app string, route docker.Route) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES (?, 'compose.yaml', 'x', ?)`,
		app, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	set, _ := loadEdgeRoutes(st)
	set.Routes = append(set.Routes, route)
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
}

func uninstallImmich(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB().Exec(`DELETE FROM generated_fingerprints WHERE stack = 'immich'`); err != nil {
		t.Fatal(err)
	}
	set, _ := loadEdgeRoutes(st)
	kept := set.Routes[:0]
	for _, r := range set.Routes {
		if r.Host != "immich.lan" {
			kept = append(kept, r)
		}
	}
	set.Routes = kept
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
	// Immich keeps a name of its own (ADR-044): start from it uninstalled.
	uninstallImmich(t, st)
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
		recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/plan", c.body)
		if recorder.Code != c.code || !strings.Contains(recorder.Body.String(), c.want) {
			t.Errorf("%v: %d %s", c.body, recorder.Code, recorder.Body.String())
		}
	}
	// The install wizard is offered the proposed name before the install.
	if recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/immich/public", nil); !strings.Contains(recorder.Body.String(), `"proposed":"immich.`) ||
		!strings.Contains(recorder.Body.String(), `"available":false`) {
		t.Fatalf("status before install: %s", recorder.Body.String())
	}
	body := map[string]any{"enabled": true, "before_install": true, "route": "immich.lan", "hostname": "immich.example.com"}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/plan", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", recorder.Code, recorder.Body.String())
	}
	var before appPublicView
	if err := json.Unmarshal(recorder.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Route != "immich.lan" || before.Hostname != "immich.example.com" || before.Digest == "" {
		t.Fatalf("plan = %+v", before)
	}
	// Not installed yet: the plan cannot be applied, with or without the flag.
	applyBody := map[string]any{"enabled": true, "route": "immich.lan", "hostname": "immich.example.com", "digest": before.Digest}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply", applyBody); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply before install: %d %s", rec.Code, rec.Body.String())
	}
	early := map[string]any{"enabled": true, "before_install": true, "route": "immich.lan", "digest": before.Digest}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply", early); rec.Code != http.StatusBadRequest {
		t.Fatalf("apply with before_install: %d %s", rec.Code, rec.Body.String())
	}

	installHostApp(t, st, "immich", "server", 2283)
	after := planAppPublicFor(t, srv, "immich", map[string]any{"enabled": true, "route": "immich.lan", "hostname": "immich.example.com"})
	if after.Digest != before.Digest {
		t.Fatalf("digest moved across the install:\nbefore %+v\nafter  %+v", before, after)
	}
	// Installed: before_install changes nothing.
	again := planAppPublicFor(t, srv, "immich", body)
	if again.Digest != before.Digest {
		t.Fatalf("before_install on an installed app: %+v", again)
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply", applyBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("op = %s: %s", op.Status, op.Error)
	}
	if fmt.Sprint(trusted) != "[[immich https://immich.lan immich.lan immich.local immich.example.com]]" {
		t.Fatalf("trusted = %v", trusted)
	}
	// A plan approved before something else changed is asked again.
	stale := map[string]any{"enabled": true, "route": "immich.lan", "hostname": "immich2.example.com", "digest": before.Digest}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/apply", stale); rec.Code != http.StatusConflict {
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

// ADR-045: a craft app still on a name of its own goes on the tunnel only
// after it moves to its path, and the panel says so instead of asking for
// a name. A name with a path, or the dashboard's own public name, is
// refused with the way to publish under the dashboard's name.
func TestAppPublicPointsToTheMove(t *testing.T) {
	srv, st, _, _ := appPublicRig(t)
	installApp(t, st, "cadcraft", docker.Route{Host: "cadcraft.lan", Target: docker.Target{Host: "cadcraft", Port: 8080},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, CraftApp: "cadcraft"})
	installHostApp(t, st, "nextcloud", "nextcloud", 80)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Dashboard {
			set.Routes[i].Tunnel = []docker.Hostname{"homestead.example.com"}
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/cadcraft/public", nil)
	var status appPublicStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Available || !strings.Contains(status.Reason, "Move it to /apps/cadcraft/ first") || status.Proposed != "" {
		t.Fatalf("status = %+v", status)
	}
	for _, c := range []struct {
		path string
		body map[string]any
		code int
		want string
	}{
		{"cadcraft", map[string]any{"enabled": true, "hostname": "homestead.example.com"}, http.StatusUnprocessableEntity, "Move it to /apps/cadcraft/ first"},
		{"nextcloud", map[string]any{"enabled": true, "hostname": "homestead.example.com/apps/nextcloud"}, http.StatusBadRequest,
			"is not a public name: a public name is a host name only"},
		{"nextcloud", map[string]any{"enabled": true, "hostname": "homestead.example.com/apps/nextcloud"}, http.StatusBadRequest,
			"move it there first (Review the move, or homesteadctl apps move nextcloud)"},
		{"nextcloud", map[string]any{"enabled": true, "hostname": "Homestead.example.com"}, http.StatusUnprocessableEntity,
			"is the dashboard's public name"},
		{"immich", map[string]any{"enabled": true, "hostname": "https://photos.example.com"}, http.StatusBadRequest, "with no path."},
	} {
		recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/"+c.path+"/public/plan", c.body)
		if recorder.Code != c.code || !strings.Contains(recorder.Body.String(), c.want) {
			t.Errorf("%s %v: %d %s, want %d %q", c.path, c.body, recorder.Code, recorder.Body.String(), c.code, c.want)
		}
	}
}

// The dashboard's name may sit a level below the zone; an app with a name
// of its own is proposed directly under the zone, never under the setup
// domain, and a name two levels down is refused (S329).
func TestAppPublicNameOneLevelUnderZone(t *testing.T) {
	srv, st, _, _ := appPublicRig(t)
	srv.setupDomain = func() string { return "homestead.example.com" }
	status := func() string {
		recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/immich/public", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status: %d %s", recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}
	// Zone from the setup zone resolver while no zone is stored.
	if body := status(); !strings.Contains(body, `"proposed":"immich.example.com"`) {
		t.Fatalf("resolver zone: %s", body)
	}
	plan := planAppPublic(t, srv, map[string]any{"enabled": true})
	if plan.Hostname != "immich.example.com" {
		t.Fatalf("default plan hostname = %q", plan.Hostname)
	}
	// The stored zone wins over the resolver.
	srv.appPublic.setupZone = func() (string, string) { return "cloudflare", "other.test" }
	if err := router.SetTunnelAccount(st, "tid", "abcd0000000000000000000000000001", "zone1", "example.com"); err != nil {
		t.Fatal(err)
	}
	if body := status(); !strings.Contains(body, `"proposed":"immich.example.com"`) {
		t.Fatalf("stored zone: %s", body)
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/immich/public/plan",
		map[string]any{"enabled": true, "hostname": "immich.homestead.example.com"})
	if recorder.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(recorder.Body.String(), "immich.homestead.example.com is more than one level below example.com") ||
		!strings.Contains(recorder.Body.String(), "such as immich.example.com") {
		t.Fatalf("two-level name: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestBelowZone(t *testing.T) {
	for host, want := range map[string]int{"example.com": 0, "a.example.com": 1, "a.b.example.com": 2, "x.other.test": 0} {
		if got := belowZone(host, "example.com"); got != want {
			t.Errorf("belowZone(%q) = %d, want %d", host, got, want)
		}
	}
}
