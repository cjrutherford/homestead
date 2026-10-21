package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/secret"
	"homestead/internal/store"
)

func tunnelTestServer(t *testing.T) (*Server, *store.Store, *secret.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	secrets, err := secret.New(s, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	// Connector writes stay in temp dirs, and the router is never probed:
	// no test touches the host or the network.
	tunnelConnectorDir = filepath.Join(dir, "tunnel")
	stubDiscovery(t, new(int))
	t.Cleanup(func() {
		tunnelConnectorDir = "/etc/homestead/tunnel"
	})
	srv := New("127.0.0.1:0")
	srv.MountTunnel(s, secrets, 8080)
	return srv, s, secrets
}

func TestTunnelStatus(t *testing.T) {
	srv, _, _ := tunnelTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/tunnel/status", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var status struct {
		Preferred string         `json:"preferred"`
		Protocols []string       `json:"protocols"`
		Tunnels   []any          `json:"tunnels"`
		Routes    []any          `json:"routes"`
		Connector map[string]any `json:"connector"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.Preferred == "" || status.Protocols == nil || status.Tunnels == nil || status.Routes == nil {
		t.Fatalf("status = %+v", status)
	}
	// Nothing recorded reads as empty, and no credential means no
	// tunnel health — never an error, never a leak.
	if len(status.Tunnels) != 0 || len(status.Routes) != 0 {
		t.Fatalf("status = %+v", status)
	}
	if strings.Contains(recorder.Body.String(), "api_token") {
		t.Fatal("status leaks credential material")
	}
}

// stubDiscovery replaces the router probe with one that counts its calls,
// and empties the cache around the test.
func stubDiscovery(t *testing.T, calls *int) {
	t.Helper()
	prev := discoverGateway
	discoverGateway = func(context.Context) tunnelDiscovery {
		*calls++
		return tunnelDiscovery{Preferred: "tunnel", Protocols: []string{}, Gateway: "192.0.2.1", Interface: "eth0"}
	}
	discoveryCache.at = time.Time{}
	t.Cleanup(func() {
		discoverGateway = prev
		discoveryCache.at = time.Time{}
	})
}

// The router probe costs seconds, so status reuses it for a while.
func TestTunnelStatusCachesDiscovery(t *testing.T) {
	srv, _, _ := tunnelTestServer(t)
	calls := 0
	stubDiscovery(t, &calls)
	for range 3 {
		recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/tunnel/status", nil)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"gateway":"192.0.2.1"`) {
			t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("discovery ran %d times, want 1", calls)
	}
	discoveryCache.at = time.Now().Add(-tunnelDiscoveryTTL)
	serveRequest(t, srv, http.MethodGet, "/api/v1/tunnel/status", nil)
	if calls != 2 {
		t.Fatalf("discovery after expiry ran %d times, want 2", calls)
	}
}

// fakeCloudflare is a Cloudflare API double: zones, DNS records, accounts
// and tunnels, enough for plan, setup and teardown.
type fakeCloudflare struct {
	mu       sync.Mutex
	zones    map[string][2]string // name -> {id, account}
	records  map[string]map[string]any
	tunnels  map[string]string
	nextID   int
	requests []string
}

func newFakeCloudflare(t *testing.T) *fakeCloudflare {
	t.Helper()
	cf := &fakeCloudflare{
		zones:   map[string][2]string{"example.com": {"zone1", "abcd0000000000000000000000000001"}},
		records: map[string]map[string]any{},
		tunnels: map[string]string{},
	}
	server := httptest.NewServer(http.HandlerFunc(cf.serve))
	t.Cleanup(server.Close)
	prev := tunnelAPIBase
	tunnelAPIBase = server.URL
	t.Cleanup(func() { tunnelAPIBase = prev })
	return cf
}

func (cf *fakeCloudflare) addRecord(typ, name, content string) string {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	cf.nextID++
	id := fmt.Sprintf("rec%d", cf.nextID)
	cf.records[id] = map[string]any{"id": id, "type": typ, "name": name, "content": content, "proxied": false}
	return id
}

