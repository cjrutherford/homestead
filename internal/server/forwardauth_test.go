package server

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/store"
)

// navidromeRig is the sign-in rig with Navidrome installed and routed, the
// edge and the app's accounts stubbed.
func navidromeRig(t *testing.T) *ssoApp {
	t.Helper()
	a := appSignInRig(t)
	a.linkOwner(t)
	if _, err := a.st.DB().Exec(`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES ('navidrome', 'compose.yaml', 'x', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	set, _ := loadEdgeRoutes(a.st)
	set.Routes = append(set.Routes, docker.Route{Host: "navidrome.lan", Target: docker.Target{Host: "navidrome", Port: 4533}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
	if err := saveEdgeRoutes(a.st, set); err != nil {
		t.Fatal(err)
	}
	oldCIDR, oldRead := edgeSourceAddr, appsso.ReadAccounts
	edgeSourceAddr = func(context.Context) (string, error) { return "172.30.0.5/32", nil }
	appsso.ReadAccounts = func(context.Context, catalog.Manifest) appsso.AppAccounts {
		return appsso.AppAccounts{Names: []string{"admin", "kid"}, Admins: 1}
	}
	oldBlock := reconcileGateBlock
	reconcileGateBlock = func(context.Context, *store.Store) error { return nil }
	t.Cleanup(func() { edgeSourceAddr, appsso.ReadAccounts, reconcileGateBlock = oldCIDR, oldRead, oldBlock })
	return a
}

func TestNavidromeSignInRoutesPlanTheGate(t *testing.T) {
	a := navidromeRig(t)
	st := decodeAs[appsso.Status](t, a.req("GET", "/api/v1/catalog/navidrome/sign-in", "", a.owner.token).Body.Bytes())
	if !st.Offered || st.Enabled || st.Adapter != appsso.ForwardAdapter {
		t.Fatalf("status: %+v", st)
	}
	rec := a.req("POST", "/api/v1/catalog/navidrome/sign-in/plan", `{"enabled":true}`, a.owner.token)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	plan := decodeAs[appsso.Plan](t, rec.Body.Bytes())
	if plan.Gate == nil || plan.Gate.Canonical != "navidrome.lan" || plan.Gate.Env["ND_EXTAUTH_TRUSTEDSOURCES"] != "172.30.0.5/32" ||
		strings.Join(plan.Gate.ExistingUsers, ",") != "admin,kid" || plan.Digest == "" {
		t.Fatalf("plan: %+v", plan.Gate)
	}
	if !strings.HasPrefix(plan.Gate.AuthentikHost, a.w.srv.URL) {
		t.Fatalf("authentik host: %s", plan.Gate.AuthentikHost)
	}
	// A stale digest is refused before any operation.
	if rec := a.req("POST", "/api/v1/catalog/navidrome/sign-in/apply", `{"enabled":true,"digest":"stale","token":"t"}`, a.owner.token); rec.Code != http.StatusConflict {
		t.Fatalf("stale: %d %s", rec.Code, rec.Body)
	}
	// An active person named like a Navidrome account refuses the plan.
	if _, err := a.st.DB().Exec(`INSERT INTO people (id, display_name, username, authentik_pk, state, created_by, created_at) VALUES (9, 'Kid', 'kid', 9, 'active', 'owner', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	rec = a.req("POST", "/api/v1/catalog/navidrome/sign-in/plan", `{"enabled":true}`, a.owner.token)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "account named kid") {
		t.Fatalf("collision: %d %s", rec.Code, rec.Body)
	}
}

func TestPreserveGatesAndHeal(t *testing.T) {
	a := appSignInRig(t)
	gate := &docker.Gate{Canonical: "music.example.com", CopyHeaders: []string{"X-Authentik-Username"}}
	route := docker.Route{Host: "navidrome.lan", Target: docker.Target{Host: "navidrome", Port: 4533}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
		Tunnel: []docker.Hostname{"music.example.com"}, Gate: gate}
	if err := saveEdgeRoutes(a.st, docker.RouteSet{Routes: []docker.Route{route}}); err != nil {
		t.Fatal(err)
	}
	// A posted table cannot drop the gate or add one of its own.
	posted := route
	posted.Gate, posted.Tunnel = nil, nil
	other := docker.Route{Host: "x.lan", Target: docker.Target{Host: "x", Port: 80}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
		Gate: &docker.Gate{Canonical: "x.lan"}}
	out := preserveGates(a.st, []docker.Route{posted, other})
	if out[0].Gate == nil || out[0].Gate.Canonical != "navidrome.lan" {
		// The tunnel name is not on the posted route, so the gate heals to its host.
		t.Fatalf("gate: %+v", out[0].Gate)
	}
	if out[1].Gate != nil {
		t.Fatal("a client-sent gate must be dropped")
	}
}

func TestSetGateReconcilesTheAppRoutesOnly(t *testing.T) {
	a := navidromeRig(t)
	var got docker.RouteSet
	oldSet, oldPing := reconcileEdgeSet, outpostPing
	reconcileEdgeSet = func(_ context.Context, st *store.Store, set docker.RouteSet) error {
		got = set
		return saveEdgeRoutes(st, set)
	}
	pings := 0
	outpostPing = func(context.Context) error { pings++; return nil }
	t.Cleanup(func() { reconcileEdgeSet, outpostPing = oldSet, oldPing })
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("navidrome")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.srv.appSignIn.setGate(context.Background(), m, &docker.Gate{Canonical: "navidrome.lan", CopyHeaders: []string{"X-Authentik-Username"}, Bypass: []string{`^/rest/.*`}}); err != nil {
		t.Fatal(err)
	}
	gated := 0
	for _, r := range got.Routes {
		if r.Gate != nil {
			gated++
			if r.Host != "navidrome.lan" {
				t.Fatalf("gated %s", r.Host)
			}
		}
	}
	if gated != 1 || pings != 1 {
		t.Fatalf("gated %d pings %d", gated, pings)
	}
	// A failing outpost fails the step (the gate stays on, fail closed).
	outpostPing = func(context.Context) error { return errors.New("down") }
	if err := a.srv.appSignIn.setGate(context.Background(), m, &docker.Gate{Canonical: "navidrome.lan"}); err == nil {
		t.Fatal("an outpost that does not answer must fail the gate step")
	}
	// Off clears it without probing.
	pings = 0
	outpostPing = func(context.Context) error { pings++; return nil }
	if err := a.srv.appSignIn.setGate(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}
	for _, r := range got.Routes {
		if r.Gate != nil {
			t.Fatal("gate left")
		}
	}
	if pings != 0 {
		t.Fatal("turning off must not need the outpost")
	}
}

func TestDoctorChecksTheOutpostOnlyWhileAGateIsOn(t *testing.T) {
	a := appSignInRig(t)
	c := DoctorLiveConfig{Store: a.st, CatalogDir: filepath.Join("..", "..", "catalog")}
	old := outpostPing
	t.Cleanup(func() { outpostPing = old })
	outpostPing = func(context.Context) error { t.Fatal("no gate, no probe"); return nil }
	if got := gatedAppChecks(context.Background(), c); len(got) != 0 {
		t.Fatalf("no gate: %+v", got)
	}
	if err := saveEdgeRoutes(a.st, docker.RouteSet{Routes: []docker.Route{{Host: "navidrome.lan", Target: docker.Target{Host: "navidrome", Port: 4533},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Gate: &docker.Gate{Canonical: "navidrome.lan"}}}}); err != nil {
		t.Fatal(err)
	}
	outpostPing = func(context.Context) error { return nil }
	if got := gatedAppChecks(context.Background(), c); len(got) != 1 || got[0].Status != doctor.StatusOK {
		t.Fatalf("answers: %+v", got)
	}
	outpostPing = func(context.Context) error { return errors.New("connection refused") }
	got := gatedAppChecks(context.Background(), c)
	if len(got) != 1 || got[0].Status != doctor.StatusFail || got[0].Fix == nil || !strings.Contains(got[0].Detail, "navidrome.lan") {
		t.Fatalf("down: %+v", got)
	}
}

func TestParseEdgeAddr(t *testing.T) {
	nets := `{"bridge":{"IPAddress":"172.17.0.2"},"homestead-edge":{"IPAddress":"192.168.48.3","Gateway":"192.168.48.1"}}`
	got, err := parseEdgeAddr("true " + nets + "\n")
	if err != nil || got != "192.168.48.3/32" {
		t.Fatalf("got %q %v", got, err)
	}
	for name, out := range map[string]string{
		"stopped":    "false " + nets,
		"not on net": `true {"bridge":{"IPAddress":"172.17.0.2"}}`,
		"empty ip":   `true {"homestead-edge":{"IPAddress":""}}`,
		"ipv6 only":  `true {"homestead-edge":{"IPAddress":"fd00::3"}}`,
		"bad json":   "true nope",
		"nothing":    "",
	} {
		if got, err := parseEdgeAddr(out); err == nil {
			t.Errorf("%s: want refusal, got %q", name, got)
		}
	}
}

func TestEdgeAddrFailureRefusesThePlan(t *testing.T) {
	a := navidromeRig(t)
	edgeSourceAddr = func(context.Context) (string, error) { return "", errors.New("not running") }
	rec := a.req("POST", "/api/v1/catalog/navidrome/sign-in/plan", `{"enabled":true}`, a.owner.token)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "edge network") {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
}

func TestSetGateFailsWithoutTheHostBlockOnlyWhenRequired(t *testing.T) {
	a := navidromeRig(t)
	oldSet, oldPing := reconcileEdgeSet, outpostPing
	reconcileEdgeSet = func(_ context.Context, st *store.Store, set docker.RouteSet) error { return saveEdgeRoutes(st, set) }
	outpostPing = func(context.Context) error { return nil }
	t.Cleanup(func() { reconcileEdgeSet, outpostPing = oldSet, oldPing })
	reconcileGateBlock = func(context.Context, *store.Store) error { return errors.New("nft denied") }
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := entries.Get("navidrome")
	gate := &docker.Gate{Canonical: "navidrome.lan", CopyHeaders: []string{"X-Authentik-Username"}}
	if err := a.srv.appSignIn.setGate(context.Background(), m, gate); err != nil {
		t.Fatalf("an app that does not require the block still gates: %v", err)
	}
	m.SSO.ForwardAuth.RequiresHostBlock = true
	if err := a.srv.appSignIn.setGate(context.Background(), m, gate); err == nil || !strings.Contains(err.Error(), "host block") {
		t.Fatalf("a requiring app must fail: %v", err)
	}
}

// The install review plans a gated app before it exists (S450): the plan
// it approves must be the one the apply plans again once the first run has
// made the administrator, or the setup stops for a new approval.
func TestInstallPreviewOfAGatedAppMatchesTheLivePlan(t *testing.T) {
	a := navidromeRig(t)
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("navidrome")
	if err != nil {
		t.Fatal(err)
	}
	appsso.ReadAccounts = func(context.Context, catalog.Manifest) appsso.AppAccounts {
		return appsso.AppAccounts{Names: []string{"homestead-admin"}, Admins: 1}
	}
	route := docker.Route{Host: "navidrome.lan", Target: docker.Target{Host: "navidrome", Port: 4533}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	pv := a.srv.appSignIn.preview(m, catalog.InstallPlan{Routes: []docker.Route{route}}, "")
	if pv == nil || !pv.Offered || pv.Plan == nil {
		t.Fatalf("preview = %+v", pv)
	}
	rec := a.req("POST", "/api/v1/catalog/navidrome/sign-in/plan", `{"enabled":true}`, a.owner.token)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	live := decodeAs[appsso.Plan](t, rec.Body.Bytes())
	if pv.Plan.Digest != live.Digest {
		t.Fatalf("anticipated digest %s differs from the live plan %s", pv.Plan.Digest, live.Digest)
	}
	// An app with no first-run administrator cannot be planned ahead.
	m.FirstRun = nil
	if pv := a.srv.appSignIn.preview(m, catalog.InstallPlan{Routes: []docker.Route{route}}, ""); pv == nil || pv.Offered {
		t.Fatalf("preview without an administrator = %+v", pv)
	}
}
