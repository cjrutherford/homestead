package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
)

func routeFor(host string, tls TLSMode, exposure Exposure) Route {
	return Route{
		Host:     Hostname(host),
		Target:   Target{Host: "app", Port: 8080},
		Exposure: exposure,
		TLS:      tls,
		Health:   HealthCheck{Path: "/healthz", IntervalSeconds: 30},
	}
}

func TestRenderModes(t *testing.T) {
	internal, err := RenderRoute(routeFor("app.lan", TLSInternal, ExposureLocal))
	if err != nil {
		t.Fatalf("internal: %v", err)
	}
	for _, want := range []string{
		"app.lan, app.local {", "tls internal", "reverse_proxy app:8080 {",
		"health_uri /healthz", "health_interval 30s",
		"# Exposure: local", "Do not edit",
	} {
		if !strings.Contains(internal, want) {
			t.Fatalf("internal missing %q:\n%s", want, internal)
		}
	}

	auto, err := RenderRoute(routeFor("app.example.com", TLSAuto, ExposurePublic))
	if err != nil {
		t.Fatalf("auto: %v", err)
	}
	if strings.Contains(auto, "tls internal") || strings.Contains(auto, "http://") {
		t.Fatalf("auto over-pinned:\n%s", auto)
	}
	if !strings.Contains(auto, "app.example.com {") {
		t.Fatalf("auto site missing:\n%s", auto)
	}

	off := routeFor("plain.lan", TLSOff, ExposureLocal)
	off.Health = HealthCheck{}
	plain, err := RenderRoute(off)
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	if !strings.Contains(plain, "http://plain.lan, http://plain.local {") {
		t.Fatalf("off site missing:\n%s", plain)
	}
	if strings.Contains(plain, "health_uri") || strings.Contains(plain, "tls") {
		t.Fatalf("off over-rendered:\n%s", plain)
	}
	if !strings.Contains(plain, "\treverse_proxy app:8080\n") {
		t.Fatalf("off upstream missing:\n%s", plain)
	}

	bad := routeFor("App.lan", TLSAuto, ExposureLocal)
	if _, err := RenderRoute(bad); err == nil {
		t.Fatal("rendered invalid route")
	}
}