func (cf *fakeCloudflare) serve(w http.ResponseWriter, r *http.Request) {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	cf.requests = append(cf.requests, r.Method+" "+r.URL.Path)
	ok := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	}
	missing := func() {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 1003, "message": "not found"}}})
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/zones":
		out := []any{}
		if zone, found := cf.zones[r.URL.Query().Get("name")]; found {
			out = append(out, map[string]any{"id": zone[0], "name": r.URL.Query().Get("name"), "account": map[string]any{"id": zone[1]}})
		}
		ok(out)
	case len(parts) == 3 && parts[0] == "zones" && parts[2] == "dns_records" && r.Method == http.MethodGet:
		out := []any{}
		for _, record := range cf.records {
			if record["name"] == r.URL.Query().Get("name") {
				out = append(out, record)
			}
		}
		ok(out)
	case len(parts) == 3 && parts[0] == "zones" && parts[2] == "dns_records" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cf.nextID++
		id := fmt.Sprintf("rec%d", cf.nextID)
		body["id"] = id
		cf.records[id] = body
		ok(body)
	case len(parts) == 4 && parts[0] == "zones" && parts[2] == "dns_records":
		record, found := cf.records[parts[3]]
		if !found {
			missing()
			return
		}
		if r.Method == http.MethodDelete {
			delete(cf.records, parts[3])
			ok(map[string]any{"id": parts[3]})
			return
		}
		ok(record)
	case r.Method == http.MethodGet && r.URL.Path == "/accounts":
		ok([]any{map[string]any{"id": "abcd0000000000000000000000000001"}, map[string]any{"id": "abcd0000000000000000000000000002"}})
	case len(parts) == 3 && parts[0] == "accounts" && parts[2] == "tunnels" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cf.nextID++
		id := fmt.Sprintf("tunnel%d", cf.nextID)
		cf.tunnels[id] = fmt.Sprint(body["name"])
		ok(map[string]any{"id": id, "name": body["name"], "token": "run-token"})
	case len(parts) == 4 && parts[0] == "accounts" && parts[2] == "tunnels":
		name, found := cf.tunnels[parts[3]]
		if !found {
			missing()
			return
		}
		if r.Method == http.MethodDelete {
			delete(cf.tunnels, parts[3])
		}
		ok(map[string]any{"id": parts[3], "name": name, "status": "healthy"})
	default:
		missing()
	}
}

func waitOperation(t *testing.T, st *store.Store, body []byte) operation.Operation {
	t.Helper()
	var started struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &started); err != nil || started.ID == 0 {
		t.Fatalf("no operation id: %s", body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	op, err := operation.NewRunner(st).Wait(ctx, started.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return op
}

func fakeConnector(t *testing.T) {
	t.Helper()
	prevLook, prevCtl := tunnelLookPath, tunnelSystemctl
	tunnelLookPath = func(string) (string, error) { return "/usr/bin/cloudflared", nil }
	tunnelSystemctl = func(...string) error { return nil }
	t.Cleanup(func() { tunnelLookPath, tunnelSystemctl = prevLook, prevCtl })
}

// Teardown leaves a created record the owner has since repointed.
func TestTunnelTeardownKeepsChangedRecord(t *testing.T) {
	_, st, _ := tunnelTestServer(t)
	cf := newFakeCloudflare(t)
	id := cf.addRecord("CNAME", "app.example.com", "somewhere-else.example.net")
	if err := router.RecordOwned(st, router.OwnedRecord{RecordID: id, ZoneID: "zone1", Hostname: "app.example.com", TunnelID: "tunnel-id"}); err != nil {
		t.Fatal(err)
	}
	if err := deleteOwnedRecords(context.Background(), st, "tok", "tunnel-id"); err != nil {
		t.Fatal(err)
	}
	if cf.records[id] == nil {
		t.Fatal("teardown deleted a record that no longer points at the tunnel")
	}
	var kept int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM audit_entries WHERE action = 'tunnel.teardown.dns-kept'`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("kept audit = %d, %v", kept, err)
	}
}

// A replaced record is not put back over a record now at its name.
func TestTunnelRestoreSkipsOccupiedName(t *testing.T) {
	_, st, _ := tunnelTestServer(t)
	cf := newFakeCloudflare(t)
	cf.addRecord("A", "app.example.com", "198.51.100.9")
	if err := router.RecordReplaced(st, router.ReplacedRecord{TunnelID: "tunnel-id", ZoneID: "zone1",
		Record: router.DNSRecord{ID: "old", Type: "A", Name: "app.example.com", Content: "203.0.113.7"}}); err != nil {
		t.Fatal(err)
	}
	if err := restoreReplacedRecords(context.Background(), st, "tok", "tunnel-id"); err != nil {
		t.Fatal(err)
	}
	if len(cf.records) != 1 {
		t.Fatalf("records = %v", cf.records)
	}
	var skipped int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM audit_entries WHERE action = 'tunnel.teardown.dns-not-restored'`).Scan(&skipped); err != nil || skipped != 1 {
		t.Fatalf("skipped audit = %d, %v", skipped, err)
	}
	if replaced, _ := router.ReplacedRecords(st, "tunnel-id"); len(replaced) != 0 {
		t.Fatalf("replaced = %v", replaced)
	}
}

func TestTunnelTeardownRefusals(t *testing.T) {
	srv, _, secrets := tunnelTestServer(t)
	newFakeCloudflare(t)
	// No credential on file refuses before running anything.
	if recorder := serveRequest(t, srv, http.MethodDelete, "/api/v1/tunnel/tunnels/tunnel-id", nil); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, want 422", recorder.Code)
	}
	// The legacy token still works, but with two accounts and no stored
	// route the account cannot be guessed.
	if err := secrets.Set("router_tunnel_api_token", "sealed-token"); err != nil {
		t.Fatal(err)
	}
	recorder := serveRequest(t, srv, http.MethodDelete, "/api/v1/tunnel/tunnels/tunnel-id", nil)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "2 accounts") {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
}

