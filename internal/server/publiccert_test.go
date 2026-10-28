package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/audit"
	"homestead/internal/cert"
	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/sso"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

type tunnelCertView struct {
	Certificate *tunnelCertificate `json:"certificate"`
	IssuerMoves *cert.MovePlan     `json:"issuer_moves"`
	Digest      string             `json:"digest"`
}

// mountTestIssuing mounts certificate issuance on srv with the Cloudflare
// provider and a CA that never answers; every test stubs the issuance.
func mountTestIssuing(t *testing.T, srv *Server, st *store.Store, secrets *secret.Store, provider dns.Provider, zone bool) *cert.Issuing {
	t.Helper()
	registry := dns.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	issuing := &cert.Issuing{Store: st, Wizards: wizard.New(st), Secrets: secrets, Providers: registry,
		StackDir:  filepath.Join(t.TempDir(), "caddy"),
		NewClient: func(string) (*cert.Client, error) { return cert.NewClient("http://127.0.0.1:1/directory") },
		Routes:    func() ([]docker.Route, error) { return servedCandidateRoutes(st) },
		Reload:    func(context.Context) error { return nil },
	}
	if zone {
		if _, err := issuing.Wizards.Put("domain", "zone", map[string]any{"provider": provider.Name(), "zone": "example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	if mounted := srv.certIssuing; mounted != nil {
		// The full app mounted issuance already: keep its wiring (the
		// move hooks), swap in the test collaborators.
		mounted.Secrets, mounted.Providers, mounted.StackDir = secrets, registry, issuing.StackDir
		mounted.NewClient, mounted.Routes, mounted.Reload = issuing.NewClient, issuing.Routes, issuing.Reload
		return mounted
	}
	srv.MountTLSIssuance(st, issuing)
	return issuing
}

// stubCertIssuance replaces the in-place issuance; err is what it returns.
func stubCertIssuance(t *testing.T, calls *[]string, err error) {
	t.Helper()
	prev := runCertIssuance
	runCertIssuance = func(_ context.Context, _ *cert.Issuing, name string) error {
		*calls = append(*calls, "certificate "+name)
		return err
	}
	t.Cleanup(func() { runCertIssuance = prev })
}

func tunnelCertRig(t *testing.T) (*Server, *store.Store, *fakeCloudflare, *cert.Issuing, func() tunnelCertView) {
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
		return sso.Status{Configured: true, TokenKept: true, Spec: &sso.Spec{Origins: []string{"https://homestead.lan"}}}, nil
	}
	issuing := mountTestIssuing(t, srv, st, secrets, issuanceCloudflareDNS{}, true)
	plan := func() tunnelCertView {
		rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", map[string]any{"name": "homestead"})
		var v tunnelCertView
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
			t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
		}
		return v
	}
	return srv, st, cf, issuing, plan
}

