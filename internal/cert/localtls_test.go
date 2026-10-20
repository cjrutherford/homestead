package cert

import (
	"context"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/docker"
	"homestead/internal/secret"
	"homestead/internal/store"
)

type localEnv struct {
	t      *testing.T
	dir    string
	st     *store.Store
	secs   *secret.Store
	ca     *LocalCA
	local  *LocalTLS
	stack  string
	routes []docker.Route
	clock  time.Time
	reload int
	rfail  error
	audits []string
	mu     sync.Mutex
}

func newLocalEnv(t *testing.T) *localEnv {
	t.Helper()
	e := &localEnv{t: t, dir: t.TempDir(), clock: time.Now().UTC().Truncate(time.Second)}
	e.stack = filepath.Join(e.dir, "caddy")
	e.open(filepath.Join(e.dir, "h.db"), filepath.Join(e.dir, "secret.key"))
	return e
}

func (e *localEnv) open(dbPath, keyPath string) {
	st, err := store.Open(dbPath)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { st.Close() })
	secs, err := secret.New(st, keyPath)
	if err != nil {
		e.t.Fatal(err)
	}
	e.st, e.secs = st, secs
	audit := func(actor, action, outcome, detail string) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.audits = append(e.audits, actor+" "+action+" "+outcome+" "+detail)
		return nil
	}
	e.ca = &LocalCA{Store: st, Secrets: secs, YAMLPath: filepath.Join(e.dir, "internal-ca.yaml"), Audit: audit, Now: func() time.Time { return e.clock }}
	e.local = &LocalTLS{
		CA: e.ca, Store: st, StackDir: e.stack, LeafLifetime: 72 * time.Hour, Audit: audit,
		Now:    func() time.Time { return e.clock },
		Routes: func() ([]docker.Route, error) { return e.routes, nil },
		Reload: func(context.Context) error { e.reload++; return e.rfail },
	}
}

