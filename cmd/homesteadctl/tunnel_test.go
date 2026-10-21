package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tunnelStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tunnel/status":
			_, _ = w.Write([]byte(`{"preferred":"tunnel","gateway":"","interface":"","protocols":[],
				"tunnels":[{"id":"tunnel-id","name":"homestead","status":"healthy","connections":[]}],
				"routes":[{"Hostname":"app.example.com","TunnelID":"tunnel-id","Service":"http://127.0.0.1:8080"}],
				"connector":{"installed":true,"active":true},"accounts":1,
				"proposed":{"dashboard_hostname":"homestead.example.com","auth_hostname":"auth.example.com","sign_in_linked":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tunnel/plan":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			dash, _ := body["dashboard_hostname"].(string)
			if dash == "" {
				dash = "homestead.example.com"
			}
			if body["adopt"] == "0a1b2c3d" {
				_, _ = w.Write([]byte(`{"name":"homestead-vm2","zone":"example.com","ingress":[],"dns":[],"omitted":[],"warnings":[],
					"adopt":{"id":"0a1b2c3d","status":"healthy","connections":2,"existing":[{"hostname":"old.example.com","service":"http://localhost:8096","outcome":"Dropped from the connector."}]},
					"digest":"a1g"}`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"homestead","dashboard_hostname":"` + dash + `","auth_hostname":"auth.example.com",
				"zone":"example.com","ingress":[{"hostname":"auth.example.com","role":"authentik","route":"authentik.lan","service":"http://127.0.0.1:8480"}],
				"dns":[{"name":"auth.example.com","type":"CNAME","content":"<new tunnel id>.cfargotunnel.com","proxied":true,
				"action":"replace","replaces":[{"type":"A","name":"auth.example.com","content":"203.0.113.7","proxied":false}]}],
				"omitted":[{"hostname":"` + dash + `","role":"dashboard","reason":"Sign-in with Authentik is not linked yet."}],
				"warnings":["Existing records are put back at teardown."],"digest":"d1g"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tunnel/setup":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["digest"] != "d1g" || body["api_token"] != nil || body["hostname"] != nil || body["dashboard_hostname"] != "dash.example.com" {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"id":23}`))
		case r.Method == http.MethodDelete && r.URL.RawQuery == "":
			_, _ = w.Write([]byte(`{"id":24}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestTunnelStatus(t *testing.T) {
	url := tunnelStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "tunnel", "status"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"preferred: tunnel", "connector: installed=true active=true",
		"tunnel-id homestead: healthy", "app.example.com -> http://127.0.0.1:8080 via tunnel-id"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestTunnelPlan(t *testing.T) {
	url := tunnelStub(t)
	code, stdout, stderr := invoke(t, []string{"--server", url, "tunnel", "plan",
		"--name", "homestead", "--dashboard-hostname", "dash.example.com"})
	if code != exitOK {
		t.Fatalf("exit = %d stderr = %q", code, stderr)
	}
	for _, want := range []string{"tunnel: homestead (zone example.com)",
		"publish authentik: auth.example.com -> authentik.lan (edge tunnel entry http://127.0.0.1:8480)",
		"left out dashboard: dash.example.com: Sign-in with Authentik is not linked yet.",
		"dns replace: CNAME auth.example.com -> <new tunnel id>.cfargotunnel.com, proxied",
		"deletes A auth.example.com -> 203.0.113.7", "warning: Existing records are put back at teardown.",
		"tunnel setup --name homestead --dashboard-hostname dash.example.com --digest d1g"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if code, _, _ := invoke(t, []string{"--server", url, "tunnel", "plan"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestTunnelSetup(t *testing.T) {
	url := tunnelStub(t)
	base := []string{"--server", url, "tunnel", "setup", "--name", "homestead", "--dashboard-hostname", "dash.example.com"}
	code, stdout, stderr := invoke(t, append(base, "--digest", "d1g"))
	if code != exitOK || !strings.Contains(stdout, "operation: 23") {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
	// Setup approves a plan: no digest is a usage error naming tunnel plan.
	if code, _, stderr := invoke(t, base); code != exitUsage || !strings.Contains(stderr, "tunnel plan") {
		t.Fatalf("exit = %d stderr = %q", code, stderr)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "tunnel", "setup", "--digest", "d1g"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

// The token and account flags are retired (ADR-037) and say where the
// token now lives instead of silently ignoring it.
func TestTunnelRetiredCredentialFlags(t *testing.T) {
	url := tunnelStub(t)
	for _, args := range [][]string{
		{"tunnel", "setup", "--name", "n", "--digest", "d1g", "--api-token-file", "-"},
		{"tunnel", "setup", "--name", "n", "--digest", "d1g", "--api-token", "t"},
		{"tunnel", "plan", "--name", "n", "--account-id", "a"},
		{"tunnel", "teardown", "tunnel-id", "--account-id-file", "-"},
	} {
		code, _, stderr := invoke(t, append([]string{"--server", url}, args...))
		if code != exitUsage || !strings.Contains(stderr, "Domain step") {
			t.Fatalf("%v: exit = %d stderr = %q", args, code, stderr)
		}
	}
}

// The single-route flags are retired (ADR-037 §4) and name the new ones.
func TestTunnelRetiredRouteFlags(t *testing.T) {
	url := tunnelStub(t)
	for _, args := range [][]string{
		{"tunnel", "plan", "--name", "n", "--hostname", "h.example.com"},
		{"tunnel", "setup", "--name", "n", "--service", "http://x", "--digest", "d1g"},
	} {
		code, _, stderr := invoke(t, append([]string{"--server", url}, args...))
		if code != exitUsage || !strings.Contains(stderr, "--dashboard-hostname") {
			t.Fatalf("%v: exit = %d stderr = %q", args, code, stderr)
		}
	}
}

func TestTunnelTeardown(t *testing.T) {
	url := tunnelStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "tunnel", "teardown", "tunnel-id"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "operation: 24") {
		t.Fatalf("stdout = %q", stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "tunnel", "teardown"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "tunnel", "bogus"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

// --adopt plans and approves taking over an existing tunnel (S290).
func TestTunnelAdopt(t *testing.T) {
	url := tunnelStub(t)
	code, stdout, stderr := invoke(t, []string{"--server", url, "tunnel", "plan", "--adopt", "0a1b2c3d"})
	if code != exitOK {
		t.Fatalf("exit = %d stderr = %q", code, stderr)
	}
	for _, want := range []string{"adopt: 0a1b2c3d (healthy, 2 connections)",
		"existing old.example.com -> http://localhost:8096: Dropped from the connector.",
		"tunnel setup --adopt 0a1b2c3d --digest a1g"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	for _, args := range [][]string{
		{"tunnel", "plan", "--adopt", "0a1b2c3d", "--name", "homestead"},
		{"tunnel", "teardown", "tunnel-id", "--adopt", "0a1b2c3d"},
	} {
		if code, _, _ := invoke(t, append([]string{"--server", url}, args...)); code != exitUsage {
			t.Fatalf("%v: exit = %d, want usage", args, code)
		}
	}
}
