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

type appMoveView struct {
	App         string   `json:"app"`
	Offered     bool     `json:"offered"`
	Reason      string   `json:"reason"`
	Route       string   `json:"route"`
	From        string   `json:"from"`
	Path        string   `json:"path"`
	To          []string `json:"to"`
	StripPrefix bool     `json:"strip_prefix"`
	Redirects   []string `json:"redirects"`
	AppSettings []string `json:"app_settings"`
	SignIn      []string `json:"sign_in"`
	Warnings    []string `json:"warnings"`
	Digest      string   `json:"digest"`
}

// moveRig is appPublicRig with the move mounted and its stack seams
// recorded: configuration changes, stops and seed writes.
func moveRig(t *testing.T) (*Server, *store.Store, *[]string, *[][]string) {
	srv, st, steps, trusted, _ := moveRigCF(t)
	return srv, st, steps, trusted
}

// moveRigCF is moveRig with the fake Cloudflare the public rig runs.
func moveRigCF(t *testing.T) (*Server, *store.Store, *[]string, *[][]string, *fakeCloudflare) {
	t.Helper()
	srv, st, cf, _ := appPublicRig(t)
	srv.MountAppMove(st, srv.appPublic.secrets, filepath.Join("..", "..", "catalog"))
	steps := &[]string{}
	prevConf, prevStop, prevStart, prevSeeds, prevApply := moveConfigure, moveStop, moveStart, moveSeeds, moveApplySeed
	moveConfigure = func(plan catalog.ConfigPlan, dir, project string, _ *store.Store, _ catalog.SecretStore) (operation.Definition, error) {
		return operation.Definition{Type: "catalog.configure", Steps: []operation.StepDef{{Name: "write-config", Run: func(context.Context) error {
			for _, c := range plan.Changes {
				for k, v := range c.Set {
					*steps = append(*steps, "env "+c.Service+" "+k+"="+v)
				}
			}
			*steps = append(*steps, "up "+project)
			return nil
		}}}}, nil
	}
	moveStop = func(_ context.Context, _, project string) error {
		*steps = append(*steps, "stop "+project)
		return nil
	}
	moveStart = func(_ context.Context, _, project string) error {
		*steps = append(*steps, "up "+project)
		return nil
	}
	moveSeeds = func(m catalog.Manifest, dir string) ([]catalog.MoveSeed, error) {
		return []catalog.MoveSeed{{Service: "jellyfin", Target: "/config/config/network.xml", Source: filepath.Join(dir, "network.xml"),
			Elements: map[string]string{"BaseUrl": "/apps/jellyfin"}}}, nil
	}
	moveApplySeed = func(seed catalog.MoveSeed) error {
		*steps = append(*steps, "seed "+seed.Elements["BaseUrl"])
		return nil
	}
	t.Cleanup(func() {
		moveConfigure, moveStop, moveStart, moveSeeds, moveApplySeed = prevConf, prevStop, prevStart, prevSeeds, prevApply
	})
	trusted := &[][]string{}
	prevTrust := trustAppHosts
	trustAppHosts = func(_ context.Context, m catalog.Manifest, address string, add, remove []string) error {
		*trusted = append(*trusted, append([]string{m.Name, address}, add...))
		return nil
	}
	t.Cleanup(func() { trustAppHosts = prevTrust })
	return srv, st, steps, trusted, cf
}

func moveStatus(t *testing.T, srv *Server, app string) appMoveView {
	t.Helper()
	var v appMoveView
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/"+app+"/move", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("status %s: %d %s", app, rec.Code, rec.Body.String())
	}
	return v
}

func movePlan(t *testing.T, srv *Server, app string) appMoveView {
	t.Helper()
	var v appMoveView
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/"+app+"/move/plan", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("plan %s: %d %s", app, rec.Code, rec.Body.String())
	}
	return v
}