func TestReconcileWritesAndCleans(t *testing.T) {
	dir := t.TempDir()
	routesDir := filepath.Join(dir, "routes")
	set := RouteSet{Routes: []Route{
		routeFor("wiki.lan", TLSInternal, ExposureLocal),
		routeFor("app.lan", TLSInternal, ExposureOverlay),
	}}
	if _, err := ReconcileRoutes(dir, set, RouteSet{}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, name := range []string{"app.lan.caddy", "wiki.lan.caddy"} {
		content, err := os.ReadFile(filepath.Join(routesDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(content), "reverse_proxy app:8080") {
			t.Fatalf("%s missing upstream", name)
		}
	}
	// Hand-placed files survive; stale snippets drop.
	keep := filepath.Join(routesDir, "notes.txt")
	if err := os.WriteFile(keep, []byte("owner"), 0o644); err != nil {
		t.Fatal(err)
	}
	narrow := RouteSet{Routes: []Route{routeFor("app.lan", TLSInternal, ExposureOverlay)}}
	if _, err := ReconcileRoutes(dir, narrow, set); err != nil {
		t.Fatalf("narrow: %v", err)
	}
	if _, err := os.Stat(filepath.Join(routesDir, "wiki.lan.caddy")); !os.IsNotExist(err) {
		t.Fatal("stale snippet survived")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("hand-placed removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(routesDir, "app.lan.caddy")); err != nil {
		t.Fatalf("desired missing: %v", err)
	}
	// Invalid desired state refuses before touching disk.
	bad := RouteSet{Routes: []Route{routeFor("App.lan", TLSAuto, ExposureLocal)}}
	if _, err := ReconcileRoutes(dir, bad, narrow); err == nil {
		t.Fatal("reconciled invalid set")
	}
}

func stubRoutes(t *testing.T, reloadFails bool) *[]string {
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

func routesStackIn(t *testing.T) Stack {
	t.Helper()
	stack := CaddyStack()
	stack.Dir = t.TempDir()
	if err := WriteStack(stack.Dir); err != nil {
		t.Fatalf("WriteStack: %v", err)
	}
	return stack
}

func routesSet() RouteSet {
	return RouteSet{Routes: []Route{routeFor("app.lan", TLSInternal, ExposureLocal)}}
}

func TestReconcileDefinitionFlow(t *testing.T) {
	calls := stubRoutes(t, false)
	runner := openCaddyRunner(t)
	stack := routesStackIn(t)
	def, err := ReconcileDefinition(stack, routesSet(), RouteState{})
	if err != nil {
		t.Fatalf("ReconcileDefinition: %v", err)
	}
	if def.Type != "caddy.reconcile" || len(def.Steps) != 3 {
		t.Fatalf("def = %+v", def)
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
	joined := strings.Join(*calls, "\n")
	for _, want := range []string{"exec " + CaddyContainerName + " caddy reload", "ps --format json"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("calls = %v, want %q", *calls, want)
		}
	}
	if !strings.Contains(joined, "caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile --force") {
		t.Fatalf("reload must be forced so rewritten certificate files are re-read: %v", *calls)
	}
	snippet, err := os.ReadFile(filepath.Join(stack.Dir, "routes", "app.lan.caddy"))
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}
	if !strings.Contains(string(snippet), "app.lan, app.local {") {
		t.Fatalf("snippet wrong:\n%s", snippet)
	}
}

func TestReconcileDefinitionValidates(t *testing.T) {
	stack := routesStackIn(t)
	bad := Stack{Name: "other", Dir: stack.Dir, Enabled: true}
	if _, err := ReconcileDefinition(bad, routesSet(), RouteState{}); err == nil {
		t.Fatal("non-caddy stack accepted")
	}
	invalid := RouteSet{Routes: []Route{routeFor("App.lan", TLSAuto, ExposureLocal)}}
	if _, err := ReconcileDefinition(stack, invalid, RouteState{}); err == nil {
		t.Fatal("invalid set accepted")
	}
}

func TestReconcileReloadFailureAborts(t *testing.T) {
	stubRoutes(t, true)
	runner := openCaddyRunner(t)
	stack := routesStackIn(t)
	def, err := ReconcileDefinition(stack, routesSet(), RouteState{})
	if err != nil {
		t.Fatalf("ReconcileDefinition: %v", err)
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
	// The refused reload rolled the files back: disk matches the
	// running config.
	if _, err := os.Stat(filepath.Join(stack.Dir, "routes", "app.lan.caddy")); !os.IsNotExist(err) {
		t.Fatalf("snippet survived refused reload: %v", err)
	}
}

func TestReconcileOnlyRemovesOwnedAndRollsBackOnReloadFailure(t *testing.T) {
	stubRoutes(t, true)
	runner := openCaddyRunner(t)
	stack := routesStackIn(t)
	routesDir := filepath.Join(stack.Dir, "routes")
	exposure := filepath.Join(routesDir, "pub.example.com.caddy")
	owned := filepath.Join(routesDir, "old.lan.caddy")
	if err := os.WriteFile(exposure, []byte("exposure-owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(owned, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	prior := RouteSet{Routes: []Route{routeFor("old.lan", TLSInternal, ExposureLocal)}}
	saved := prior
	state := RouteState{
		Load: func() (RouteSet, error) { return saved, nil },
		Save: func(s RouteSet) error { saved = s; return nil },
	}
	def, err := ReconcileDefinition(stack, routesSet(), state)
	if err != nil {
		t.Fatal(err)
	}
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatal(err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q", done.Status)
	}
	if b, err := os.ReadFile(owned); err != nil || string(b) != "previous" {
		t.Fatalf("stale file not restored: %q %v", b, err)
	}
	if _, err := os.Stat(exposure); err != nil {
		t.Fatalf("exposure-owned file touched: %v", err)
	}
	if len(saved.Routes) != 1 || saved.Routes[0].Host != "old.lan" {
		t.Fatalf("record not restored: %+v", saved)
	}
}

func TestReconcileSuccessKeepsExposureOwned(t *testing.T) {
	stubRoutes(t, false)
	runner := openCaddyRunner(t)
	stack := routesStackIn(t)
	routesDir := filepath.Join(stack.Dir, "routes")
	exposure := filepath.Join(routesDir, "pub.example.com.caddy")
	owned := filepath.Join(routesDir, "old.lan.caddy")
	for _, p := range []string{exposure, owned} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	saved := RouteSet{Routes: []Route{routeFor("old.lan", TLSInternal, ExposureLocal)}}
	state := RouteState{
		Load: func() (RouteSet, error) { return saved, nil },
		Save: func(s RouteSet) error { saved = s; return nil },
	}
	def, _ := ReconcileDefinition(stack, routesSet(), state)
	op, _ := runner.Start(context.Background(), def, "")
	done, _ := runner.Wait(context.Background(), op.ID)
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q", done.Status)
	}
	if _, err := os.Stat(exposure); err != nil {
		t.Fatalf("exposure-owned snippet removed: %v", err)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatal("owned stale snippet survived")
	}
	if len(saved.Routes) != 1 || saved.Routes[0].Host != "app.lan" {
		t.Fatalf("record = %+v", saved)
	}
}

func TestReconcileRefusesPrivateAutoUpFront(t *testing.T) {
	stack := routesStackIn(t)
	set := RouteSet{Routes: []Route{routeFor("app.lan", TLSAuto, ExposureLocal)}}
	if _, err := ReconcileDefinition(stack, set, RouteState{}); err == nil {
		t.Fatal("private auto accepted")
	}
}