// Teardown stops the connector before it deletes the provider tunnel: the
// provider refuses to delete a tunnel with live connections (KVM, S287).
func TestTunnelTeardownStopsConnectorFirst(t *testing.T) {
	var calls []string
	prev := tunnelSystemctl
	tunnelSystemctl = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { tunnelSystemctl = prev })
	def := tunnelTeardownDefinition(nil, "tok", "acct", "tunnel-id")
	var names []string
	for _, step := range def.Steps {
		names = append(names, step.Name)
	}
	if got := strings.Join(names, ","); got != "stop-connector,delete-tunnel,delete-dns,restore-dns,remove-connector,edge,forget-routes" {
		t.Fatalf("steps = %s", got)
	}
	if err := def.Steps[0].Run(context.Background()); err != nil {
		t.Fatalf("stop-connector: %v", err)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "stop ") {
		t.Fatalf("systemctl calls = %v, want one stop", calls)
	}
}

// fakeEdge records tunnel names on the edge routes without Docker.
func fakeEdge(t *testing.T) {
	t.Helper()
	prev := applyTunnelNames
	applyTunnelNames = func(_ context.Context, st *store.Store, names map[docker.Hostname][]docker.Hostname) error {
		set, err := loadEdgeRoutes(st)
		if err != nil {
			return err
		}
		for i := range set.Routes {
			set.Routes[i].Tunnel = names[set.Routes[i].Host]
		}
		if err := set.Validate(); err != nil {
			return err
		}
		return saveEdgeRoutes(st, set)
	}
	t.Cleanup(func() { applyTunnelNames = prev })
}

// seedTunnelEdge records the dashboard route and, when authentik is
// set, the Authentik route; domain and linked feed the plan.
func seedTunnelEdge(t *testing.T, srv *Server, st *store.Store, authentik, linked bool) {
	t.Helper()
	dash, err := BuildDashboardRoute(dashLAN, "")
	if err != nil {
		t.Fatal(err)
	}
	routes := []docker.Route{dash}
	if authentik {
		routes = append(routes, docker.Route{Host: "authentik.lan", Target: docker.Target{Host: "server", Port: 9000},
			Exposure: docker.ExposureLocal, TLS: docker.TLSInternal})
	}
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: routes}); err != nil {
		t.Fatal(err)
	}
	srv.setupDomain = func() string { return "example.com" }
	srv.signInOffered = func() bool { return linked }
}

type tunnelPlanView struct {
	DashboardHostname string `json:"dashboard_hostname"`
	AuthHostname      string `json:"auth_hostname"`
	Zone              string `json:"zone"`
	Ingress           []struct{ Hostname, Role, Route, Service string }
	DNS               []struct {
		Name, Type, Content, Action string
		Proxied                     bool
		Replaces                    []struct{ Type, Content string }
	} `json:"dns"`
	Omitted  []struct{ Hostname, Role, Reason string }
	SignIn   string   `json:"sign_in"`
	Warnings []string `json:"warnings"`
	Digest   string   `json:"digest"`
}

