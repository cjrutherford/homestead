package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appsStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog":
			_, _ = w.Write([]byte(`[{"name":"jellyfin","version":"12.2","title":"Jellyfin","blurb":"Media.","category":"media","icon":"mdi:plex"}]`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/plan"):
			_, _ = w.Write([]byte(`{"Package":"jellyfin","Requirements":{"Passed":true},"Routes":[{"Host":"jellyfin.lan","Exposure":"local"}],"DNS":[],"TLS":[],"Firewall":[],"Directories":[],"Actions":["provision","up"]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/install"):
			_, _ = w.Write([]byte(`{"id":7}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/configure"):
			_, _ = w.Write([]byte(`{"id":9}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/uninstall"):
			_, _ = w.Write([]byte(`{"id":11}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func writeInput(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestAppsList(t *testing.T) {
	url := appsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "list"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "jellyfin 12.2 — Jellyfin") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAppsPlan(t *testing.T) {
	url := appsStub(t)
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "plan", "jellyfin", "-f", file})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"package: jellyfin", "route: jellyfin.lan (local)", "actions: provision, up"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "plan", "jellyfin"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestAppsInstall(t *testing.T) {
	url := appsStub(t)
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "install", "jellyfin", "-f", file})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "operation: 7") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAppsConfigure(t *testing.T) {
	url := appsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "configure", "jellyfin",
		"--service", "jellyfin", "--set", "TZ=UTC", "--set", "PUID=1000", "--unset", "OLD"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "operation: 9") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "configure", "jellyfin", "--service", "jellyfin"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "configure", "jellyfin",
		"--service", "jellyfin", "--set", "NOEQUALS"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "bogus"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestAppsUninstall(t *testing.T) {
	url := appsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "uninstall", "jellyfin"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "operation: 11") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "uninstall"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
	code, _, stderr := invoke(t, []string{"--server", url, "apps", "uninstall", "jellyfin", "--data-dir", "nope"})
	if code != exitUsage || !strings.Contains(stderr, "MOUNT:PATH") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}
