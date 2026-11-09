package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/store"
)

// seedPathAuthentik records the dashboard and Authentik served at
// /apps/auth/ under it (ADR-044).
func seedPathAuthentik(t *testing.T, srv *Server, st *store.Store, linked bool) {
	t.Helper()
	seedTunnelEdge(t, srv, st, false, linked)
	set, _ := loadEdgeRoutes(st)
	set.Routes = append(set.Routes, docker.Route{Host: "authentik.lan", Target: docker.Target{Host: "server", Port: 9000},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Path: "/apps/auth/"})
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
}

type tunnelPathView struct {
	Role, Route, Path, URL string
}

// ADR-044 §4: the tunnel no longer proposes auth.<domain>. Authentik
// answers at the dashboard's public name under /apps/auth/, published
// with it: no ingress or DNS record of its own.
func TestTunnelPlanPathAuthentik(t *testing.T) {
	srv, st, secrets := tunnelTestServer(t)
	cf := newFakeCloudflare(t)
	fakeConnector(t)
	fakeEdge(t)
	if err := secrets.Set("dns_cloudflare_api_token", "sealed-token"); err != nil {
		t.Fatal(err)
	}
	seedPathAuthentik(t, srv, st, false)
	// Not linked: the dashboard is not published, so neither is the
	// sign-in under it, and there is nothing to publish.
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", map[string]any{"name": "homestead"})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "homestead.example.com/apps/auth/") {
		t.Fatalf("unlinked: %d %s", rec.Code, rec.Body.String())
	}
	srv.signInOffered = func() bool { return true }
	var status struct{ Proposed tunnelProposal }
	if err := json.Unmarshal(serveRequest(t, srv, http.MethodGet, "/api/v1/tunnel/status", nil).Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Proposed.DashboardHostname != "homestead.example.com" || status.Proposed.AuthHostname != "" {
		t.Fatalf("proposed = %+v", status.Proposed)
	}
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", map[string]any{"name": "homestead", "auth_hostname": "auth.example.com"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "/apps/auth/") {
		t.Fatalf("auth name: %d %s", rec.Code, rec.Body.String())
	}
	request := map[string]any{"name": "homestead"}
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", request)
	var paths struct {
		Paths []tunnelPathView `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &paths); err != nil {
		t.Fatal(err)
	}
	plan := planTunnel(t, srv, request)
	if plan.AuthHostname != "" || len(plan.Ingress) != 1 || plan.Ingress[0].Hostname != "homestead.example.com" ||
		len(plan.DNS) != 1 || len(plan.Omitted) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if len(paths.Paths) != 1 || paths.Paths[0] != (tunnelPathView{Role: "authentik", Route: "authentik.lan", Path: "/apps/auth/",
		URL: "https://homestead.example.com/apps/auth/"}) {
		t.Fatalf("paths = %+v", paths.Paths)
	}
	if !strings.Contains(plan.SignIn, "https://homestead.example.com/apps/auth/") || strings.Contains(rec.Body.String(), "auth.example.com") {
		t.Fatalf("sign-in = %q", plan.SignIn)
	}
	approve := map[string]any{"name": "homestead", "digest": plan.Digest}
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", approve)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("setup op = %s: %s", op.Status, op.Error)
	}
	if len(cf.records) != 1 {
		t.Fatalf("records = %v", cf.records)
	}
	set, _ := loadEdgeRoutes(st)
	for _, r := range set.Routes {
		switch {
		case r.Dashboard && fmt.Sprint(r.Tunnel) != "[homestead.example.com]":
			t.Fatalf("dashboard = %+v", r)
		case r.Host == "authentik.lan" && (!r.Published || len(r.Tunnel) != 0):
			t.Fatalf("authentik = %+v", r)
		}
	}
	// Taking the names away unpublishes the path too.
	setTunnelNames(set.Routes, nil)
	for _, r := range set.Routes {
		if r.Published || len(r.Tunnel) != 0 {
			t.Fatalf("after teardown = %+v", r)
		}
	}
}

type pathPublicView struct {
	appPublicView
	Path string `json:"path"`
	URL  string `json:"url"`
}

// pathPublicRig is appPublicRig with the dashboard published and
// Nextcloud installed by path (ADR-044).
func pathPublicRig(t *testing.T, publishDashboard bool) (*Server, *store.Store, *fakeCloudflare, *[][]string) {
	t.Helper()
	srv, st, cf, _ := appPublicRig(t)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Dashboard && publishDashboard {
			set.Routes[i].Tunnel = []docker.Hostname{"homestead.example.com"}
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	if publishDashboard {
		if err := router.RecordRoute(st, router.TunnelRoute{Hostname: "homestead.example.com", TunnelID: "tid", Service: tunnelEntryService}, "homestead"); err != nil {
			t.Fatal(err)
		}
	}
	installApp(t, st, "nextcloud", docker.Route{Host: "nextcloud.lan", Target: docker.Target{Host: "nextcloud", Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Path: "/apps/nextcloud/", StripPrefix: true})
	trusted := &[][]string{}
	old := trustAppHosts
	trustAppHosts = func(_ context.Context, m catalog.Manifest, address string, add, remove []string) error {
		entry := append([]string{m.Name, address, "+"}, add...)
		*trusted = append(*trusted, append(append(entry, "-"), remove...))
		return nil
	}
	t.Cleanup(func() { trustAppHosts = old })
	return srv, st, cf, trusted
}

func planPathPublic(t *testing.T, srv *Server, body map[string]any) pathPublicView {
	t.Helper()
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/public/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
	}
	var plan pathPublicView
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

// ADR-044 §3: publishing a path app is a digest-bound plan that shows
// the path and public URL and adds no DNS record; unpublishing takes it
// off again. The dashboard keeps its name throughout.
func TestAppPublicPathAppPublishUnpublish(t *testing.T) {
	srv, st, cf, trusted := pathPublicRig(t, true)
	records := len(cf.records)
	status := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/nextcloud/public", nil).Body.String()
	if !strings.Contains(status, `"proposed":"homestead.example.com"`) || !strings.Contains(status, `"proposed_url":"https://homestead.example.com/apps/nextcloud/"`) ||
		!strings.Contains(status, `"path":"/apps/nextcloud/"`) {
		t.Fatalf("status = %s", status)
	}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/public/plan",
		map[string]any{"enabled": true, "hostname": "nextcloud.example.com"}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "no name of its own") {
		t.Fatalf("own name: %d %s", rec.Code, rec.Body.String())
	}
	plan := planPathPublic(t, srv, map[string]any{"enabled": true})
	if plan.Action != "add" || plan.Hostname != "homestead.example.com" || plan.Route != "nextcloud.lan" || plan.Path != "/apps/nextcloud/" ||
		plan.URL != "https://homestead.example.com/apps/nextcloud/" || len(plan.DNS) != 0 || plan.Digest == "" {
		t.Fatalf("plan = %+v", plan)
	}
	if fmt.Sprint(plan.Before) != fmt.Sprint(plan.After) || len(plan.Coverage) != 1 || !plan.Coverage[0].Covered {
		t.Fatalf("connector/coverage = %+v", plan)
	}
	warnings := strings.Join(plan.Warnings, " ")
	if !strings.Contains(warnings, "No DNS record changes") || !strings.Contains(warnings, "one web origin") ||
		!strings.Contains(warnings, "are redirected (308, path and query kept) to https://homestead.example.com/apps/nextcloud/") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
	// The digest is stable and bound: a changed request is a changed plan.
	if again := planPathPublic(t, srv, map[string]any{"enabled": true}); again.Digest != plan.Digest {
		t.Fatalf("digest moved: %s %s", again.Digest, plan.Digest)
	}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/public/apply",
		map[string]any{"enabled": true, "digest": "stale"}); rec.Code != http.StatusConflict {
		t.Fatalf("stale: %d", rec.Code)
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/public/apply", map[string]any{"enabled": true, "digest": plan.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("op = %s: %s", op.Status, op.Error)
	}
	route := func(host docker.Hostname) docker.Route {
		set, _ := loadEdgeRoutes(st)
		for _, r := range set.Routes {
			if r.Host == host || (host == "" && r.Dashboard) {
				return r
			}
		}
		t.Fatalf("no route %s", host)
		return docker.Route{}
	}
	if r := route("nextcloud.lan"); !r.Published || len(r.Tunnel) != 0 {
		t.Fatalf("nextcloud = %+v", r)
	}
	if r := route(""); fmt.Sprint(r.Tunnel) != "[homestead.example.com]" {
		t.Fatalf("dashboard lost its name: %+v", r)
	}
	if r := route("authentik.lan"); fmt.Sprint(r.Tunnel) != "[auth.example.com]" {
		t.Fatalf("authentik lost its name: %+v", r)
	}
	if len(cf.records) != records {
		t.Fatalf("a path app added DNS: %v", cf.records)
	}
	if fmt.Sprint(*trusted) != "[[nextcloud https://homestead.example.com/apps/nextcloud + "+dashLAN+" homestead.local homestead.example.com -]]" {
		t.Fatalf("trusted = %v", *trusted)
	}
	*trusted = nil
	// Published already: adding again refuses; the status shows the URL.
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/public/plan", map[string]any{"enabled": true}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("again: %d %s", rec.Code, rec.Body.String())
	}
	if status := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/nextcloud/public", nil).Body.String(); !strings.Contains(status, `"url":"https://homestead.example.com/apps/nextcloud/"`) {
		t.Fatalf("status = %s", status)
	}
	off := planPathPublic(t, srv, map[string]any{"enabled": false})
	if off.Action != "remove" || off.URL != plan.URL || len(off.DNS) != 0 || off.Digest == "" || off.Digest == plan.Digest {
		t.Fatalf("remove plan = %+v", off)
	}
	if w := strings.Join(off.Warnings, " "); !strings.Contains(w, "the redirect from the local address /apps/nextcloud/") {
		t.Fatalf("remove warnings = %v", off.Warnings)
	}
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/public/apply", map[string]any{"enabled": false, "digest": off.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("op = %s: %s", op.Status, op.Error)
	}
	if r := route("nextcloud.lan"); r.Published {
		t.Fatalf("still published: %+v", r)
	}
	if r := route(""); fmt.Sprint(r.Tunnel) != "[homestead.example.com]" {
		t.Fatalf("dashboard lost its name: %+v", r)
	}
	if fmt.Sprint(*trusted) != "[[nextcloud https://homestead.example.com/apps/nextcloud + - homestead.example.com]]" {
		t.Fatalf("untrusted = %v", *trusted)
	}
}

// A path app answers under the dashboard's public name, so it cannot be
// published before the dashboard is.
func TestAppPublicPathAppNeedsPublicDashboard(t *testing.T) {
	srv, _, _, _ := pathPublicRig(t, false)
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/public/plan", map[string]any{"enabled": true})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Publish the dashboard") {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
	}
}

// ADR-044 §4: dashboard sign-in plans its issuer from Authentik under the
// dashboard's own name, and the public login from its public name once
// the path is published.
func TestSSODefaultsPathAuthentik(t *testing.T) {
	srv, st, _ := tunnelTestServer(t)
	seedPathAuthentik(t, srv, st, true)
	if d := ssoDefaults(st, 8080); d.AuthentikURL != "https://"+dashLAN+"/apps/auth" || d.PublicAuthentikURL != "" {
		t.Fatalf("local only = %+v", d)
	}
	set, _ := loadEdgeRoutes(st)
	setTunnelNames(set.Routes, map[docker.Hostname][]docker.Hostname{
		docker.Hostname(dashLAN): {"homestead.example.com"}, "authentik.lan": {"homestead.example.com"}})
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	d := ssoDefaults(st, 8080)
	if d.AuthentikURL != "https://"+dashLAN+"/apps/auth" || d.PublicAuthentikURL != "https://homestead.example.com/apps/auth" ||
		!strings.Contains(strings.Join(d.PublicOrigins, " "), "https://homestead.example.com") {
		t.Fatalf("published = %+v", d)
	}
}
