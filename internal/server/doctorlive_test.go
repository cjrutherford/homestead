package server

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/router"
	"homestead/internal/secret"
	"homestead/internal/sso"
	"homestead/internal/store"
)

const doctorSecret = "sealed-cloudflare-token-do-not-print"

type doctorRig struct {
	srv *Server
	st  *store.Store
	cf  *fakeCloudflare
	cfg DoctorLiveConfig
	// ready is what the fake cloudflared answers on /ready.
	ready *atomic.Value
}

// doctorLiveRig is a recorded tunnel for auth.example.com with a fake
// Cloudflare, a fake connector and a fake cloudflared metrics endpoint.
// Nothing touches the host or the network.
func doctorLiveRig(t *testing.T) *doctorRig {
	t.Helper()
	srv, st, secrets := tunnelTestServer(t)
	cf := newFakeCloudflare(t)
	fakeConnector(t)
	if err := secrets.Set("dns_cloudflare_api_token", doctorSecret); err != nil {
		t.Fatal(err)
	}
	if err := router.RecordRoute(st, router.TunnelRoute{Hostname: "auth.example.com", TunnelID: "tid", Service: tunnelEntryService}, "homestead"); err != nil {
		t.Fatal(err)
	}
	if err := router.SetTunnelAccount(st, "tid", "abcd0000000000000000000000000001", "zone1", ""); err != nil {
		t.Fatal(err)
	}
	cf.tunnels["tid"] = "homestead"
	id := cf.addRecord("CNAME", "auth.example.com", "tid.cfargotunnel.com")
	cf.records[id]["proxied"] = true
	ready := &atomic.Value{}
	ready.Store(`{"status":200,"readyConnections":4,"connectorId":"x"}`)
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			_, _ = w.Write([]byte(ready.Load().(string)))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(metrics.Close)
	prev := connectorMetricsURLs
	connectorMetricsURLs = func() []string { return []string{metrics.URL} }
	t.Cleanup(func() { connectorMetricsURLs = prev })
	return &doctorRig{srv: srv, st: st, cf: cf, ready: ready,
		cfg: DoctorLiveConfig{Store: st, Secrets: secrets, CatalogDir: filepath.Join("..", "..", "catalog")}}
}

func only(t *testing.T, checks []doctor.Check) doctor.Check {
	t.Helper()
	if len(checks) != 1 {
		t.Fatalf("want one check, got %+v", checks)
	}
	return checks[0]
}