// ADR-048 §5: the tunnel plan plans the public dashboard name's Let's
// Encrypt certificate, bound into the digest; approving needs the terms
// and production confirmations, records the certificate's approval
// under the owner through the tunnel plan, and issues it after the
// connector and before sign-in.
func TestTunnelPlanIssuesPublicCertificate(t *testing.T) {
	srv, st, _, issuing, plan := tunnelCertRig(t)
	p := plan()
	c := p.Certificate
	if c == nil || !c.Issue || c.Certificate != publicCertName || !c.AddsToStrategy || c.Directory != cert.LetsEncryptDirectory ||
		c.DirectoryClass != cert.DirectoryProduction || c.Challenge != "dns-01" || c.Provider != "cloudflare" ||
		c.Renewal != cert.RenewalAuto || c.TermsURL != cert.LetsEncryptTermsURL || c.Digest == "" || c.Zone != "example.com" {
		t.Fatalf("certificate = %+v", c)
	}
	// The certificate is in the digest: dropping issuance changes it.
	srv.certIssuing = nil
	if without := plan(); without.Certificate != nil || without.Digest == p.Digest {
		t.Fatalf("without issuance = %+v", without)
	}
	srv.certIssuing = issuing
	for body, want := range map[string]string{
		`{}`:                   "agree_terms is required",
		`{"agree_terms":true}`: "confirm_production is required",
	} {
		req := map[string]any{"name": "homestead", "digest": p.Digest}
		_ = json.Unmarshal([]byte(body), &req)
		if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", req); rec.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	var calls []string
	stubCertIssuance(t, &calls, nil)
	srv.updateSignIn = func(context.Context, string, string, tunnelSignIn) error {
		calls = append(calls, "sign-in")
		return nil
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup",
		map[string]any{"name": "homestead", "digest": p.Digest, "agree_terms": true, "confirm_production": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("setup op = %s: %s", op.Status, op.Error)
	}
	if strings.Join(calls, ";") != "certificate public-dashboard;sign-in" {
		t.Fatalf("calls = %v", calls)
	}
	set, err := cert.LoadStrategy(issuing.Wizards)
	if err != nil || len(set.Certificates) != 1 || set.Certificates[0].DNSNames[0] != "homestead.example.com" {
		t.Fatalf("strategy = %+v, %v", set.Certificates, err)
	}
	approvals, _ := issuing.Approvals()
	if len(approvals) != 1 || approvals[0].Certificate != publicCertName || !approvals[0].AgreedTerms || !approvals[0].ConfirmedProduction || approvals[0].RenewalMode != cert.RenewalAuto {
		t.Fatalf("approvals = %+v", approvals)
	}
	entries, _ := audit.New(st).List(100)
	found := false
	for _, e := range entries {
		if e.Action == "cert.issuance.approve" && strings.Contains(e.Detail, "approved with tunnel plan "+p.Digest[:12]) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no cert approval audited through the tunnel plan: %+v", entries)
	}
}

// ADR-048 §5: a failed certificate leaves the tunnel up, fails the
// operation saying how to retry from TLS, and skips sign-in.
func TestTunnelCertificateFailureKeepsTunnel(t *testing.T) {
	srv, st, cf, _, plan := tunnelCertRig(t)
	p := plan()
	var calls []string
	stubCertIssuance(t, &calls, errors.New("the CA refused"))
	srv.updateSignIn = func(context.Context, string, string, tunnelSignIn) error {
		calls = append(calls, "sign-in")
		return nil
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup",
		map[string]any{"name": "homestead", "digest": p.Digest, "agree_terms": true, "confirm_production": true})
	op := waitOperation(t, st, rec.Body.Bytes())
	if op.Status != operation.StatusFailed || !strings.Contains(op.Error, "the tunnel is up, but the certificate for homestead.example.com was not issued") ||
		!strings.Contains(op.Error, "the CA refused") || !strings.Contains(op.Error, "Retry it from TLS") {
		t.Fatalf("setup op = %s: %s", op.Status, op.Error)
	}
	if strings.Join(calls, ";") != "certificate public-dashboard" {
		t.Fatalf("calls = %v", calls)
	}
	set, _ := loadEdgeRoutes(st)
	dash, _ := set.DashboardRoute()
	if len(cf.records) != 1 || fmt.Sprint(dash.Tunnel) != "[homestead.example.com]" {
		t.Fatalf("records = %v, dashboard = %+v", cf.records, dash)
	}
}

// ADR-048 §5: a public certificate already placed for the name is
// reported and adds no certificate step, and no terms are asked.
func TestTunnelPlanCertificateAlreadyPlaced(t *testing.T) {
	srv, st, _, issuing, plan := tunnelCertRig(t)
	placeTestCert(t, issuing.StackDir, "imported", "homestead.example.com")
	p := plan()
	if p.Certificate == nil || p.Certificate.Issue || p.Certificate.Placed != "imported" ||
		!strings.Contains(p.Certificate.Note, "already placed") {
		t.Fatalf("certificate = %+v", p.Certificate)
	}
	var calls []string
	stubCertIssuance(t, &calls, nil)
	srv.updateSignIn = func(context.Context, string, string, tunnelSignIn) error { return nil }
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", map[string]any{"name": "homestead", "digest": p.Digest})
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded || len(calls) != 0 {
		t.Fatalf("setup op = %s %s, calls %v", op.Status, op.Error, calls)
	}
	// A local leaf never counts as a public certificate.
	if _, ok := placedPublicCert(issuing.StackDir, "other.example.com"); ok {
		t.Fatal("placed certificate for an uncovered name")
	}
}

// A strategy certificate covering the name that tunnel setup cannot
// issue (not DNS-01) blocks the step instead of being replaced.
func TestTunnelPlanCertificateBlockedByStrategy(t *testing.T) {
	_, _, _, issuing, plan := tunnelCertRig(t)
	if err := cert.AddToStrategy(issuing.Wizards, cert.Certificate{Name: "dash", DNSNames: []string{"homestead.example.com"},
		State: cert.StatePending, Issuer: cert.Issuer{Name: "own", Type: cert.IssuerManual}}); err != nil {
		t.Fatal(err)
	}
	p := plan()
	if p.Certificate == nil || p.Certificate.Issue || p.Certificate.Certificate != "dash" || len(p.Certificate.Blockers) != 1 {
		t.Fatalf("certificate = %+v", p.Certificate)
	}
}

func placeTestCert(t *testing.T, stackDir, name, host string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := docker.PlaceCert(stackDir, docker.PlacedCert{Name: name, Names: []string{host}}, chain, keyPEM); err != nil {
		t.Fatal(err)
	}
}

// issuerMoveRig is the app sign-in rig with the dashboard and Authentik's path
// published, Immich published on its own name and its sign-in applied
// at the local issuer: the certificate moves it.
func issuerMoveRig(t *testing.T, keep bool) (*ssoApp, docker.RouteSet) {
	t.Helper()
	a := newSSOApp(t)
	a.configureKeeping(t, keep)
	a.srv.appSignIn.cfg.CatalogDir = filepath.Join("..", "..", "catalog")
	dir := t.TempDir()
	old := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(dir, name) }
	t.Cleanup(func() { stackDirFor = old })
	if _, err := a.st.DB().Exec(`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES ('immich', 'compose.yaml', 'x', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	set := docker.RouteSet{Routes: []docker.Route{
		{Host: "homestead.lan", Dashboard: true, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
			Tunnel: []docker.Hostname{"homestead.example.com"}},
		{Host: "authentik.lan", Target: docker.Target{Host: "server", Port: 9000}, Exposure: docker.ExposureLocal,
			TLS: docker.TLSInternal, Path: "/apps/auth/", Published: true},
		{Host: "immich.lan", Target: docker.Target{Host: "server", Port: 2283}, Exposure: docker.ExposureLocal,
			TLS: docker.TLSInternal, Tunnel: []docker.Hostname{"photos.example.com"}},
	}}
	if err := saveEdgeRoutes(a.st, set); err != nil {
		t.Fatal(err)
	}
	local := a.w.srv.URL + "/application/o/homestead-immich/"
	if err := (appsso.Records{DB: a.st.DB()}).Save(appsso.Record{Package: "immich", Adapter: "immich",
		ClientID: "homestead-immich", Issuer: local, RedirectURIs: []string{"https://photos.example.com/auth/login"}}); err != nil {
		t.Fatal(err)
	}
	return a, set
}

// ADR-048 §6: an applied app whose plan, with the certificate served,
// names Authentik's public address is listed with its issuer change and
// host entry; one not covered by the certificate moves nothing.
func TestIssuerMovesListAppliedApps(t *testing.T) {
	a, set := issuerMoveRig(t, true)
	moves, err := a.srv.issuerMoves(set, []string{"homestead.example.com"})
	if err != nil || moves == nil || len(moves.Apps) != 1 || !moves.TokenKept {
		t.Fatalf("moves = %+v, %v", moves, err)
	}
	m := moves.Apps[0]
	if m.App != "immich" || m.Title == "" || !strings.HasPrefix(m.To, "https://homestead.example.com/apps/auth/") ||
		m.HostEntry != "homestead.example.com:host-gateway" || !strings.HasSuffix(m.ReplacedHostEntry, ":host-gateway") ||
		m.Digest == "" || !strings.Contains(moves.Note, "API token you kept") {
		t.Fatalf("move = %+v note %q", m, moves.Note)
	}
	if moves, _ := a.srv.issuerMoves(set, []string{"other.example.com"}); moves != nil {
		t.Fatalf("uncovered name moved %+v", moves)
	}
}

// ADR-048 §6: without the kept token the apps are listed with the
// re-plan to approve, and nothing is re-applied.
func TestIssuerMovesWithoutToken(t *testing.T) {
	a, set := issuerMoveRig(t, false)
	moves, err := a.srv.issuerMoves(set, []string{"homestead.example.com"})
	if err != nil || moves == nil || moves.TokenKept || moves.Reapplies() || !strings.Contains(moves.Note, "nothing changes in Authentik") {
		t.Fatalf("moves = %+v, %v", moves, err)
	}
}

// ADR-048 §6: the move step re-applies only the listed apps; one failing
// does not stop the others, and the failures are named and audited.
func TestMoveIssuersContinuesPastFailures(t *testing.T) {
	a, _ := issuerMoveRig(t, true)
	var tried []string
	prev := reapplyAppSignIn
	reapplyAppSignIn = func(_ context.Context, _ *appSignIn, move cert.IssuerMove, actor string) error {
		tried = append(tried, actor+" "+move.App+" "+move.Digest)
		if move.App == "nextcloud" {
			return errors.New("Authentik did not answer")
		}
		return nil
	}
	t.Cleanup(func() { reapplyAppSignIn = prev })
	moves := cert.MovePlan{TokenKept: true, Apps: []cert.IssuerMove{
		{App: "nextcloud", Title: "Nextcloud", To: "https://homestead.example.com/apps/auth/x/", Digest: "n1"},
		{App: "immich", Title: "Immich", To: "https://homestead.example.com/apps/auth/y/", Digest: "i1"},
	}}
	err := a.srv.moveIssuers(context.Background(), "owner", moves)
	if err == nil || !strings.Contains(err.Error(), "Nextcloud (Authentik did not answer)") || strings.Contains(err.Error(), "Immich") {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(tried, ";") != "owner nextcloud n1;owner immich i1" {
		t.Fatalf("tried = %v", tried)
	}
	entries, _ := audit.New(a.st).List(100)
	found := false
	for _, e := range entries {
		if e.Action == "app.sign-in" && e.Outcome == audit.OutcomeError && strings.HasPrefix(e.Detail, "nextcloud: moving to") {
			found = true
		}
	}
	if !found {
		t.Fatal("the failed move was not audited")
	}
	// The default re-apply refuses a digest the app's plan no longer has.
	reapplyAppSignIn = prev
	if err := prev(context.Background(), a.srv.appSignIn, cert.IssuerMove{App: "immich", Digest: "stale"}, "owner"); err == nil ||
		!strings.Contains(err.Error(), "plan changed") {
		t.Fatalf("stale re-apply = %v", err)
	}
}

// ADR-048 §6 from TLS: the issuance plan of a certificate covering the
// public dashboard name carries the move list in its digest, and its
// operation ends with the same re-apply step.
func TestTLSIssuancePlanCarriesIssuerMoves(t *testing.T) {
	a, _ := issuerMoveRig(t, true)
	// Planning only: the fake provider needs no credentials.
	issuing := mountTestIssuing(t, a.srv, a.st, nil, issuanceFakeDNS{}, true)
	if err := cert.AddToStrategy(issuing.Wizards, cert.Certificate{Name: "dash", DNSNames: []string{"homestead.example.com"},
		State: cert.StatePending, Renewal: cert.Renewal{Auto: true}, Issuer: cert.Issuer{Name: "le", Type: cert.IssuerACME,
			Directory: cert.LetsEncryptStagingDirectory, Challenge: cert.ChallengeDNS01, DNSProvider: "fakedns"}}); err != nil {
		t.Fatal(err)
	}
	plan, err := issuing.Plan("dash")
	if err != nil || !plan.Ready || plan.IssuerMoves == nil || len(plan.IssuerMoves.Apps) != 1 || plan.IssuerMoves.Apps[0].App != "immich" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if _, err := issuing.Approve("dash", plan.Digest, "owner", true, false); err != nil {
		t.Fatal(err)
	}
	def, err := issuing.Definition("dash")
	if err != nil || def.Steps[len(def.Steps)-1].Name != cert.MoveStep {
		t.Fatalf("definition = %+v, %v", def.Steps, err)
	}
	var tried []string
	prev := reapplyAppSignIn
	reapplyAppSignIn = func(_ context.Context, _ *appSignIn, move cert.IssuerMove, actor string) error {
		tried = append(tried, actor+" "+move.App)
		return nil
	}
	t.Cleanup(func() { reapplyAppSignIn = prev })
	if err := def.Steps[len(def.Steps)-1].Run(context.Background()); err != nil || strings.Join(tried, ";") != "owner immich" {
		t.Fatalf("move step = %v, tried %v", err, tried)
	}
}
