package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appSetupStub(t *testing.T, bodies *[]map[string]any) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			*bodies = append(*bodies, body)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/jellyfin/setup":
			_, _ = w.Write([]byte(`{"app":"jellyfin","status":"stopped","install_operation":7,"operation":9,"steps":[{"name":"address","state":"done","digest":"a"},{"name":"sign-in","state":"changed","digest":"s","reason":"The plan changed since it was approved."}],"reason":"Stopped at the sign-in with Authentik: The plan changed since it was approved.","review":{"step":"sign-in","digest":"s2","sign_in":{"changes":["Create the provider"],"warnings":[]},"token_kept":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/setup/resume":
			_, _ = w.Write([]byte(`{"id":11}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/setup/skip":
			_, _ = w.Write([]byte(`{"app":"jellyfin","status":"skipped","steps":[{"name":"sign-in","state":"skipped","digest":"s"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestAppsSetupStatusResumeSkip(t *testing.T) {
	var bodies []map[string]any
	url := appSetupStub(t, &bodies)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "setup", "status", "jellyfin"})
	if code != exitOK || !strings.Contains(stdout, "setup:  stopped") || !strings.Contains(stdout, "change: Create the provider") ||
		!strings.Contains(stdout, "resume: homesteadctl apps setup resume jellyfin --digest s2 --token-file FILE") {
		t.Fatalf("status: %d %q", code, stdout)
	}
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("ak-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "apps", "setup", "resume", "jellyfin", "--digest", "s2", "--token-file", tok})
	if code != exitOK || !strings.Contains(stdout, "operation: 11") || strings.Contains(stdout, "ak-secret") {
		t.Fatalf("resume: %d %q", code, stdout)
	}
	if bodies[0]["digest"] != "s2" || bodies[0]["token"] != "ak-secret" {
		t.Fatalf("resume body: %v", bodies[0])
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "apps", "setup", "skip", "jellyfin"})
	if code != exitOK || !strings.Contains(stdout, "setup:  skipped") {
		t.Fatalf("skip: %d %q", code, stdout)
	}
	if code, _, _ = invoke(t, []string{"--server", url, "apps", "setup", "resume", "jellyfin"}); code != exitUsage {
		t.Fatalf("resume without digest = %d", code)
	}
	if code, _, _ = invoke(t, []string{"--server", url, "apps", "setup", "skip", "jellyfin", "--digest", "x"}); code != exitUsage {
		t.Fatalf("skip with digest = %d", code)
	}
}