func TestDoctorTunnelConnector(t *testing.T) {
	rig := doctorLiveRig(t)
	ctx := context.Background()
	if c := only(t, probeTunnelConnector(ctx, rig.cfg)); c.Status != doctor.StatusOK || !strings.Contains(c.Detail, "4 connection") {
		t.Fatalf("healthy: %+v", c)
	}
	rig.ready.Store(`{"status":503,"readyConnections":0}`)
	if c := only(t, probeTunnelConnector(ctx, rig.cfg)); c.Status != doctor.StatusFail || c.Fix == nil || !strings.Contains(c.Fix.Command, "journalctl") {
		t.Fatalf("no connections: %+v", c)
	}
	tunnelSystemctl = func(...string) error { return errors.New("inactive") }
	if c := only(t, probeTunnelConnector(ctx, rig.cfg)); c.Status != doctor.StatusFail || !strings.Contains(c.Detail, "not running") {
		t.Fatalf("inactive: %+v", c)
	}
	tunnelSystemctl = func(...string) error { return nil }
	connectorMetricsURLs = func() []string { return []string{"http://127.0.0.1:1"} }
	if c := only(t, probeTunnelConnector(ctx, rig.cfg)); c.Status != doctor.StatusUnknown {
		t.Fatalf("metrics down: %+v", c)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if c := only(t, probeTunnelConnector(done, rig.cfg)); c.Status != doctor.StatusUnknown {
		t.Fatalf("timeout: %+v", c)
	}
}

func TestDoctorTunnelConnectorMetricFallback(t *testing.T) {
	rig := doctorLiveRig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			_, _ = w.Write([]byte("# HELP cloudflared_tunnel_ha_connections Number of active HA connections\n# TYPE cloudflared_tunnel_ha_connections gauge\ncloudflared_tunnel_ha_connections 3\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	connectorMetricsURLs = func() []string { return []string{ts.URL} }
	if c := only(t, probeTunnelConnector(context.Background(), rig.cfg)); c.Status != doctor.StatusOK || !strings.Contains(c.Detail, "3 connection") {
		t.Fatalf("%+v", c)
	}
}

func TestDoctorNoTunnelIsFine(t *testing.T) {
	_, st, secrets := tunnelTestServer(t)
	cfg := DoctorLiveConfig{Store: st, Secrets: secrets}
	for _, c := range []doctor.Check{only(t, probeTunnelConnector(context.Background(), cfg)),
		only(t, probeTunnelExists(context.Background(), cfg)), only(t, probeTunnelDNS(context.Background(), cfg)),
		only(t, probeEdgeCertificates(context.Background(), cfg))} {
		if c.Status != doctor.StatusOK {
			t.Fatalf("%+v", c)
		}
	}
}

func TestDoctorTunnelExists(t *testing.T) {
	rig := doctorLiveRig(t)
	ctx := context.Background()
	if c := only(t, probeTunnelExists(ctx, rig.cfg)); c.Status != doctor.StatusOK || !strings.Contains(c.Detail, "homestead") {
		t.Fatalf("exists: %+v", c)
	}
	delete(rig.cf.tunnels, "tid")
	c := only(t, probeTunnelExists(ctx, rig.cfg))
	if c.Status != doctor.StatusFail || c.Fix == nil || c.Fix.Link != "/admin/tunnel" {
		t.Fatalf("missing: %+v", c)
	}
}

func TestDoctorTunnelExistsWithoutToken(t *testing.T) {
	_, st, secrets := tunnelTestServer(t)
	newFakeCloudflare(t)
	if err := router.RecordRoute(st, router.TunnelRoute{Hostname: "auth.example.com", TunnelID: "tid", Service: tunnelEntryService}, "homestead"); err != nil {
		t.Fatal(err)
	}
	if err := router.SetTunnelAccount(st, "tid", "abcd0000000000000000000000000001", "zone1", ""); err != nil {
		t.Fatal(err)
	}
	cfg := DoctorLiveConfig{Store: st, Secrets: secrets}
	if c := only(t, probeTunnelExists(context.Background(), cfg)); c.Status != doctor.StatusUnknown {
		t.Fatalf("no token: %+v", c)
	}
}

func TestDoctorTunnelDNS(t *testing.T) {
	rig := doctorLiveRig(t)
	ctx := context.Background()
	if c := only(t, probeTunnelDNS(ctx, rig.cfg)); c.Status != doctor.StatusOK || c.Name != "tunnel-dns:auth.example.com" {
		t.Fatalf("ok: %+v", c)
	}
	for id, r := range rig.cf.records {
		r["content"] = "other.cfargotunnel.com"
		_ = id
	}
	if c := only(t, probeTunnelDNS(ctx, rig.cfg)); c.Status != doctor.StatusFail || !strings.Contains(c.Detail, "other.cfargotunnel.com") {
		t.Fatalf("elsewhere: %+v", c)
	}
	rig.cf.records = map[string]map[string]any{}
	if c := only(t, probeTunnelDNS(ctx, rig.cfg)); c.Status != doctor.StatusFail || !strings.Contains(c.Detail, "no DNS record") {
		t.Fatalf("missing: %+v", c)
	}
}

func TestDoctorEdgeCertificates(t *testing.T) {
	rig := doctorLiveRig(t)
	ctx := context.Background()
	if c := only(t, probeEdgeCertificates(ctx, rig.cfg)); c.Status != doctor.StatusOK {
		t.Fatalf("covered: %+v", c)
	}
	// Two levels deep is not covered by the free certificate.
	if err := router.RecordRoute(rig.st, router.TunnelRoute{Hostname: "nextcloud.homestead.example.com", TunnelID: "tid", Service: tunnelEntryService}, "homestead"); err != nil {
		t.Fatal(err)
	}
	var warned bool
	for _, c := range probeEdgeCertificates(ctx, rig.cfg) {
		if c.Name == "edge-certificate:nextcloud.homestead.example.com" {
			warned = c.Status == doctor.StatusWarn && c.Fix != nil && strings.Contains(c.Detail, "nextcloud.homestead.example.com")
		}
	}
	if !warned {
		t.Fatal("uncovered name not warned")
	}
}

func authentikManager(t *testing.T, st *store.Store, secrets *secret.Store, url string) *sso.Manager {
	t.Helper()
	old := authentikInstalled
	authentikInstalled = func(*store.Store) bool { return true }
	t.Cleanup(func() { authentikInstalled = old })
	return &sso.Manager{DB: st.DB(), Secrets: secrets, Client: func() *http.Client { return http.DefaultClient },
		Now: time.Now, Defaults: func() sso.Defaults { return sso.Defaults{AuthentikURL: url} },
		Installed: func() bool { return true }}
}

func TestDoctorAuthentik(t *testing.T) {
	rig := doctorLiveRig(t)
	code := atomic.Int32{}
	code.Store(204)
	var path atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		w.WriteHeader(int(code.Load()))
	}))
	cfg := rig.cfg
	cfg.SSO = authentikManager(t, rig.st, rig.cfg.Secrets, ts.URL+"/apps/auth")
	ctx := context.Background()
	if c := only(t, probeAuthentik(ctx, cfg)); c.Status != doctor.StatusOK || path.Load() != "/apps/auth/-/health/live/" {
		t.Fatalf("ok: %+v %v", c, path.Load())
	}
	code.Store(503)
	if c := only(t, probeAuthentik(ctx, cfg)); c.Status != doctor.StatusFail {
		t.Fatalf("503: %+v", c)
	}
	code.Store(404)
	if c := only(t, probeAuthentik(ctx, cfg)); c.Status != doctor.StatusWarn {
		t.Fatalf("404: %+v", c)
	}
	ts.Close()
	c := only(t, probeAuthentik(ctx, cfg))
	if c.Status != doctor.StatusFail || c.Fix == nil || strings.Contains(c.Detail, "GET ") {
		t.Fatalf("down: %+v", c)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if c := only(t, probeAuthentik(done, cfg)); c.Status != doctor.StatusUnknown {
		t.Fatalf("timeout: %+v", c)
	}
}

func TestDoctorSignInPlanCheck(t *testing.T) {
	rig := doctorLiveRig(t)
	m := authentikManager(t, rig.st, rig.cfg.Secrets, "http://x")
	c := only(t, signInDoctorChecks(m)())
	if c.Name != "sign-in" || c.Status != doctor.StatusOK || !strings.Contains(c.Detail, "not set up") {
		t.Fatalf("%+v", c)
	}
}

var appPort = map[string]int{"immich": 2283, "nextcloud": 8080}

func installDoctorApp(t *testing.T, st *store.Store, app string) {
	t.Helper()
	old := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(t.TempDir(), name) }
	t.Cleanup(func() { stackDirFor = old })
	installApp(t, st, app, docker.Route{Host: docker.Hostname(app + ".lan"), Target: docker.Target{Host: "server", Port: appPort[app]},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
}

func TestDoctorAppAccess(t *testing.T) {
	rig := doctorLiveRig(t)
	installDoctorApp(t, rig.st, "immich")
	records := appsso.Records{DB: rig.st.DB()}
	if err := records.Save(appsso.Record{Package: "immich", Adapter: "immich", ClientID: "homestead-immich", Issuer: "https://x/", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	checks := appAccessDoctorChecks(rig.st, rig.cfg.CatalogDir)()
	c := only(t, checks)
	if c.Name != "app-access:immich" || c.Status != doctor.StatusWarn || !strings.Contains(c.Detail, "open to every Authentik user") {
		t.Fatalf("open: %+v", c)
	}
	users, admins := sso.AppGroups("immich")
	if err := records.Save(appsso.Record{Package: "immich", Adapter: "immich", ClientID: "homestead-immich", Issuer: "https://x/", UpdatedAt: time.Now(),
		Access: &sso.Access{Groups: []string{users, admins}}}); err != nil {
		t.Fatal(err)
	}
	if c := only(t, appAccessDoctorChecks(rig.st, rig.cfg.CatalogDir)()); c.Status != doctor.StatusOK {
		t.Fatalf("closed: %+v", c)
	}
}

func TestDoctorAppContainers(t *testing.T) {
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := entries.Get("immich")
	if len(m.Services) < 2 {
		t.Fatalf("immich services = %d", len(m.Services))
	}
	old := migrationContainers
	t.Cleanup(func() { migrationContainers = old })
	build := func(state, status string, skip int) {
		migrationContainers = func(context.Context) ([]docker.Container, error) {
			var out []docker.Container
			for i, s := range m.Services {
				if i == skip {
					continue
				}
				c := docker.Container{Names: []string{m.Name + "-" + s.Name}, State: "running", Status: "Up 2 hours (healthy)",
					Labels: map[string]string{docker.LabelProject: m.Name, docker.LabelService: s.Name}}
				if i == 0 && state != "" {
					c.State, c.Status = state, status
				}
				out = append(out, c)
			}
			return out, nil
		}
	}
	ctx := context.Background()
	build("", "", -1)
	if c := only(t, probeAppContainers(ctx, &lazyContainers{}, m)); c.Status != doctor.StatusOK {
		t.Fatalf("healthy: %+v", c)
	}
	build("exited", "Exited (1) 3 minutes ago", -1)
	c := only(t, probeAppContainers(ctx, &lazyContainers{}, m))
	if c.Status != doctor.StatusFail || !strings.Contains(c.Detail, m.Services[0].Name+" is exited") || c.Fix == nil || c.Fix.Command == "" {
		t.Fatalf("stopped: %+v", c)
	}
	build("running", "Up 1 minute (unhealthy)", -1)
	if c := only(t, probeAppContainers(ctx, &lazyContainers{}, m)); c.Status != doctor.StatusFail || !strings.Contains(c.Detail, "unhealthy") {
		t.Fatalf("unhealthy: %+v", c)
	}
	build("running", "Up 5 seconds (health: starting)", -1)
	if c := only(t, probeAppContainers(ctx, &lazyContainers{}, m)); c.Status != doctor.StatusWarn {
		t.Fatalf("starting: %+v", c)
	}
	build("", "", 1)
	if c := only(t, probeAppContainers(ctx, &lazyContainers{}, m)); c.Status != doctor.StatusFail || !strings.Contains(c.Detail, "does not exist") {
		t.Fatalf("missing: %+v", c)
	}
	build("exited", "Exited (0) 3 minutes ago", -1)
	if c := only(t, probeAppContainers(ctx, &lazyContainers{}, m)); c.Status != doctor.StatusOK {
		t.Fatalf("finished job: %+v", c)
	}
	migrationContainers = func(context.Context) ([]docker.Container, error) { return nil, errors.New("no engine") }
	if c := only(t, probeAppContainers(ctx, &lazyContainers{}, m)); c.Status != doctor.StatusUnknown {
		t.Fatalf("no engine: %+v", c)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if c := only(t, probeAppContainers(done, &lazyContainers{}, m)); c.Status != doctor.StatusUnknown {
		t.Fatalf("timeout: %+v", c)
	}
}

func TestDoctorAppReach(t *testing.T) {
	rig := doctorLiveRig(t)
	installDoctorApp(t, rig.st, "immich")
	set, _ := loadEdgeRoutes(rig.st)
	for i := range set.Routes {
		if set.Routes[i].Host == "immich.lan" {
			set.Routes[i].Tunnel = []docker.Hostname{"immich.homestead.example.com"}
		}
	}
	if err := saveEdgeRoutes(rig.st, set); err != nil {
		t.Fatal(err)
	}
	oldLocal, oldPublic := reachLocal, reachPublic
	t.Cleanup(func() { reachLocal, reachPublic = oldLocal, oldPublic })
	reachLocal = func(context.Context, string, string, bool, *x509.CertPool) (int, error) { return 200, nil }
	reachPublic = func(context.Context, string, string, bool, *x509.CertPool) (int, error) {
		return 0, errors.New("remote error: tls: handshake failure")
	}
	entries, _ := catalog.Load(rig.cfg.CatalogDir)
	m, _ := entries.Get("immich")
	checks := probeAppReach(context.Background(), rig.cfg, m)
	var publicFail, localOK bool
	for _, c := range checks {
		if strings.HasPrefix(c.Name, "app-reach:immich:") && strings.Contains(c.Title, "https://immich.homestead.example.com") {
			publicFail = c.Status == doctor.StatusFail && strings.Contains(c.Detail, "Advanced Certificate Manager") && c.Fix != nil
		}
		if strings.Contains(c.Title, "immich.lan") && c.Status != doctor.StatusFail {
			localOK = true
		}
	}
	if !publicFail || !localOK {
		t.Fatalf("checks = %+v", checks)
	}
	reachLocal = func(ctx context.Context, _ string, _ string, _ bool, _ *x509.CertPool) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if c := only(t, probeAppReach(ctx, rig.cfg, m)); c.Status != doctor.StatusUnknown {
		t.Fatalf("timeout: %+v", c)
	}
}

func TestDoctorAppSetUpNeedsNextcloud(t *testing.T) {
	entries, _ := catalog.Load(filepath.Join("..", "..", "catalog"))
	m, _ := entries.Get("immich")
	if got := probeAppSetUp(context.Background(), m); got != nil {
		t.Fatalf("immich cannot be detected: %+v", got)
	}
}

func TestDoctorLiveProbesCoverInstalledApps(t *testing.T) {
	rig := doctorLiveRig(t)
	installDoctorApp(t, rig.st, "immich")
	installDoctorApp(t, rig.st, "nextcloud")
	var names []string
	for _, p := range doctorLiveProbes(rig.cfg)() {
		names = append(names, p.Name)
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"tunnel-connector", "tunnel-exists", "tunnel-dns", "edge-certificate", "authentik",
		"app-containers:immich", "app-reach:immich", "app-containers:nextcloud", "app-setup:nextcloud"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing probe %s in %s", want, got)
		}
	}
	if strings.Contains(got, "app-setup:immich") {
		t.Errorf("immich has no first-run probe: %s", got)
	}
}

func TestDoctorEndpointLiveOnlyOnRequest(t *testing.T) {
	srv := New("127.0.0.1:0")
	srv.MountDoctor()
	var ran atomic.Int32
	srv.MountDoctorLive(func() []doctor.Probe {
		return []doctor.Probe{{Name: "tunnel-connector", Group: doctor.GroupTunnel, Run: func(context.Context) []doctor.Check {
			ran.Add(1)
			return []doctor.Check{{Name: "tunnel-connector", Status: doctor.StatusFail, Detail: "down"}}
		}}}
	}, time.Second)
	get := func(path string) doctor.Report {
		rec := serveRequest(t, srv, http.MethodGet, path, nil)
		var report doctor.Report
		if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	cheap := get("/api/v1/doctor")
	if ran.Load() != 0 || cheap.Live || cheap.CheckedAt != "" {
		t.Fatalf("cheap ran probes: %d %+v", ran.Load(), cheap)
	}
	for _, c := range cheap.Checks {
		if c.Group == "" || c.Title == "" || c.Fix == nil {
			t.Fatalf("not annotated: %+v", c)
		}
	}
	live := get("/api/v1/doctor?live=1")
	if ran.Load() != 1 || !live.Live || live.CheckedAt == "" || live.Overall != doctor.StatusFail {
		t.Fatalf("live: %d %+v", ran.Load(), live)
	}
	var found bool
	for _, c := range live.Checks {
		if c.Name == "tunnel-connector" {
			found = c.Live && c.Group == doctor.GroupTunnel && c.Fix != nil && c.Why != ""
		}
	}
	if !found {
		t.Fatalf("probe check missing or unannotated: %+v", live.Checks)
	}
}

func TestDoctorEndpointLiveBudget(t *testing.T) {
	srv := New("127.0.0.1:0")
	srv.MountDoctor()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	srv.MountDoctorLive(func() []doctor.Probe {
		return []doctor.Probe{{Name: "slow", Timeout: time.Minute, Run: func(context.Context) []doctor.Check { <-block; return nil }}}
	}, 100*time.Millisecond)
	start := time.Now()
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/doctor?live=1", nil)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	var report doctor.Report
	_ = json.Unmarshal(rec.Body.Bytes(), &report)
	var unknown bool
	for _, c := range report.Checks {
		if c.Name == "slow" {
			unknown = c.Status == doctor.StatusUnknown
		}
	}
	if !unknown {
		t.Fatalf("slow probe not unknown: %s", rec.Body.String())
	}
}

func TestDoctorNeverPrintsTheToken(t *testing.T) {
	rig := doctorLiveRig(t)
	delete(rig.cf.tunnels, "tid")
	var all []doctor.Check
	for _, p := range doctorLiveProbes(rig.cfg)() {
		if strings.HasPrefix(p.Name, "tunnel") || p.Name == "edge-certificate" {
			all = append(all, p.Run(context.Background())...)
		}
	}
	raw, _ := json.Marshal(all)
	if strings.Contains(string(raw), doctorSecret) || strings.Contains(string(raw), "Bearer") {
		t.Fatalf("secret in %s", raw)
	}
}