func planTunnel(t *testing.T, srv *Server, body map[string]any) tunnelPlanView {
	t.Helper()
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan: code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var plan tunnelPlanView
	if err := json.Unmarshal(recorder.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestTunnelPlanRefusals(t *testing.T) {
	srv, st, secrets := tunnelTestServer(t)
	newFakeCloudflare(t)
	valid := map[string]any{"name": "homestead"}
	// Malformed bodies refuse, and the retired fields are named.
	for _, body := range []any{
		nil,
		map[string]any{"name": "Bad Name"},
		map[string]any{"name": "homestead", "dashboard_hostname": "not a host", "auth_hostname": "auth.example.com"},
		map[string]any{"name": "homestead", "dashboard_hostname": "same.example.com", "auth_hostname": "same.example.com"},
		map[string]any{"name": "homestead", "dashboard_hostname": "homestead.lan", "auth_hostname": "auth.example.com"},
		map[string]any{"name": "homestead", "hostname": "app.example.com", "service": "http://127.0.0.1:3000"},
		map[string]any{"name": "homestead", "api_token": "t"},
		map[string]any{"name": "homestead", "account_id": "a"},
		map[string]any{"name": "homestead", "digest": "d"},
	} {
		if recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", body); recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %+v: code = %d, want 400 (%s)", body, recorder.Code, recorder.Body.String())
		}
	}
	// No domain and no names point at the Domain step.
	if recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", valid); recorder.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(recorder.Body.String(), "Domain step") {
		t.Fatalf("no domain: code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	// Without Authentik there is nothing to publish, with the reasons.
	seedTunnelEdge(t, srv, st, false, false)
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", valid)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "Authentik is not installed") {
		t.Fatalf("no authentik: code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	seedTunnelEdge(t, srv, st, true, true)
	// No token on file points at the Domain step.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", valid)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "Domain step") {
		t.Fatalf("code = %d (%s), want 422 naming the Domain step", recorder.Code, recorder.Body.String())
	}
	if err := secrets.Set("dns_cloudflare_api_token", "sealed-token"); err != nil {
		t.Fatal(err)
	}
	// A name in no visible zone refuses.
	other := map[string]any{"name": "homestead", "auth_hostname": "auth.example.org"}
	if recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", other); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no zone: code = %d", recorder.Code)
	}
	// A name the edge already serves refuses.
	clash := map[string]any{"name": "homestead", "auth_hostname": dashLAN}
	if recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", clash); recorder.Code == http.StatusOK {
		t.Fatalf("clash planned: %s", recorder.Body.String())
	}
}

