package main

import (
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
				"connector":{"installed":true,"active":true},"accounts":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tunnel/setup":
			_, _ = w.Write([]byte(`{"id":23}`))
		case r.Method == http.MethodDelete:
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

func TestTunnelSetup(t *testing.T) {
	url := tunnelStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "tunnel", "setup",
		"--name", "homestead", "--hostname", "app.example.com",
		"--service", "http://127.0.0.1:8080",
		"--api-token", "token", "--account-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "operation: 23") {
		t.Fatalf("stdout = %q", stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "tunnel", "setup",
		"--name", "homestead"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestTunnelTeardown(t *testing.T) {
	url := tunnelStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "tunnel", "teardown",
		"tunnel-id", "--account-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
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

func TestTunnelTeardownAccountIDFromFileAndStdin(t *testing.T) {
	url := tunnelStub(t)
	file := writeTemp(t, "account", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n")
	code, stdout, stderr := invoke(t, []string{"--server", url, "tunnel", "teardown", "tunnel-id", "--account-id-file", file})
	if code != exitOK || !strings.Contains(stdout, "operation: 24") {
		t.Fatalf("file: exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "warning") {
		t.Fatalf("file form warned: %q", stderr)
	}
	old := stdinReader
	stdinReader = strings.NewReader("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n")
	t.Cleanup(func() { stdinReader = old })
	code, stdout, _ = invoke(t, []string{"--server", url, "tunnel", "teardown", "tunnel-id", "--account-id-file", "-"})
	if code != exitOK || !strings.Contains(stdout, "operation: 24") {
		t.Fatalf("stdin: exit = %d stdout = %q", code, stdout)
	}
	empty := writeTemp(t, "empty", "\n")
	if code, _, _ := invoke(t, []string{"--server", url, "tunnel", "teardown", "tunnel-id", "--account-id-file", empty}); code != exitError {
		t.Fatalf("empty file exit = %d, want 1", code)
	}
}

func TestTunnelArgvSecretsWarn(t *testing.T) {
	url := tunnelStub(t)
	_, _, stderr := invoke(t, []string{"--server", url, "tunnel", "teardown", "tunnel-id", "--account-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "--account-id-file") {
		t.Fatalf("stderr = %q, want argv warning", stderr)
	}
}

func TestTunnelSetupSecretsFromFiles(t *testing.T) {
	url := tunnelStub(t)
	tok := writeTemp(t, "tok", "cf-token-value\n")
	acct := writeTemp(t, "acct", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n")
	code, _, stderr := invoke(t, []string{"--server", url, "tunnel", "setup", "--name", "n", "--hostname", "h.example.com",
		"--service", "http://127.0.0.1:80", "--api-token-file", tok, "--account-id-file", acct})
	if code != exitOK {
		t.Fatalf("exit = %d stderr = %q", code, stderr)
	}
}