func localRoute(host string) docker.Route {
	return docker.Route{Host: docker.Hostname(host), Target: docker.Target{Host: "app", Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
}

// applyRoutes writes the snippets the edge reconciler would, after the
// leaves are prepared, like the real reconcile.
func (e *localEnv) applyRoutes(routes ...docker.Route) { e.applyRoutesWith(true, routes...) }

func (e *localEnv) applyRoutesWith(prepare bool, routes ...docker.Route) {
	e.t.Helper()
	e.routes = routes
	if prepare {
		if _, _, err := e.local.EnsureLeaves(routes, false, "test"); err != nil {
			e.t.Fatal(err)
		}
	}
	if _, err := docker.ReconcileRoutes(e.stack, docker.RouteSet{Routes: routes}, docker.RouteSet{}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *localEnv) snippet(host string) string {
	b, err := os.ReadFile(filepath.Join(e.stack, "routes", host+".caddy"))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func TestLocalCAPersistsAcrossRestartAndRestore(t *testing.T) {
	e := newLocalEnv(t)
	info, created, err := e.ca.Ensure("test")
	if err != nil || !created || !info.Present || !info.KeyPresent {
		t.Fatalf("Ensure = %+v created=%v err=%v", info, created, err)
	}
	again, created, err := e.ca.Ensure("test")
	if err != nil || created || again.FingerprintSHA256 != info.FingerprintSHA256 {
		t.Fatalf("second Ensure created=%v fp %s vs %s err=%v", created, again.FingerprintSHA256, info.FingerprintSHA256, err)
	}
	// Restart: a new store and secret store over the same files.
	e.st.Close()
	e.open(filepath.Join(e.dir, "h.db"), filepath.Join(e.dir, "secret.key"))
	restarted, created, err := e.ca.Ensure("test")
	if err != nil || created || restarted.FingerprintSHA256 != info.FingerprintSHA256 {
		t.Fatalf("after restart created=%v fp %s want %s err=%v", created, restarted.FingerprintSHA256, info.FingerprintSHA256, err)
	}
	// Restore: the database and secret key file copied to a new appliance.
	e.st.Close()
	dir2 := t.TempDir()
	entries, _ := os.ReadDir(e.dir)
	for _, ent := range entries {
		if !ent.Type().IsRegular() || strings.HasSuffix(ent.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(e.dir, ent.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir2, ent.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.open(filepath.Join(dir2, "h.db"), filepath.Join(dir2, "secret.key"))
	restored, created, err := e.ca.Ensure("test")
	if err != nil || created || restored.FingerprintSHA256 != info.FingerprintSHA256 {
		t.Fatalf("after restore created=%v fp %s want %s err=%v", created, restored.FingerprintSHA256, info.FingerprintSHA256, err)
	}
	if _, err := e.ca.Authority(); err != nil {
		t.Fatalf("restored authority unusable: %v", err)
	}
}

func TestLocalCAMissingKeyIsNeverSilentlyRegenerated(t *testing.T) {
	e := newLocalEnv(t)
	info, _, err := e.ca.Ensure("test")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.secs.Delete(caKeyName(1)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.ca.Ensure("test"); !errors.Is(err, ErrCAKeyMissing) {
		t.Fatalf("Ensure err = %v, want ErrCAKeyMissing", err)
	}
	after, _ := e.ca.Info()
	if after.FingerprintSHA256 != info.FingerprintSHA256 || after.KeyPresent {
		t.Fatalf("root changed or key reported present: %+v", after)
	}
}

func TestLocalCAKeyOnlyInSecretStore(t *testing.T) {
	e := newLocalEnv(t)
	info, _, err := e.ca.Ensure("test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(caKeyName(1), "sys_") {
		t.Fatalf("key secret %q is not in the sys_ namespace", caKeyName(1))
	}
	yamlBytes, err := os.ReadFile(e.ca.YAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(yamlBytes), "PRIVATE KEY") || !strings.Contains(string(yamlBytes), info.FingerprintSHA256) {
		t.Fatalf("yaml mirror wrong: %s", yamlBytes)
	}
	for _, f := range []string{"h.db", "h.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(e.dir, f))
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), "PRIVATE KEY") {
			t.Fatalf("%s holds plaintext key material", f)
		}
	}
	key, _ := e.secs.Get(caKeyName(1))
	root, _ := e.ca.RootPEM()
	if strings.Contains(string(root), "PRIVATE") || strings.Count(string(root), "BEGIN") != 1 {
		t.Fatalf("root PEM is not a single public certificate: %s", root)
	}
	if strings.Contains(info.CertificatePEM, "PRIVATE") || strings.Contains(string(root), strings.TrimSpace(key)[:30]) {
		t.Fatal("key material leaked into public view")
	}
	e.local.Routes = func() ([]docker.Route, error) { return []docker.Route{localRoute("a.lan")}, nil }
	if _, err := e.local.Reconcile(context.Background(), "test", "", false); err != nil {
		t.Fatal(err)
	}
	for _, line := range e.audits {
		if strings.Contains(line, "PRIVATE") || strings.Contains(line, strings.TrimSpace(key)[27:60]) {
			t.Fatalf("audit leaks key material: %s", line)
		}
	}
}

func TestLocalCANameConstraints(t *testing.T) {
	e := newLocalEnv(t)
	if _, _, err := e.ca.Ensure("test"); err != nil {
		t.Fatal(err)
	}
	a, err := e.ca.Authority()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(a.Cert)
	verify := func(host string) error {
		chain, _, err := a.IssueServerCert([]string{host}, time.Hour*24, e.clock)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := parseCert(chain)
		_, err = c.Verify(x509.VerifyOptions{DNSName: host, Roots: pool, CurrentTime: e.clock.Add(time.Hour)})
		return err
	}
	for _, ok := range []string{"app.lan", "homestead.local", "deep.sub.home.arpa"} {
		if err := verify(ok); err != nil {
			t.Errorf("%s should verify: %v", ok, err)
		}
	}
	for _, bad := range []string{"example.com", "app.lan.example.com"} {
		if err := verify(bad); err == nil {
			t.Errorf("%s verified; the root must be name-constrained", bad)
		}
	}
	if !LocalNameAllowed("x.lan") || LocalNameAllowed("x.com") || LocalNameAllowed("notlan") {
		t.Error("LocalNameAllowed wrong")
	}
}

func TestLocalLeafIssuedPlacedAndPresented(t *testing.T) {
	e := newLocalEnv(t)
	pubAuto := docker.Route{Host: "pub.example.com", Target: docker.Target{Host: "app", Port: 80}, Exposure: docker.ExposurePublic, TLS: docker.TLSAuto}
	pubInternal := docker.Route{Host: "x.lan", Target: docker.Target{Host: "app", Port: 80}, Exposure: docker.ExposurePublic, TLS: docker.TLSInternal}
	off := docker.Route{Host: "off.lan", Target: docker.Target{Host: "app", Port: 80}, Exposure: docker.ExposureLocal, TLS: docker.TLSOff}
	// Snippets exist before any leaf (a route applied by another flow): the
	// maintenance run must issue, refresh the snippet and reload.
	e.applyRoutesWith(false, localRoute("craft.lan"), localRoute("wide.example.com"), pubAuto, pubInternal, off)
	res, err := e.local.Reconcile(context.Background(), "test", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reloaded != true || e.reload != 1 {
		t.Fatalf("reload res=%+v count=%d", res, e.reload)
	}
	name := docker.LocalCertName("craft.lan")
	if !strings.Contains(e.snippet("craft.lan"), "tls /etc/caddy/routes/certs/"+name+"/fullchain.pem") {
		t.Fatalf("local route does not present the leaf:\n%s", e.snippet("craft.lan"))
	}
	if strings.Contains(e.snippet("craft.lan"), "tls internal") {
		t.Fatal("the edge's own CA directive remained")
	}
	// A host outside the root's name constraints keeps the edge's issuance.
	if !strings.Contains(e.snippet("wide.example.com"), "tls internal") {
		t.Fatalf("uncoverable host should keep tls internal:\n%s", e.snippet("wide.example.com"))
	}
	// Public routes and other modes never get a local leaf.
	for _, h := range []string{"pub.example.com", "x.lan", "off.lan"} {
		if _, err := os.Stat(docker.CertDir(e.stack, docker.LocalCertName(docker.Hostname(h)))); err == nil {
			t.Errorf("%s was given a local leaf", h)
		}
	}
	// The placed leaf chains to the root and covers the host.
	chain, key, err := readPlaced(e.stack, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docker.ValidateCertPair([]string{"craft.lan"}, chain, key); err != nil {
		t.Fatal(err)
	}
	root, _ := e.ca.RootPEM()
	rc, _ := parseCert(root)
	leaf, _ := parseCert(chain)
	pool := x509.NewCertPool()
	pool.AddCert(rc)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "craft.lan", Roots: pool, CurrentTime: e.clock.Add(time.Minute)}); err != nil {
		t.Fatalf("leaf does not verify to the root: %v", err)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "craft.lan" {
		t.Fatalf("SANs = %v", leaf.DNSNames)
	}
	report, _ := e.local.Report()
	got := map[string]string{}
	for _, l := range report.Leaves {
		got[l.Host] = l.Status
	}
	if got["craft.lan"] != LeafOK || got["wide.example.com"] != LeafUncoverable || len(got) != 2 {
		t.Fatalf("report = %v", got)
	}
}

func readPlaced(stack, name string) (chain, key []byte, err error) {
	c, k := docker.CertPaths(stack, name)
	if chain, err = os.ReadFile(c); err != nil {
		return
	}
	key, err = os.ReadFile(k)
	return
}

func TestLocalLeafRenewalWithInjectedClock(t *testing.T) {
	e := newLocalEnv(t) // 72h leaves renew with 24h left
	e.applyRoutes(localRoute("craft.lan"))
	if !strings.Contains(e.snippet("craft.lan"), "fullchain.pem") {
		t.Fatal("a leaf prepared before the reconcile must render into the first snippet")
	}
	if _, err := e.local.Reconcile(context.Background(), "test", "", false); err != nil {
		t.Fatal(err)
	}
	first, _ := e.local.records()
	e.reload = 0
	// Day 1: well inside the lifetime, nothing to do and nothing reloaded.
	e.clock = e.clock.Add(24 * time.Hour)
	res, err := e.local.Reconcile(context.Background(), "scheduler", "", false)
	if err != nil || len(res.Renewed) != 0 || e.reload != 0 {
		t.Fatalf("early run res=%+v reload=%d err=%v", res, e.reload, err)
	}
	// Renewal window (< a third remaining): the edge reloads with a new leaf.
	e.clock = e.clock.Add(26 * time.Hour)
	report, _ := e.local.Report()
	if report.Leaves[0].Status != LeafRenewalDue {
		t.Fatalf("status = %s, want renewal-due", report.Leaves[0].Status)
	}
	before := e.snippet("craft.lan")
	res, err = e.local.Reconcile(context.Background(), "scheduler", "", false)
	if err != nil || len(res.Renewed) != 1 || !res.Reloaded || e.reload != 1 {
		t.Fatalf("renew res=%+v reload=%d err=%v", res, e.reload, err)
	}
	second, _ := e.local.records()
	if second["craft.lan"].Serial == first["craft.lan"].Serial {
		t.Fatal("leaf serial unchanged after renewal")
	}
	if e.snippet("craft.lan") != before {
		t.Fatal("snippet changed; renewal replaces the placement in place")
	}
	report, _ = e.local.Report()
	if report.Leaves[0].Status != LeafOK {
		t.Fatalf("after renewal status = %s", report.Leaves[0].Status)
	}
	// Expiry is reported and repaired.
	e.clock = e.clock.Add(100 * time.Hour)
	report, _ = e.local.Report()
	if report.Leaves[0].Status != LeafExpired {
		t.Fatalf("status = %s, want expired", report.Leaves[0].Status)
	}
}

func TestLocalLeafReloadRefusedRollsBackAndRetries(t *testing.T) {
	e := newLocalEnv(t)
	e.applyRoutes(localRoute("craft.lan"))
	_ = os.WriteFile(filepath.Join(e.stack, "routes", "craft.lan.caddy"), []byte(strings.Replace(e.snippet("craft.lan"), "tls /etc/caddy/routes/certs/"+docker.LocalCertName("craft.lan")+"/fullchain.pem /etc/caddy/routes/certs/"+docker.LocalCertName("craft.lan")+"/privkey.pem", "tls internal", 1)), 0o644)
	e.rfail = errors.New("caddy refused")
	if _, err := e.local.Reconcile(context.Background(), "test", "", false); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(e.snippet("craft.lan"), "fullchain.pem") {
		t.Fatal("snippet not rolled back after refused reload")
	}
	report, _ := e.local.Report()
	if report.Leaves[0].Status != LeafOK && report.Leaves[0].Status != LeafNotPresented {
		t.Fatalf("status = %s", report.Leaves[0].Status)
	}
	e.rfail = nil
	e.reload = 0
	res, err := e.local.Reconcile(context.Background(), "test", "", false)
	if err != nil || !res.Reloaded || e.reload != 1 || !strings.Contains(e.snippet("craft.lan"), "fullchain.pem") {
		t.Fatalf("retry res=%+v reload=%d err=%v", res, e.reload, err)
	}
}

func TestLocalLeafOrphansRemoved(t *testing.T) {
	e := newLocalEnv(t)
	e.applyRoutes(localRoute("a.lan"), localRoute("b.lan"))
	e.routes = []docker.Route{localRoute("a.lan")}
	// Inside the grace window a fresh leaf is kept (its route table save
	// may still be in flight).
	if res, err := e.local.Reconcile(context.Background(), "scheduler", "", false); err != nil || len(res.Removed) != 0 {
		t.Fatalf("fresh leaf swept: res=%+v err=%v", res, err)
	}
	e.clock = e.clock.Add(orphanGrace + time.Minute)
	res, err := e.local.Reconcile(context.Background(), "scheduler", "", false)
	if err != nil || len(res.Removed) != 1 || res.Removed[0] != "b.lan" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(docker.CertDir(e.stack, docker.LocalCertName("b.lan"))); err == nil {
		t.Fatal("orphan placement not removed")
	}
	recs, _ := e.local.records()
	if len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}
}

func TestLocalTLSNoCAWithoutLocalRoutes(t *testing.T) {
	e := newLocalEnv(t)
	e.routes = []docker.Route{{Host: "pub.example.com", Target: docker.Target{Host: "a", Port: 1}, Exposure: docker.ExposurePublic, TLS: docker.TLSAuto}}
	if _, err := e.local.Reconcile(context.Background(), "scheduler", "", false); err != nil {
		t.Fatal(err)
	}
	if info, _ := e.ca.Info(); info.Present {
		t.Fatal("CA created with no local-only HTTPS route")
	}
}

func TestLocalCARotationPlanAndApproval(t *testing.T) {
	e := newLocalEnv(t)
	e.applyRoutes(localRoute("a.lan"), localRoute("b.lan"))
	old, _ := e.ca.Info()
	plan, err := e.local.PlanRotation()
	if err != nil || len(plan.Leaves) != 2 || plan.Digest == "" || plan.DevicesMustRetrust == "" {
		t.Fatalf("plan = %+v err=%v", plan, err)
	}
	if _, err := e.local.Rotate(context.Background(), "owner", "stale"); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("stale digest err = %v", err)
	}
	if cur, _ := e.ca.Info(); cur.FingerprintSHA256 != old.FingerprintSHA256 {
		t.Fatal("root changed on a refused rotation")
	}
	res, err := e.local.Rotate(context.Background(), "owner", plan.Digest)
	if err != nil || len(res.Renewed) != 2 {
		t.Fatalf("rotate res=%+v err=%v", res, err)
	}
	cur, _ := e.ca.Info()
	if cur.FingerprintSHA256 == old.FingerprintSHA256 || cur.Generation != 2 || cur.PreviousFingerprint != old.FingerprintSHA256 {
		t.Fatalf("rotated info = %+v", cur)
	}
	if _, err := e.secs.Get(caKeyName(1)); err == nil {
		t.Fatal("old root key survived rotation")
	}
	report, _ := e.local.Report()
	for _, l := range report.Leaves {
		if l.Status != LeafOK || l.CAFingerprint != cur.FingerprintSHA256 {
			t.Fatalf("leaf %+v not on the new root", l)
		}
	}
	// The plan digest of the old state no longer applies.
	if _, err := e.local.Rotate(context.Background(), "owner", plan.Digest); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("replayed digest err = %v", err)
	}
}

func TestExpiringRootGrades(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		left time.Duration
		want string
	}{{2 * 365 * 24 * time.Hour, ""}, {200 * 24 * time.Hour, "warn"}, {10 * 24 * time.Hour, "fail"}, {-time.Hour, "fail"}} {
		if got := ExpiringRoot(CAInfo{NotAfter: now.Add(c.left)}, now); got != c.want {
			t.Errorf("left %v = %q, want %q", c.left, got, c.want)
		}
	}
}

func TestLocalCAOrphanedKeyIsNeverOverwritten(t *testing.T) {
	e := newLocalEnv(t)
	if _, _, err := e.ca.Ensure("test"); err != nil {
		t.Fatal(err)
	}
	before, _ := e.secs.Get(caKeyName(1))
	// Database-only restore: the row is gone, the secret remains.
	if _, err := e.st.DB().Exec(`DELETE FROM internal_ca`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.ca.Ensure("test"); !errors.Is(err, ErrCAKeyOrphaned) {
		t.Fatalf("Ensure err = %v, want ErrCAKeyOrphaned", err)
	}
	if after, _ := e.secs.Get(caKeyName(1)); after != before {
		t.Fatal("live CA key was replaced")
	}
	e.routes = []docker.Route{localRoute("a.lan")}
	if _, _, err := e.local.EnsureLeaves(e.routes, false, "test"); !errors.Is(err, ErrCAKeyOrphaned) {
		t.Fatalf("EnsureLeaves err = %v", err)
	}
	rep, _ := e.local.Report()
	if !rep.OrphanedKey {
		t.Fatal("report does not flag the orphaned key")
	}
	if !strings.Contains(strings.Join(e.audits, "\n"), "tls.local-ca.ensure error") {
		t.Fatalf("CA ensure failure not audited: %v", e.audits)
	}
}

func TestLocalCABackdatesNotBefore(t *testing.T) {
	e := newLocalEnv(t)
	e.applyRoutes(localRoute("a.lan"))
	ca, _ := e.ca.Info()
	if want := e.clock.Add(-ClockSkewBackdate); !ca.NotBefore.Equal(want) {
		t.Fatalf("CA NotBefore = %s, want %s", ca.NotBefore, want)
	}
	leaves, _ := e.local.Leaves()
	if want := e.clock.Add(-ClockSkewBackdate); !leaves[0].NotBefore.Equal(want) {
		t.Fatalf("leaf NotBefore = %s, want %s", leaves[0].NotBefore, want)
	}
	chain, _ := docker.ReadPlacedChainAny(e.stack, leaves[0].CertName)
	c, _ := parseCert(chain)
	if c.KeyUsage&x509.KeyUsageKeyEncipherment != 0 {
		t.Fatal("ECDSA leaf carries KeyEncipherment")
	}
}

func TestLocalCAOldKeyDestroyFailureAudited(t *testing.T) {
	e := newLocalEnv(t)
	e.applyRoutes(localRoute("a.lan"))
	// Make the secret delete fail by dropping the table's delete path.
	if _, err := e.st.DB().Exec(`CREATE TRIGGER nodel BEFORE DELETE ON secrets BEGIN SELECT RAISE(ABORT, 'locked'); END`); err != nil {
		t.Fatal(err)
	}
	ca, _ := e.ca.Info()
	if _, _, err := e.ca.Rotate("owner", ca.FingerprintSHA256); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(e.audits, "\n"), "tls.local-ca.key-destroy error") {
		t.Fatalf("destroy failure not audited: %v", e.audits)
	}
	if rep, _ := e.local.Report(); len(rep.LeftoverKeys) != 1 {
		t.Fatalf("leftover keys = %v", rep.LeftoverKeys)
	}
}

func TestLocalCAConcurrentApprovalsRotateOnce(t *testing.T) {
	e := newLocalEnv(t)
	e.applyRoutes(localRoute("a.lan"))
	plan, err := e.local.PlanRotation()
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = e.local.Rotate(context.Background(), "owner", plan.Digest)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrStalePlan) {
			t.Fatalf("unexpected error %v", err)
		}
	}
	cur, _ := e.ca.Info()
	if ok != 1 || cur.Generation != 2 {
		t.Fatalf("rotations succeeded = %d, generation = %d; want 1 and 2", ok, cur.Generation)
	}
	ca, _ := e.ca.Info()
	if _, _, err := e.ca.Rotate("owner", "AA:BB"); !errors.Is(err, ErrRotationStale) || ca.Generation != 2 {
		t.Fatalf("CAS guard: %v", err)
	}
}

func TestLocalLeafCoversDashboardBothNames(t *testing.T) {
	e := newLocalEnv(t)
	dash := docker.Route{Host: "homestead.lan", Aliases: []docker.Hostname{"homestead.local"}, Dashboard: true,
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	e.applyRoutes(dash)
	chain, key, err := readPlaced(e.stack, docker.LocalCertName("homestead.lan"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docker.ValidateCertPair([]string{"homestead.lan", "homestead.local"}, chain, key); err != nil {
		t.Fatal(err)
	}
	root, _ := e.ca.RootPEM()
	rc, _ := parseCert(root)
	leaf, _ := parseCert(chain)
	pool := x509.NewCertPool()
	pool.AddCert(rc)
	for _, n := range []string{"homestead.lan", "homestead.local"} {
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: n, Roots: pool, CurrentTime: e.clock.Add(time.Minute)}); err != nil {
			t.Errorf("leaf does not verify for %s: %v", n, err)
		}
	}
	if !strings.Contains(e.snippet("homestead.lan"), "homestead.lan, homestead.local {") {
		t.Fatalf("snippet:\n%s", e.snippet("homestead.lan"))
	}
}
