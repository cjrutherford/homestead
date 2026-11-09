package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/sso"
	"homestead/internal/store"
)

const tunnelPublicOrigin = "https://homestead.example.com"

// ADR-046 §1, §4, §5: what publishing the dashboard does to sign-in.
func TestTunnelSignInChange(t *testing.T) {
	at := "https://homestead.example.com/apps/auth/"
	local := &sso.Spec{Origins: []string{"https://homestead.lan"}}
	covered := &sso.Spec{Origins: []string{"https://homestead.lan", tunnelPublicOrigin},
		PublicOrigins: []string{tunnelPublicOrigin}, PublicAuthentikURL: "https://homestead.example.com/apps/auth"}
	note, update := tunnelSignInChange(tunnelFacts{SignIn: local, TokenKept: true}, "Homestead.Example.com", at)
	want := tunnelSignIn{Origin: tunnelPublicOrigin, RedirectURI: tunnelPublicOrigin + sso.CallbackPath,
		AuthentikURL: "https://homestead.example.com/apps/auth"}
	if update == nil || *update != want || !strings.HasPrefix(note, "Approving also updates Sign-in") {
		t.Fatalf("kept token: %q %+v", note, update)
	}
	if note, update = tunnelSignInChange(tunnelFacts{SignIn: local}, "homestead.example.com", at); update != nil ||
		!strings.Contains(note, "did not keep the Authentik API token") || !strings.Contains(note, "re-plan Sign-in") {
		t.Fatalf("no token: %q %+v", note, update)
	}
	if note, update = tunnelSignInChange(tunnelFacts{SignIn: covered, TokenKept: true}, "homestead.example.com", at); update != nil ||
		!strings.HasPrefix(note, "Sign-in already covers") {
		t.Fatalf("covered: %q %+v", note, update)
	}
}

type tunnelSignInView struct {
	SignIn       string        `json:"sign_in"`
	SignInUpdate *tunnelSignIn `json:"sign_in_update"`
	Digest       string        `json:"digest"`
}

// tunnelSignInRig is TestTunnelPlanPathAuthentik's appliance with sign-in
// linked and configured; kept says whether the API token was kept.
func tunnelSignInRig(t *testing.T, kept *bool) (*Server, *store.Store, *fakeCloudflare, func() tunnelSignInView) {
	t.Helper()
	srv, st, secrets := tunnelTestServer(t)
	cf := newFakeCloudflare(t)
	fakeConnector(t)
	fakeEdge(t)
	if err := secrets.Set("dns_cloudflare_api_token", "sealed-token"); err != nil {
		t.Fatal(err)
	}
	seedPathAuthentik(t, srv, st, true)
	srv.signInStatus = func() (sso.Status, error) {
		return sso.Status{Configured: true, TokenKept: *kept, Spec: &sso.Spec{Origins: []string{"https://homestead.lan"}}}, nil
	}
	plan := func() tunnelSignInView {
		rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", map[string]any{"name": "homestead"})
		var v tunnelSignInView
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
			t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
		}
		return v
	}
	return srv, st, cf, plan
}

// ADR-046 §1-2: approving the tunnel plan also updates sign-in, after the
// edge carries the public name; the change is bound into the digest.
func TestTunnelSetupUpdatesSignIn(t *testing.T) {
	kept := false
	srv, st, cf, plan := tunnelSignInRig(t, &kept)
	without := plan()
	if without.SignInUpdate != nil || !strings.Contains(without.SignIn, "re-plan Sign-in") {
		t.Fatalf("no token plan = %+v", without)
	}
	kept = true
	p := plan()
	if p.SignInUpdate == nil || p.SignInUpdate.Origin != tunnelPublicOrigin ||
		p.SignInUpdate.AuthentikURL != "https://homestead.example.com/apps/auth" || p.Digest == without.Digest {
		t.Fatalf("kept token plan = %+v (digest without %s)", p, without.Digest)
	}
	var got []string
	srv.updateSignIn = func(_ context.Context, actor, digest string, want tunnelSignIn) error {
		set, _ := loadEdgeRoutes(st)
		dash, _ := set.DashboardRoute()
		got = append(got, fmt.Sprintf("%s %s %s %v", digest, want.Origin, want.RedirectURI, dash.Tunnel))
		return nil
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", map[string]any{"name": "homestead", "digest": p.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("setup op = %s: %s", op.Status, op.Error)
	}
	want := p.Digest + " " + tunnelPublicOrigin + " " + tunnelPublicOrigin + sso.CallbackPath + " [homestead.example.com]"
	if len(got) != 1 || got[0] != want || len(cf.records) != 1 {
		t.Fatalf("sign-in calls = %q, records = %v", got, cf.records)
	}
}

