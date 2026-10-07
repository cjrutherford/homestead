package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/dns"
	"homestead/internal/operation"
)

func validName() FriendlyName {
	return FriendlyName{
		Route: Route{
			Host:     "app.lan",
			Target:   Target{Host: "app", Port: 8080},
			Exposure: ExposureLocal,
			TLS:      TLSInternal,
			Health:   HealthCheck{Path: "/healthz", IntervalSeconds: 30},
		},
		Answer: netip.MustParseAddr("192.168.1.10"),
	}
}

func TestFriendlyNameValidation(t *testing.T) {
	if err := validName().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rw := validName().Rewrite(); rw.Domain != "app.lan" || rw.Answer != "192.168.1.10" {
		t.Fatalf("Rewrite = %+v", rw)
	}
	cases := []struct {
		name   string
		mutate func(*FriendlyName)
		want   string
	}{
		{"bad host", func(n *FriendlyName) { n.Route.Host = "App.lan" }, "lowercase"},
		{"public exposure", func(n *FriendlyName) { n.Route.Exposure = ExposurePublic }, "must be local"},
		{"overlay exposure", func(n *FriendlyName) { n.Route.Exposure = ExposureOverlay }, "must be local"},
		{"unspecified answer", func(n *FriendlyName) { n.Answer = netip.Addr{} }, "usable IP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := validName()
			tc.mutate(&n)
			err := n.Validate()
			if err == nil {
				t.Fatal("accepted invalid name")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func fakeLookup(addrs []netip.Addr, err error) LookupFunc {
	return func(_ context.Context, _ string) ([]netip.Addr, error) {
		return addrs, err
	}
}

func TestVerifyName(t *testing.T) {
	name := validName()
	ctx := context.Background()
	if err := VerifyName(ctx, fakeLookup([]netip.Addr{name.Answer}, nil), name); err != nil {
		t.Fatalf("match: %v", err)
	}
	other := netip.MustParseAddr("192.168.1.99")
	if err := VerifyName(ctx, fakeLookup([]netip.Addr{other}, nil), name); err == nil ||
		!strings.Contains(err.Error(), "resolves to") {
		t.Fatalf("mismatch err = %v", err)
	}
	if err := VerifyName(ctx, fakeLookup(nil, errors.New("no dns")), name); err == nil {
		t.Fatal("accepted lookup error")
	}
	bad := name
	bad.Route.Exposure = ExposurePublic
	if err := VerifyName(ctx, fakeLookup(nil, nil), bad); err == nil {
		t.Fatal("verified invalid name")
	}
}

func TestLookupViaUnreachable(t *testing.T) {
	lookup := LookupVia("127.0.0.1:1")
	if _, err := lookup(context.Background(), "app.lan"); err == nil {
		t.Fatal("resolved via closed port")
	}
}

// serveNameDNS doubles the AdGuard rewrite endpoint, recording entries.
func serveNameDNS(t *testing.T) (*dns.Client, *[]dns.Rewrite) {
	t.Helper()
	var stored []dns.Rewrite
	mux := http.NewServeMux()
	mux.HandleFunc("/control/rewrite/add", func(w http.ResponseWriter, r *http.Request) {
		var entry dns.Rewrite
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			t.Fatalf("decode: %v", err)
		}
		stored = append(stored, entry)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client, err := dns.NewAdGuard(srv.URL, "owner", "secret")
	if err != nil {
		t.Fatalf("NewAdGuard: %v", err)
	}
	return client, &stored
}

func stubNames(t *testing.T, reloadFails bool) *[]string {
	t.Helper()
	var calls []string
	old := execRun
	execRun = func(_ context.Context, name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		calls = append(calls, joined)
		if strings.Contains(joined, "caddy reload") && reloadFails {
			return "", errors.New("exit status 1: invalid config")
		}
		if strings.Contains(joined, "ps --format json") {
			return "{\"Service\":\"caddy\",\"State\":\"running\"}\n", nil
		}
		return "", nil
	}
	t.Cleanup(func() { execRun = old })
	return &calls
}

func TestWriteNameSnippet(t *testing.T) {
	dir := t.TempDir()
	if err := WriteNameSnippet(dir, validName()); err != nil {
		t.Fatalf("WriteNameSnippet: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "routes", "app.lan.caddy"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "reverse_proxy app:8080") {
		t.Fatalf("snippet wrong:\n%s", content)
	}
	bad := validName()
	bad.Route.Exposure = ExposurePublic
	if err := WriteNameSnippet(dir, bad); err == nil {
		t.Fatal("wrote public name")
	}
}

func TestEnsureNameFlow(t *testing.T) {
	calls := stubNames(t, false)
	runner := openCaddyRunner(t)
	stack := caddyStackIn(t)
	client, stored := serveNameDNS(t)
	name := validName()
	lookup := fakeLookup([]netip.Addr{name.Answer}, nil)
	def, err := EnsureNameDefinition(stack, client, lookup, name)
	if err != nil {
		t.Fatalf("EnsureNameDefinition: %v", err)
	}
	if def.Type != "names.ensure" || len(def.Steps) != 4 {
		t.Fatalf("def = %+v", def)
	}
	for i, want := range []string{"write-snippet", "add-rewrite", "reload", "verify-name"} {
		if def.Steps[i].Name != want {
			t.Fatalf("step %d = %q, want %q", i, def.Steps[i].Name, want)
		}
	}
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	if len(*stored) != 1 || (*stored)[0] != name.Rewrite() {
		t.Fatalf("rewrites = %+v", *stored)
	}
	if _, err := os.Stat(filepath.Join(stack.Dir, "routes", "app.lan.caddy")); err != nil {
		t.Fatalf("snippet missing: %v", err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "caddy reload") {
		t.Fatalf("calls = %v", *calls)
	}
}

func TestEnsureNameValidates(t *testing.T) {
	client, _ := serveNameDNS(t)
	lookup := fakeLookup(nil, nil)
	stack := caddyStackIn(t)
	bad := Stack{Name: "other", Dir: stack.Dir, Enabled: true}
	if _, err := EnsureNameDefinition(bad, client, lookup, validName()); err == nil {
		t.Fatal("non-caddy stack accepted")
	}
	if _, err := EnsureNameDefinition(stack, nil, lookup, validName()); err == nil {
		t.Fatal("nil client accepted")
	}
	public := validName()
	public.Route.Exposure = ExposurePublic
	if _, err := EnsureNameDefinition(stack, client, lookup, public); err == nil {
		t.Fatal("public name accepted")
	}
}

func TestEnsureReloadFailureAborts(t *testing.T) {
	stubNames(t, true)
	runner := openCaddyRunner(t)
	stack := caddyStackIn(t)
	client, _ := serveNameDNS(t)
	name := validName()
	def, err := EnsureNameDefinition(stack, client, fakeLookup([]netip.Addr{name.Answer}, nil), name)
	if err != nil {
		t.Fatalf("EnsureNameDefinition: %v", err)
	}
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
}
