package server

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/cert"
	"homestead/internal/desired"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/secret"
	"homestead/internal/store"
)

func localTLSServer(t *testing.T, routes []docker.Route) (*Server, *store.Store, *cert.LocalTLS, *secret.Store, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	secrets, err := secret.New(st, filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalTLS(st, secrets, filepath.Join(dir, "internal-ca.yaml"), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	local.StackDir = filepath.Join(dir, "caddy")
	local.Routes = func() ([]docker.Route, error) { return routes, nil }
	local.Reload = func(context.Context) error { return nil }
	local.Now = func() time.Time { return clock }
	local.CA.Now = local.Now
	srv := New("127.0.0.1:0")
	srv.MountLocalTLS(st, local)
	return srv, st, local, secrets, &clock
}

func lanRoute(host string) docker.Route {
	return docker.Route{Host: docker.Hostname(host), Target: docker.Target{Host: "app", Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
}

func TestRootDownloadServesOnlyPublicCertificate(t *testing.T) {
	srv, st, local, secrets, _ := localTLSServer(t, nil)
	if rec := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/local-ca/root.pem", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("root before the CA exists = %d", rec.Code)
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tls/local-ca", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	var created struct {
		CA      cert.CAInfo `json:"ca"`
		Created bool        `json:"created"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if !created.Created || created.CA.FingerprintSHA256 == "" {
		t.Fatalf("create body = %s", rec.Body.String())
	}
	key, err := secrets.Get(cert.LocalCAKeyPrefix + "1")
	if err != nil {
		t.Fatal(err)
	}
	keyBody := strings.TrimSpace(strings.Split(key, "\n")[1])
	pemRec := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/local-ca/root.pem", nil)
	derRec := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/local-ca/root.der", nil)
	if pemRec.Code != 200 || derRec.Code != 200 {
		t.Fatalf("root codes %d %d", pemRec.Code, derRec.Code)
	}
	block, rest := pem.Decode(pemRec.Body.Bytes())
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		t.Fatalf("PEM is not exactly one certificate: %q", pemRec.Body.String())
	}
	parsed, err := x509.ParseCertificate(derRec.Body.Bytes())
	if err != nil || !parsed.IsCA || cert.CertFingerprint(parsed.Raw) != created.CA.FingerprintSHA256 {
		t.Fatalf("DER wrong: %v", err)
	}
	if cert.CertFingerprint(block.Bytes) != created.CA.FingerprintSHA256 {
		t.Fatal("PEM and DER disagree")
	}
	for _, rec := range []string{pemRec.Body.String(), derRec.Body.String(), created.CA.CertificatePEM} {
		if strings.Contains(rec, "PRIVATE") || strings.Contains(rec, keyBody) {
			t.Fatal("key material in a public response")
		}
	}
	if pemRec.Header().Get("Content-Disposition") == "" || pemRec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers = %v", pemRec.Header())
	}
	// Status, leaves and rotation plan never carry key material either.
	for _, path := range []string{"/api/v1/tls/local-ca", "/api/v1/tls/local-ca/leaves", "/api/v1/tls/local-ca/rotation"} {
		body := serveRequest(t, srv, http.MethodGet, path, nil).Body.String()
		if strings.Contains(body, "PRIVATE") || strings.Contains(body, keyBody) {
			t.Fatalf("%s leaks key material", path)
		}
	}
	entries, _ := audit.New(st).List(100)
	for _, e := range entries {
		if strings.Contains(e.Detail, "PRIVATE") || strings.Contains(e.Detail, keyBody) {
			t.Fatalf("audit leaks key: %s", e.Detail)
		}
	}
	_ = local
}

func TestRootRoutesOnlyGETAndOffTheRecoverySocket(t *testing.T) {
	srv, _, _, _, _ := localTLSServer(t, nil)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if rec := serveRequest(t, srv, m, "/api/v1/tls/local-ca/root.pem", nil); rec.Code == http.StatusOK {
			t.Errorf("%s root.pem = 200", m)
		}
	}
	full, _ := fullServer(t)
	for _, pattern := range []string{"GET /api/v1/tls/local-ca/root.pem", "GET /api/v1/tls/local-ca/root.der"} {
		if c, ok := Classify(pattern); !ok || c != ClassPublic {
			t.Errorf("%s class = %v (ADR-030: explicit public exception)", pattern, c)
		}
		method, path := routeRequest(pattern)
		rec := httptest.NewRecorder()
		full.SocketHandler().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("recovery socket serves %s (%d)", pattern, rec.Code)
		}
	}
	// Every other local-ca route needs a session.
	for pattern, class := range routeClasses {
		if strings.Contains(pattern, "/tls/local-ca") && !strings.Contains(pattern, "root.") && class != ClassSession {
			t.Errorf("%s class = %v, want session", pattern, class)
		}
	}
}

func TestRootDownloadReachablePreLoginWithSessionRoutesStillGated(t *testing.T) {
	a := newEnforcedApp(t)
	// Unclaimed and not logged in: reachable (404 only because no CA yet).
	if rec := a.do(http.MethodGet, "/api/v1/tls/local-ca/root.pem", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("pre-claim root = %d, want 404 (reachable, no CA)", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/api/v1/tls/local-ca", "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("pre-claim status = %d, want 403", rec.Code)
	}
	token := a.setupToken(t)
	if rec := a.do(http.MethodGet, "/api/v1/tls/local-ca", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/api/v1/tls/local-ca", "", bearer(token)); rec.Code != http.StatusOK {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	rec := a.do(http.MethodGet, "/api/v1/tls/local-ca/root.pem", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "BEGIN CERTIFICATE") {
		t.Fatalf("anonymous root = %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/api/v1/tls/local-ca", "", bearer(token)); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "fingerprint_sha256") {
		t.Fatalf("status with session = %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestLocalTLSDoctorStates(t *testing.T) {
	routes := []docker.Route{lanRoute("craft.lan")}
	_, _, local, secrets, clock := localTLSServer(t, routes)
	find := func(name string) doctor.Check {
		for _, c := range localTLSDoctorChecks(local, local.Now)() {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("no check %q", name)
		return doctor.Check{}
	}
	if c := find("local-ca"); c.Status != doctor.StatusWarn || !strings.Contains(c.Detail, "not created") {
		t.Fatalf("absent with local routes: %+v", c)
	}
	if _, err := local.Reconcile(context.Background(), "test", "", false); err != nil {
		t.Fatal(err)
	}
	if c := find("local-ca"); c.Status != doctor.StatusOK || !strings.Contains(c.Detail, "SHA-256") {
		t.Fatalf("present: %+v", c)
	}
	// The snippet is not written in this unit test: the leaf is placed and
	// valid, and with no snippet nothing contradicts it.
	if c := find("local-leaf:craft.lan"); c.Status != doctor.StatusOK {
		t.Fatalf("leaf: %+v", c)
	}
	// Leaf renewal window, then expiry.
	*clock = clock.Add(61 * 24 * time.Hour)
	if c := find("local-leaf:craft.lan"); c.Status != doctor.StatusWarn || !strings.Contains(c.Detail, "renewal-due") {
		t.Fatalf("renewal-due: %+v", c)
	}
	*clock = clock.Add(40 * 24 * time.Hour)
	if c := find("local-leaf:craft.lan"); c.Status != doctor.StatusFail || !strings.Contains(c.Detail, "expired") {
		t.Fatalf("expired: %+v", c)
	}
	// Root windows.
	*clock = time.Now().UTC().Add(10*365*24*time.Hour - 100*24*time.Hour)
	if c := find("local-ca"); c.Status != doctor.StatusWarn {
		t.Fatalf("root expiring in a year: %+v", c)
	}
	*clock = time.Now().UTC().Add(10*365*24*time.Hour - 5*24*time.Hour)
	if c := find("local-ca"); c.Status != doctor.StatusFail {
		t.Fatalf("root expiring in days: %+v", c)
	}
	// Lost key fails and is never regenerated.
	*clock = time.Now().UTC()
	if err := secrets.Delete(cert.LocalCAKeyPrefix + "1"); err != nil {
		t.Fatal(err)
	}
	if c := find("local-ca"); c.Status != doctor.StatusFail || !strings.Contains(c.Detail, "key is missing") {
		t.Fatalf("key missing: %+v", c)
	}
	// An uncoverable host is flagged, not silently issued.
	local.Routes = func() ([]docker.Route, error) { return []docker.Route{lanRoute("odd.example.com")}, nil }
	if c := find("local-leaf:odd.example.com"); c.Status != doctor.StatusWarn || !strings.Contains(c.Detail, "uncoverable") {
		t.Fatalf("uncoverable: %+v", c)
	}
}

func TestLocalTLSLeafLifetimeKnobIsValidated(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "h.db"))
	defer st.Close()
	secrets, _ := secret.New(st, filepath.Join(dir, "key"))
	if _, err := NewLocalTLS(st, secrets, "", func(string) string { return "30m" }); err == nil {
		t.Fatal("30m leaf lifetime accepted")
	}
	l, err := NewLocalTLS(st, secrets, "", func(string) string { return "2h" })
	if err != nil || l.LeafLifetime != 2*time.Hour {
		t.Fatalf("2h: %v %v", l, err)
	}
}

func TestLocalTLSSchedulerJobRenewsAndIssues(t *testing.T) {
	_, _, local, _, clock := localTLSServer(t, []docker.Route{lanRoute("craft.lan")})
	jobs, err := localTLSSource(local, func() time.Time { return *clock }).ScheduledJobs()
	if err != nil || len(jobs) != 1 || jobs[0].ID != LocalTLSJobID || !jobs[0].Enabled {
		t.Fatalf("jobs = %+v err=%v", jobs, err)
	}
	local.LeafLifetime = 12 * time.Hour
	jobs, _ = localTLSSource(local, func() time.Time { return *clock }).ScheduledJobs()
	if jobs[0].Interval != 2*time.Hour {
		t.Fatalf("short-lifetime interval = %v", jobs[0].Interval)
	}
}

func TestRotationApprovalRequiresMatchingDigestOverHTTP(t *testing.T) {
	srv, _, local, _, _ := localTLSServer(t, []docker.Route{lanRoute("app.home.arpa")})
	if _, _, err := local.CA.Ensure("test"); err != nil {
		t.Fatal(err)
	}
	before, _ := local.CA.Info()
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tls/local-ca/rotation", map[string]string{"digest": "stale"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale digest = %d (%s)", rec.Code, rec.Body.String())
	}
	if after, _ := local.CA.Info(); after.FingerprintSHA256 != before.FingerprintSHA256 {
		t.Fatal("a stale digest rotated the root")
	}
}

func TestReissueIsAuditedAndExportCarriesNoKey(t *testing.T) {
	_, st, local, secrets, _ := localTLSServer(t, []docker.Route{lanRoute("app.home.arpa")})
	if _, _, err := local.CA.Ensure("test"); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Reconcile(context.Background(), "owner", "", true); err != nil {
		t.Fatal(err)
	}
	entries, _ := audit.New(st).List(100)
	seen := false
	for _, e := range entries {
		if e.Action == "tls.local-leaf.renew" || e.Action == "tls.local-leaf.issue" {
			seen = e.Actor == "owner" || seen
		}
	}
	if !seen {
		t.Fatalf("re-issue left no audit entry: %+v", entries)
	}
	key, err := secrets.Get(cert.LocalCAKeyPrefix + "1")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	desiredPath := filepath.Join(dir, "desired.yaml")
	if err := desired.Save(desiredPath, desired.State{Version: desired.CurrentVersion, Settings: map[string]any{"hostname": "h"}}); err != nil {
		t.Fatal(err)
	}
	export, err := desired.ExportState(desiredPath, secrets)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(export)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), cert.LocalCAKeyPrefix) ||
		strings.Contains(string(raw), strings.TrimSpace(strings.Split(key, "\n")[1])) {
		t.Fatalf("config export carries CA key material or its name: %s", raw)
	}
}