// ADR-046 §3: a failed sign-in update fails the operation but leaves the
// working tunnel up.
func TestTunnelSetupSignInFailureKeepsTunnel(t *testing.T) {
	kept := true
	srv, st, cf, plan := tunnelSignInRig(t, &kept)
	p := plan()
	srv.updateSignIn = func(context.Context, string, string, tunnelSignIn) error {
		return errors.New("Authentik did not answer")
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", map[string]any{"name": "homestead", "digest": p.Digest})
	op := waitOperation(t, st, rec.Body.Bytes())
	if op.Status != operation.StatusFailed || !strings.Contains(op.Error, "the tunnel is up, but sign-in was not updated") {
		t.Fatalf("setup op = %s: %s", op.Status, op.Error)
	}
	set, _ := loadEdgeRoutes(st)
	dash, _ := set.DashboardRoute()
	if len(cf.records) != 1 || fmt.Sprint(dash.Tunnel) != "[homestead.example.com]" {
		t.Fatalf("records = %v, dashboard = %+v", cf.records, dash)
	}
}

// ADR-046 §2 against a real sign-in manager and fake Authentik: the
// public origin, its redirect URI and Authentik's public address are
// planned, approved and applied with the kept token, and audited.
func TestUpdateSignInForTunnel(t *testing.T) {
	a := newSSOApp(t)
	a.configureKeeping(t, true)
	set, err := loadEdgeRoutes(a.st)
	if err != nil {
		t.Fatal(err)
	}
	dash, err := BuildDashboardRoute(dashLAN, "")
	if err != nil {
		t.Fatal(err)
	}
	dash.Tunnel = []docker.Hostname{"homestead.example.com"}
	set.Routes = append(set.Routes, dash, docker.Route{Host: "authentik.lan", Target: docker.Target{Host: "server", Port: 9000},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Path: "/apps/auth/", Published: true})
	if err := saveEdgeRoutes(a.st, set); err != nil {
		t.Fatal(err)
	}
	want := tunnelSignIn{Origin: tunnelPublicOrigin, RedirectURI: tunnelPublicOrigin + sso.CallbackPath,
		AuthentikURL: "https://homestead.example.com/apps/auth"}
	ctx := context.Background()
	wrong := want
	wrong.AuthentikURL = "https://auth.example.com"
	if err := a.srv.updateSignIn(ctx, "owner", "abcdef0123456789", wrong); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatch err = %v", err)
	}
	if err := a.srv.updateSignIn(ctx, "owner", "abcdef0123456789", want); err != nil {
		t.Fatal(err)
	}
	st, err := a.srv.signInStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(st.Spec.PublicOrigins), tunnelPublicOrigin) || st.Spec.PublicAuthentikURL != want.AuthentikURL ||
		!strings.Contains(fmt.Sprint(st.Spec.Origins), "https://homestead.example.test") || !st.TokenKept {
		t.Fatalf("spec = %+v kept=%t", st.Spec, st.TokenKept)
	}
	a.w.mu.Lock()
	uris := fmt.Sprint(a.w.provider["redirect_uris"])
	a.w.mu.Unlock()
	if !strings.Contains(uris, want.RedirectURI) {
		t.Fatalf("Authentik redirect URIs = %s", uris)
	}
	all, err := audit.New(a.st).List(1000)
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	for _, e := range all {
		if e.Action == "tunnel.sign-in" {
			outcomes = append(outcomes, e.Outcome)
		}
	}
	if len(outcomes) != 2 || !strings.Contains(fmt.Sprint(outcomes), audit.OutcomeOK) || !strings.Contains(fmt.Sprint(outcomes), audit.OutcomeError) {
		t.Fatalf("tunnel.sign-in outcomes = %v", outcomes)
	}
}