// Until sign-in with Authentik is linked the dashboard is left out, with
// the reason, and only Authentik is published (ADR-037 §4).
func TestTunnelPlanOmitsDashboardUntilLinked(t *testing.T) {
	srv, st, secrets := tunnelTestServer(t)
	newFakeCloudflare(t)
	if err := secrets.Set("dns_cloudflare_api_token", "sealed-token"); err != nil {
		t.Fatal(err)
	}
	seedTunnelEdge(t, srv, st, true, false)
	plan := planTunnel(t, srv, map[string]any{"name": "homestead"})
	if plan.DashboardHostname != "homestead.example.com" || plan.AuthHostname != "auth.example.com" {
		t.Fatalf("defaults = %+v", plan)
	}
	if len(plan.Ingress) != 1 || plan.Ingress[0].Hostname != "auth.example.com" || plan.Ingress[0].Route != "authentik.lan" ||
		plan.Ingress[0].Service != "http://127.0.0.1:8480" {
		t.Fatalf("ingress = %+v", plan.Ingress)
	}
	if len(plan.Omitted) != 1 || plan.Omitted[0].Hostname != "homestead.example.com" || !strings.Contains(plan.Omitted[0].Reason, "not linked") {
		t.Fatalf("omitted = %+v", plan.Omitted)
	}
	if plan.SignIn != "" || len(plan.DNS) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	// The status proposes the same names.
	var status struct{ Proposed tunnelProposal }
	if err := json.Unmarshal(serveRequest(t, srv, http.MethodGet, "/api/v1/tunnel/status", nil).Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Proposed.DashboardHostname != "homestead.example.com" || status.Proposed.SignInLinked {
		t.Fatalf("proposed = %+v", status.Proposed)
	}
}

func TestTunnelPlanSetupTeardown(t *testing.T) {
	srv, st, secrets := tunnelTestServer(t)
	cf := newFakeCloudflare(t)
	fakeConnector(t)
	fakeEdge(t)
	if err := secrets.Set("dns_cloudflare_api_token", "sealed-token"); err != nil {
		t.Fatal(err)
	}
	seedTunnelEdge(t, srv, st, true, true)
	oldA := cf.addRecord("A", "dash.example.com", "203.0.113.7")
	cf.records[oldA]["comment"] = "home router"
	// The owner renames the dashboard; Authentik keeps its default.
	request := map[string]any{"name": "homestead", "dashboard_hostname": "dash.example.com"}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", request)
	raw := recorder.Body.String()
	for _, hidden := range []string{"abcd0000000000000000000000000001", "zone1", oldA, "sealed-token"} {
		if strings.Contains(raw, hidden) {
			t.Fatalf("plan leaks %q: %s", hidden, raw)
		}
	}
	plan := planTunnel(t, srv, request)
	if plan.Zone != "example.com" || len(plan.Ingress) != 2 || plan.Ingress[0].Role != "dashboard" || plan.Ingress[0].Route != dashLAN ||
		plan.Ingress[1].Hostname != "auth.example.com" || len(plan.Omitted) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.DNS) != 2 || plan.DNS[0].Action != "replace" || plan.DNS[0].Replaces[0].Content != "203.0.113.7" ||
		plan.DNS[1].Action != "create" || !plan.DNS[1].Proxied {
		t.Fatalf("dns = %+v", plan.DNS)
	}
	if !strings.Contains(plan.SignIn, "re-plan Sign-in") || len(plan.Warnings) != 1 || plan.Digest == "" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(cf.records) != 1 || len(cf.tunnels) != 0 {
		t.Fatal("plan changed Cloudflare")
	}
	// Setup without a digest, or with a stale one, refuses.
	if recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", request); recorder.Code != http.StatusBadRequest {
		t.Fatalf("no digest: code = %d", recorder.Code)
	}
	cf.addRecord("AAAA", "dash.example.com", "2001:db8::7")
	approve := map[string]any{"name": "homestead", "dashboard_hostname": "dash.example.com", "digest": plan.Digest}
	if recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", approve); recorder.Code != http.StatusConflict {
		t.Fatalf("changed plan: code = %d, want 409", recorder.Code)
	}
	approve["digest"] = planTunnel(t, srv, request).Digest
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", approve)
	if recorder.Code != http.StatusOK {
		t.Fatalf("setup: code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if op := waitOperation(t, st, recorder.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("setup op = %s: %s", op.Status, op.Error)
	}
	// The replaced records are gone; a proxied CNAME per name points at
	// the tunnel.
	var tunnelID string
	for id := range cf.tunnels {
		tunnelID = id
	}
	if len(cf.records) != 2 || tunnelID == "" {
		t.Fatalf("records = %v tunnels = %v", cf.records, cf.tunnels)
	}
	for _, record := range cf.records {
		if record["type"] != "CNAME" || record["content"] != tunnelID+".cfargotunnel.com" || record["proxied"] != true {
			t.Fatalf("record = %v", record)
		}
	}
	// The edge routes carry their public names; the connector delivers
	// both to the loopback tunnel entry.
	set, _ := loadEdgeRoutes(st)
	got := map[docker.Hostname]string{}
	for _, r := range set.Routes {
		got[r.Host] = fmt.Sprint(r.Tunnel)
	}
	if got[dashLAN] != "[dash.example.com]" || got["authentik.lan"] != "[auth.example.com]" {
		t.Fatalf("edge tunnel names = %v", got)
	}
	config, err := os.ReadFile(filepath.Join(tunnelConnectorDir, "config.yml"))
	if err != nil || !strings.Contains(string(config), "hostname: dash.example.com\n    service: http://127.0.0.1:8480") ||
		!strings.Contains(string(config), "hostname: auth.example.com\n    service: http://127.0.0.1:8480") {
		t.Fatalf("connector config = %s (%v)", config, err)
	}
	if routes, _ := router.ListRoutes(st); len(routes) != 2 {
		t.Fatalf("routes = %v", routes)
	}
	if account, err := router.TunnelAccount(st, tunnelID); err != nil || account != "abcd0000000000000000000000000001" {
		t.Fatalf("stored account = %q, %v", account, err)
	}
	// The approval is audited without the account.
	var audited int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM audit_entries WHERE action = 'tunnel.setup.start' AND detail NOT LIKE '%abcd0000%' AND detail LIKE '%dash.example.com -> homestead.lan%'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit = %d, %v", audited, err)
	}
	// A second plan refuses while the tunnel is set up.
	if recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/plan", request); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second plan: code = %d", recorder.Code)
	}
	// A record the owner added by hand at another name is never touched,
	// and teardown takes no account.
	foreign := cf.addRecord("CNAME", "www.example.com", "elsewhere.example.net")
	if recorder := serveRequest(t, srv, http.MethodDelete, "/api/v1/tunnel/tunnels/"+tunnelID+"?account_id=x", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("account_id teardown: code = %d", recorder.Code)
	}
	recorder = serveRequest(t, srv, http.MethodDelete, "/api/v1/tunnel/tunnels/"+tunnelID, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("teardown: code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if op := waitOperation(t, st, recorder.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("teardown op = %s: %s", op.Status, op.Error)
	}
	// The CNAMEs are gone and the A and AAAA records setup replaced are back.
	if len(cf.tunnels) != 0 || len(cf.records) != 3 || cf.records[foreign] == nil {
		t.Fatalf("after teardown records = %v tunnels = %v", cf.records, cf.tunnels)
	}
	restored, comments := map[string]string{}, map[string]string{}
	for _, record := range cf.records {
		if record["name"] == "dash.example.com" {
			restored[fmt.Sprint(record["type"])] = fmt.Sprint(record["content"])
			comments[fmt.Sprint(record["type"])] = fmt.Sprint(record["comment"])
		}
	}
	if restored["A"] != "203.0.113.7" || restored["AAAA"] != "2001:db8::7" {
		t.Fatalf("restored = %v", restored)
	}
	// The owner's note comes back; a record without one is marked.
	if comments["A"] != "home router" || !strings.Contains(comments["AAAA"], "Restored by Homestead") {
		t.Fatalf("comments = %v", comments)
	}
	// The edge forgets the public names.
	set, _ = loadEdgeRoutes(st)
	for _, r := range set.Routes {
		if len(r.Tunnel) != 0 {
			t.Fatalf("%s keeps tunnel names %v", r.Host, r.Tunnel)
		}
	}
	if replaced, _ := router.ReplacedRecords(st, tunnelID); len(replaced) != 0 {
		t.Fatalf("replaced = %v", replaced)
	}
	if routes, _ := router.ListRoutes(st); len(routes) != 0 {
		t.Fatalf("routes = %v", routes)
	}
	if owned, _ := router.OwnedRecords(st, tunnelID); len(owned) != 0 {
		t.Fatalf("owned = %v", owned)
	}
}