func movedRoute(t *testing.T, st *store.Store, host string) docker.Route {
	t.Helper()
	set, _ := loadEdgeRoutes(st)
	for _, r := range set.Routes {
		if string(r.Host) == host {
			return r
		}
	}
	t.Fatalf("no route %s", host)
	return docker.Route{}
}

// ADR-044 §5: an app installed under a name of its own is offered the
// move to its path as a plan the owner approves; nothing moves silently.
func TestAppMoveNextcloud(t *testing.T) {
	srv, st, steps, trusted := moveRig(t)
	installHostApp(t, st, "nextcloud", "nextcloud", 80)
	status := moveStatus(t, srv, "nextcloud")
	if !status.Offered || status.From != "https://nextcloud.lan/" || status.Path != "/apps/nextcloud/" || len(status.To) == 0 ||
		status.To[0] != "https://homestead.lan/apps/nextcloud/" {
		t.Fatalf("status = %+v", status)
	}
	// Listing the offer changed nothing.
	if r := movedRoute(t, st, "nextcloud.lan"); r.IsPath() {
		t.Fatalf("moved on read: %+v", r)
	}
	plan := movePlan(t, srv, "nextcloud")
	settings := strings.Join(plan.AppSettings, "\n")
	if plan.Route != "nextcloud.lan" || !plan.StripPrefix || len(plan.Redirects) != 2 || plan.Digest == "" ||
		!strings.Contains(settings, "OVERWRITEWEBROOT=/apps/nextcloud") || !strings.Contains(settings, "trusted_domains") {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.Contains(strings.Join(plan.Warnings, " "), "stops answering at https://nextcloud.lan/") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
	if again := movePlan(t, srv, "nextcloud"); again.Digest != plan.Digest {
		t.Fatal("digest is not stable")
	}
	for body, code := range map[string]int{`{}`: http.StatusBadRequest, `{"digest":"stale"}`: http.StatusConflict} {
		rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/move/apply", json.RawMessage(body))
		if rec.Code != code {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if r := movedRoute(t, st, "nextcloud.lan"); r.IsPath() || len(*steps) != 0 {
		t.Fatalf("refused apply moved it: %+v %v", r, *steps)
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/move/apply", map[string]any{"digest": plan.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("operation = %+v", op)
	}
	r := movedRoute(t, st, "nextcloud.lan")
	if r.Path != "/apps/nextcloud/" || !r.StripPrefix || len(r.Redirects) != 2 || r.Published || len(r.Tunnel) != 0 {
		t.Fatalf("route = %+v", r)
	}
	joined := strings.Join(*steps, "\n")
	if !strings.Contains(joined, "env nextcloud OVERWRITEWEBROOT=/apps/nextcloud") || !strings.HasSuffix(joined, "up nextcloud") ||
		strings.Contains(joined, "stop ") {
		t.Fatalf("steps = %v", *steps)
	}
	if len(*trusted) != 1 || (*trusted)[0][1] != "https://homestead.lan/apps/nextcloud" {
		t.Fatalf("trusted = %v", *trusted)
	}
	if after := moveStatus(t, srv, "nextcloud"); after.Offered || !strings.Contains(after.Reason, "already at") {
		t.Fatalf("after = %+v", after)
	}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/nextcloud/move/plan", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second plan: %d", rec.Code)
	}
}

// Jellyfin's base URL lives in a file it rewrites: the stack stops while
// the file is written, then starts with its new settings.
func TestAppMoveJellyfinSeedsWithTheStackStopped(t *testing.T) {
	srv, st, steps, _ := moveRig(t)
	installJellyfin(t, st)
	plan := movePlan(t, srv, "jellyfin")
	if !strings.Contains(strings.Join(plan.AppSettings, " "), "<BaseUrl>/apps/jellyfin</BaseUrl>") || plan.StripPrefix {
		t.Fatalf("plan = %+v", plan)
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/apply", map[string]any{"digest": plan.Digest})
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("operation = %+v", op)
	}
	if got := strings.Join(*steps, ","); got != "stop jellyfin,seed /apps/jellyfin,up jellyfin" {
		t.Fatalf("steps = %s", got)
	}
	if r := movedRoute(t, st, "jellyfin.lan"); r.Path != "/apps/jellyfin/" || r.StripPrefix {
		t.Fatalf("route = %+v", r)
	}
}

// Refusals: a host package, an app with a public name (Authentik's
// belongs to the tunnel), and an app not installed.
func TestAppMoveRefusals(t *testing.T) {
	srv, st, _, _ := moveRig(t)
	if s := moveStatus(t, srv, "immich"); s.Offered || !strings.Contains(s.Reason, "name of its own") {
		t.Fatalf("immich = %+v", s)
	}
	if s := moveStatus(t, srv, "nextcloud"); s.Offered || !strings.Contains(s.Reason, "not installed") {
		t.Fatalf("nextcloud = %+v", s)
	}
	installHostApp(t, st, "authentik", "server", 9000)
	if s := moveStatus(t, srv, "authentik"); s.Offered || !strings.Contains(s.Reason, "Tear down the tunnel") {
		t.Fatalf("authentik = %+v", s)
	}
	installHostApp(t, st, "jellyfin", "jellyfin", 8096)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Host == "jellyfin.lan" {
			set.Routes[i].Tunnel = []docker.Hostname{"jellyfin.example.com"}
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	if s := moveStatus(t, srv, "jellyfin"); s.Offered || !strings.Contains(s.Reason, "the dashboard has none") {
		t.Fatalf("jellyfin = %+v", s)
	}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/plan", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("plan: %d", rec.Code)
	}
	if rec := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/nope/move", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
}

// Moving Authentik warns that every sign-in through it needs setting up
// again at the new address; password sign-in keeps working.
func TestAppMoveAuthentikWarnsSignIn(t *testing.T) {
	srv, st, _, _ := moveRig(t)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Host == "authentik.lan" {
			set.Routes[i].Tunnel = nil
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	installApp(t, st, "authentik", docker.Route{Host: "auth.lan", Target: docker.Target{Host: "unused", Port: 1},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
	plan := movePlan(t, srv, "authentik")
	if plan.Path != "/apps/auth/" || !strings.Contains(strings.Join(plan.SignIn, " "), "Password sign-in to the dashboard keeps working") {
		t.Fatalf("plan = %+v", plan)
	}
}

// A path app's local names are the appliance's names with its path: its
// own route name answers nothing (ADR-044).
func TestAppNamesOfPathApp(t *testing.T) {
	set := docker.RouteSet{Routes: []docker.Route{
		{Host: "homestead.lan", Aliases: []docker.Hostname{"homestead.local"}, Dashboard: true},
		{Host: "nextcloud.lan", Path: "/apps/nextcloud/"},
	}}
	got := appNamesOf(set)
	if len(got) != 2 || got[1].Path != "/apps/nextcloud/" ||
		strings.Join(got[1].Names, ",") != "homestead.lan/apps/nextcloud/,homestead.local/apps/nextcloud/" {
		t.Fatalf("names = %+v", got)
	}
}

// ADR-045 §5: a craft app on a name of its own is offered the move too,
// and its route keeps its craft app name and strips its path.
func TestAppMoveCraftApp(t *testing.T) {
	srv, st, steps, _ := moveRig(t)
	installApp(t, st, "cadcraft", docker.Route{Host: "cadcraft.lan", Target: docker.Target{Host: "cadcraft", Port: 8080},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, CraftApp: "cadcraft"})
	status := moveStatus(t, srv, "cadcraft")
	if !status.Offered || status.Path != "/apps/cadcraft/" {
		t.Fatalf("status = %+v", status)
	}
	plan := movePlan(t, srv, "cadcraft")
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/cadcraft/move/apply", map[string]any{"digest": plan.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("operation = %+v %v", op, *steps)
	}
	r := movedRoute(t, st, "cadcraft.lan")
	if r.Path != "/apps/cadcraft/" || !r.StripPrefix || r.CraftApp != "cadcraft" {
		t.Fatalf("route = %+v", r)
	}
}

// S395: moving Authentik to the dashboard's path keeps it published on
// the dashboard's tunnel when a fresh tunnel plan would publish it, so
// public sign-in and recovery links keep answering.
func authentikMoveRig(t *testing.T, tunneled, linked bool) (*Server, *store.Store) {
	t.Helper()
	srv, st, _, _ := moveRig(t)
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		switch {
		case set.Routes[i].Host == "authentik.lan":
			set.Routes[i].Tunnel = nil
		case set.Routes[i].Dashboard && tunneled:
			set.Routes[i].Tunnel = []docker.Hostname{"homestead.example.com"}
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	installApp(t, st, "authentik", docker.Route{Host: "auth.lan", Target: docker.Target{Host: "server", Port: 9000},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
	// Only the rig's own Authentik route stays; installApp added the marker.
	set, _ = loadEdgeRoutes(st)
	kept := set.Routes[:0]
	for _, r := range set.Routes {
		if r.Host != "auth.lan" {
			kept = append(kept, r)
		}
	}
	set.Routes = kept
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	srv.signInOffered = func() bool { return linked }
	prev := reconcileEdgeSet
	reconcileEdgeSet = func(_ context.Context, st *store.Store, set docker.RouteSet) error { return saveEdgeRoutes(st, set) }
	t.Cleanup(func() { reconcileEdgeSet = prev })
	return srv, st
}

func applyAuthentikMove(t *testing.T, srv *Server, st *store.Store) appMoveView {
	t.Helper()
	plan := movePlan(t, srv, "authentik")
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/authentik/move/apply", map[string]any{"digest": plan.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("operation = %+v", op)
	}
	return plan
}

func TestAppMoveAuthentikKeepsPublicSignIn(t *testing.T) {
	srv, st := authentikMoveRig(t, true, true)
	plan := applyAuthentikMove(t, srv, st)
	if !strings.Contains(strings.Join(plan.SignIn, " "), "stays published on the tunnel") {
		t.Fatalf("plan text = %v", plan.SignIn)
	}
	set, _ := loadEdgeRoutes(st)
	var ak docker.Route
	for _, r := range set.Routes {
		if r.Host == "authentik.lan" {
			ak = r
		}
	}
	if ak.Path != "/apps/auth/" || !ak.Published || len(ak.Tunnel) != 0 {
		t.Fatalf("route = %+v", ak)
	}
	dir := t.TempDir()
	if _, err := docker.ReconcileRoutes(dir, set, docker.RouteSet{}); err != nil {
		t.Fatal(err)
	}
	var dashFile string
	for _, r := range set.Routes {
		if r.Dashboard {
			dashFile = string(docker.RouteFilename(r.Host))
		}
	}
	body, err := os.ReadFile(filepath.Join(dir, "routes", dashFile))
	if err != nil {
		t.Fatal(err)
	}
	tunnelBlock := string(body)[strings.Index(string(body), "# Tunnel entry"):]
	if !strings.Contains(tunnelBlock, "handle /apps/auth/*") {
		t.Fatalf("tunnel block lacks the Authentik handle:\n%s", tunnelBlock)
	}
}

func TestAppMoveAuthentikStaysUnpublishedWithoutTunnelOrSignIn(t *testing.T) {
	for name, tc := range map[string]struct {
		tunneled, linked bool
		note             string
	}{
		"no tunnel":        {false, true, ""},
		"sign-in unlinked": {true, false, "sign-in is not linked"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, st := authentikMoveRig(t, tc.tunneled, tc.linked)
			plan := applyAuthentikMove(t, srv, st)
			text := strings.Join(plan.SignIn, " ")
			if strings.Contains(text, "stays published") || !strings.Contains(text, tc.note) {
				t.Fatalf("plan text = %v", plan.SignIn)
			}
			if r := movedRoute(t, st, "authentik.lan"); r.Path != "/apps/auth/" || r.Published {
				t.Fatalf("route = %+v", r)
			}
		})
	}
}
