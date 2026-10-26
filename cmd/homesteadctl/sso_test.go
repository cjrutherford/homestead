package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ssoSeen struct{ line, reauth string }

func ssoStub(t *testing.T) (string, *[]ssoSeen) {
	t.Helper()
	var seen []ssoSeen
	state := `{"state":"configured","configured":true,"linked":true,"dormant":false,"offered":true,"spec":{"issuer":"https://authentik.lan/application/o/homestead/","origins":["http://127.0.0.1:8080"],"redirect_uris":[]},"subject":"abc123","client_secret_sealed":true,"api_token_kept":false,"defaults":{"authentik_url":"","origins":[]},"authentik_installed":true,"session_method":"password"}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, ssoSeen{r.Method + " " + r.URL.Path + " " + string(raw), r.Header.Get("X-Homestead-Reauth")})
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/sso", "DELETE /api/v1/sso", "DELETE /api/v1/sso/link", "POST /api/v1/sso/approve":
			_, _ = w.Write([]byte(state))
		case "POST /api/v1/sso/plan":
			_, _ = w.Write([]byte(`{"digest":"d1gest","spec":{"authentik_url":"https://authentik.lan","authentik_version":"2026.8.3","issuer":"https://authentik.lan/application/o/homestead/","redirect_uris":["http://127.0.0.1:8080/api/v1/sso/callback"],"authorization_flow":"a","invalidation_flow":"b","signing_key":"k","scopes":["openid"]},"objects":[{"kind":"Application","name":"Homestead","action":"create"}],"untouched":["users and groups"],"warnings":["The API token can do anything."],"inspected":false,"expires_at":"2026-10-19T12:00:00Z"}`))
		case "POST /api/v1/sso/apply":
			_, _ = w.Write([]byte(`{"id":7}`))
		case "POST /api/v1/sso/link":
			_, _ = w.Write([]byte(`{"url":"https://authentik.lan/application/o/authorize/?x=1","expires_at":"2099-01-01T00:00:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL, &seen
}

func TestSSOStatusShowsReplan(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"configured","configured":true,"linked":true,"offered":true,"spec":{"issuer":"i","origins":["http://homestead.lan"]},"replan":true,"replan_reason":"Sign-in does not yet cover the public dashboard address https://homestead.example.com. Re-plan Sign-in and approve it."}`))
	}))
	t.Cleanup(ts.Close)
	code, out, _ := invoke(t, []string{"--server", ts.URL, "sso", "status"})
	if code != exitOK || !strings.Contains(out, "re-plan:   Sign-in does not yet cover the public dashboard address https://homestead.example.com") || !strings.Contains(out, "homesteadctl sso plan") {
		t.Fatalf("status: %d %s", code, out)
	}
}

func TestSSOCLI(t *testing.T) {
	url, seen := ssoStub(t)
	dir := t.TempDir()
	tok := filepath.Join(dir, "token")
	pw := filepath.Join(dir, "pw")
	_ = os.WriteFile(tok, []byte("ak-token\n"), 0o600)
	_ = os.WriteFile(pw, []byte("owner-password-123\n"), 0o600)

	code, out, _ := invoke(t, []string{"--server", url, "sso", "status"})
	if code != exitOK || !strings.Contains(out, "on (the login page offers it)") || !strings.Contains(out, "abc123") || strings.Contains(out, "ak-token") {
		t.Fatalf("status: %d %s", code, out)
	}
	code, out, _ = invoke(t, []string{"--server", url, "sso", "plan", "--origin", "http://127.0.0.1:8080"})
	if code != exitOK || !strings.Contains(out, "redirect URIs (exact)") || !strings.Contains(out, "left untouched") || !strings.Contains(out, "warning:") || !strings.Contains(out, "sso approve --digest d1gest") {
		t.Fatalf("plan: %d %s", code, out)
	}
	// The token is optional when one is already sealed.
	code, out, _ = invoke(t, []string{"--server", url, "sso", "approve", "--digest", "d1gest"})
	if code != exitOK {
		t.Fatalf("approve without a token file: %d %s", code, out)
	}
	code, out, _ = invoke(t, []string{"--server", url, "sso", "approve", "--digest", "d1gest", "--token-file", tok, "--keep-token"})
	if code != exitOK || strings.Contains(out, "ak-token") {
		t.Fatalf("approve: %d %s", code, out)
	}
	code, out, _ = invoke(t, []string{"--server", url, "sso", "apply"})
	if code != exitOK || !strings.Contains(out, "operation: 7") {
		t.Fatalf("apply: %d %s", code, out)
	}
	code, out, _ = invoke(t, []string{"--server", url, "sso", "link", "--password-file", pw})
	if code != exitOK || !strings.Contains(out, "https://authentik.lan/application/o/authorize/") || strings.Contains(out, "owner-password-123") {
		t.Fatalf("link: %d %s", code, out)
	}
	code, _, _ = invoke(t, []string{"--server", url, "sso", "unlink", "--password-file", pw})
	if code != exitOK {
		t.Fatalf("unlink: %d", code)
	}
	code, out, _ = invoke(t, []string{"--server", url, "sso", "disable"})
	if code != exitOK || !strings.Contains(out, "local owner account is unchanged") {
		t.Fatalf("disable: %d %s", code, out)
	}
	byLine := map[string]ssoSeen{}
	for _, s := range *seen {
		byLine[strings.SplitN(s.line, " ", 3)[0]+" "+strings.SplitN(s.line, " ", 3)[1]] = s
	}
	if byLine["POST /api/v1/sso/link"].reauth != "owner-password-123" || byLine["DELETE /api/v1/sso/link"].reauth != "owner-password-123" {
		t.Fatalf("link and unlink must send the fresh password: %+v", *seen)
	}
	if byLine["DELETE /api/v1/sso"].reauth != "" || byLine["POST /api/v1/sso/plan"].reauth != "" {
		t.Fatal("only link and unlink send the password")
	}
	if !strings.Contains(byLine["POST /api/v1/sso/approve"].line, `"keep_token":true`) || !strings.Contains(byLine["POST /api/v1/sso/approve"].line, `"api_token":"ak-token"`) {
		t.Fatalf("approve body: %s", byLine["POST /api/v1/sso/approve"].line)
	}
	if code, _, _ = invoke(t, []string{"--server", url, "sso"}); code != exitUsage {
		t.Fatal("bare sso must be a usage error")
	}
	if code, _, _ = invoke(t, []string{"--server", url, "sso", "approve"}); code != exitUsage {
		t.Fatal("approve without a digest must be a usage error")
	}
	if code, _, _ = invoke(t, []string{"--server", url, "sso", "bogus"}); code != exitUsage {
		t.Fatal("unknown subcommand must be a usage error")
	}
}

func TestAccessReadBackAndOpenWarning(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/catalog/jellyfin/sign-in/access", "POST /api/v1/sso/access":
			_, _ = w.Write([]byte(`{"groups":[{"name":"homestead-owner","exists":true,"members":1,"owner_member":true}],"bindings":[{"group":"homestead-owner","bound":false}],"claim_mapping":"ok","open":true,"drift":["homestead-owner is not bound"]}`))
		case "GET /api/v1/catalog/jellyfin/sign-in":
			_, _ = w.Write([]byte(`{"package":"jellyfin","enabled":true,"adapter":"jellyfin","open_to_all":true}`))
		case "GET /api/v1/sso":
			_, _ = w.Write([]byte(`{"state":"configured","configured":true,"linked":true,"offered":true,"spec":{"issuer":"i"},"open_apps":["jellyfin"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	for _, args := range [][]string{{"apps", "sign-in", "access", "jellyfin"}, {"sso", "access"}} {
		code, out, _ := invoke(t, append([]string{"--server", ts.URL}, args...))
		if code != exitOK || !strings.Contains(out, "drift: homestead-owner is not bound") || !strings.Contains(out, "open to every Authentik user") {
			t.Fatalf("%v: %d %s", args, code, out)
		}
	}
	_, out, _ := invoke(t, []string{"--server", ts.URL, "apps", "sign-in", "status", "jellyfin"})
	if !strings.Contains(out, "open to every Authentik user") {
		t.Fatalf("app status: %s", out)
	}
	_, out, _ = invoke(t, []string{"--server", ts.URL, "sso", "status"})
	if !strings.Contains(out, "open to every Authentik user: jellyfin") {
		t.Fatalf("sso status: %s", out)
	}
}