// A setup that fails after replacing records puts them back, deletes the
// tunnel and its CNAMEs, and drops the edge's public names.
func TestTunnelSetupFailureRestoresRecords(t *testing.T) {
	srv, st, secrets := tunnelTestServer(t)
	cf := newFakeCloudflare(t)
	fakeConnector(t)
	fakeEdge(t)
	tunnelLookPath = func(string) (string, error) { return "", errors.New("not found") }
	if err := secrets.Set("dns_cloudflare_api_token", "sealed-token"); err != nil {
		t.Fatal(err)
	}
	seedTunnelEdge(t, srv, st, true, true)
	cf.addRecord("A", "homestead.example.com", "203.0.113.7")
	request := map[string]any{"name": "homestead", "digest": planTunnel(t, srv, map[string]any{"name": "homestead"}).Digest}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/tunnel/setup", request)
	if op := waitOperation(t, st, recorder.Body.Bytes()); op.Status != operation.StatusFailed {
		t.Fatalf("setup op = %s", op.Status)
	}
	if len(cf.tunnels) != 0 || len(cf.records) != 1 {
		t.Fatalf("records = %v tunnels = %v", cf.records, cf.tunnels)
	}
	for _, record := range cf.records {
		if record["type"] != "A" || record["content"] != "203.0.113.7" {
			t.Fatalf("record = %v", record)
		}
	}
	set, _ := loadEdgeRoutes(st)
	for _, r := range set.Routes {
		if len(r.Tunnel) != 0 {
			t.Fatalf("%s keeps tunnel names %v", r.Host, r.Tunnel)
		}
	}
}
