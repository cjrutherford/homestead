package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/appsso"
)

func appSignInStub(t *testing.T, bodies *[]map[string]any) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			*bodies = append(*bodies, body)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/jellyfin/sign-in":
			_, _ = w.Write([]byte(`{"package":"jellyfin","offered":true,"enabled":true,"replan":true,"adapter":"jellyfin-sso-plugin","plugin":{"name":"SSO Authentication","version":"4.0.0.4"},"sign_in_url":"https://jellyfin.lan/sso/OID/start/authentik","token_kept":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/sign-in/plan":
			_, _ = w.Write([]byte(`{"package":"jellyfin","title":"Jellyfin","enabled":true,"provider":"Homestead: Jellyfin","application":"Jellyfin","client_id":"homestead-jellyfin","issuer":"https://authentik.lan/application/o/homestead-jellyfin/","redirect_uris":["https://jellyfin.lan/sso/OID/redirect/authentik"],"app_settings":["Install the plugin"],"changes":["Create the provider"],"warnings":[],"digest":"d1g"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/sign-in/apply":
			_, _ = w.Write([]byte(`{"id":42}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestAppsSignInStatusPlanApply(t *testing.T) {
	var bodies []map[string]any
	url := appSignInStub(t, &bodies)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "sign-in", "status", "jellyfin"})
	if code != exitOK || !strings.Contains(stdout, "on (jellyfin-sso-plugin)") || !strings.Contains(stdout, "re-plan") ||
		!strings.Contains(stdout, "plugin: SSO Authentication 4.0.0.4") || !strings.Contains(stdout, "apply needs --token-file") {
		t.Fatalf("status: %d %q", code, stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "apps", "sign-in", "plan", "jellyfin"})
	if code != exitOK || !strings.Contains(stdout, "redirect uri: https://jellyfin.lan/sso/OID/redirect/authentik") ||
		!strings.Contains(stdout, "approve: homesteadctl apps sign-in apply jellyfin --digest d1g") {
		t.Fatalf("plan: %d %q", code, stdout)
	}
	if bodies[0]["enabled"] != true {
		t.Fatalf("plan body: %v", bodies[0])
	}
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("ak-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "apps", "sign-in", "apply", "jellyfin", "--off", "--digest", "d1g", "--token-file", tok})
	if code != exitOK || !strings.Contains(stdout, "operation: 42") || strings.Contains(stdout, "ak-secret") {
		t.Fatalf("apply: %d %q", code, stdout)
	}
	if bodies[1]["digest"] != "d1g" || bodies[1]["enabled"] != false || bodies[1]["token"] != "ak-secret" {
		t.Fatalf("apply body: %v", bodies[1])
	}
	for _, args := range [][]string{
		{"apps", "sign-in", "apply", "jellyfin"},
		{"apps", "sign-in", "plan", "jellyfin", "--digest", "x"},
		{"apps", "sign-in", "status"},
		{"apps", "sign-in", "nope", "jellyfin"},
	} {
		if code, _, _ := invoke(t, append([]string{"--server", url}, args...)); code != exitUsage {
			t.Fatalf("%v: %d", args, code)
		}
	}
}

// S399: a published app's status shows where visitors through the tunnel
// sign in, next to the local address.
func TestPrintAppSignInStatusPublicURL(t *testing.T) {
	var out bytes.Buffer
	printAppSignInStatus(&out, appsso.Status{Package: "immich", Enabled: true, Adapter: "immich",
		SignInURL: "https://immich.lan/auth/login", PublicSignInURL: "https://immich.example.com/auth/login"})
	if !strings.Contains(out.String(), "sign in at: https://immich.lan/auth/login") ||
		!strings.Contains(out.String(), "public sign in at: https://immich.example.com/auth/login") {
		t.Fatalf("output = %q", out.String())
	}
}
