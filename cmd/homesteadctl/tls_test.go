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

func TestTLSRenewal(t *testing.T) {
	body := `{"recorded_issuances":1,"summary":"1 recorded issuance(s)","certificates":[{"certificate":"www","status":"renewal-due","days_remaining":12,"not_after":"2026-10-23T00:00:00Z","detail":"www expires soon"}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tls/renewal" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "tls", "renewal"})
	if code != exitOK || !strings.Contains(stdout, "www: renewal-due days=12") {
		t.Fatalf("exit = %d stdout = %q", code, stdout)
	}
	body = strings.Replace(body, "renewal-due", "expired", 1)
	code, _, _ = invoke(t, []string{"--server", ts.URL, "tls", "renewal"})
	if code != exitError {
		t.Fatalf("expired exit = %d, want %d", code, exitError)
	}
	body = strings.Replace(body, "expired", "error", 1)
	if code, _, _ = invoke(t, []string{"--server", ts.URL, "tls", "renewal"}); code != exitError {
		t.Fatalf("error exit = %d, want %d", code, exitError)
	}
	body = `{"recorded_issuances":0,"summary":"no recorded issuances: nothing to renew","certificates":[]}`
	code, stdout, _ = invoke(t, []string{"--server", ts.URL, "tls", "renewal"})
	if code != exitOK || !strings.Contains(stdout, "no recorded issuances") {
		t.Fatalf("empty exit = %d stdout = %q", code, stdout)
	}
	code, _, _ = invoke(t, []string{"--server", ts.URL, "tls"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestTLSIssuanceFlow(t *testing.T) {
	var approveBody, credBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/tls/issuance/plans":
			_, _ = w.Write([]byte(`{"plans":[{"certificate":"www","names":["www.example.com"],"provider":"cloudflare","zone":"example.com","challenge":"dns-01","directory":"https://acme-staging-v02.api.letsencrypt.org/directory","directory_class":"staging","account":{"state":"new","detail":"will register","secret":"sys_acme_account_ab"},"terms":"agree","placement":{"directory":"/d","chain_file":"/d/fullchain.pem","key_file":"/d/privkey.pem","key_mode":"0600"},"served_routes":["www.example.com"],"ready":true,"blockers":[],"digest":"abc","approved":false}]}`))
		case "GET /api/v1/tls/issuance/approvals":
			_, _ = w.Write([]byte(`{"approvals":[]}`))
		case "POST /api/v1/tls/issuance/approvals":
			_ = json.NewDecoder(r.Body).Decode(&approveBody)
			_, _ = w.Write([]byte(`{"certificate":"www","digest":"abc","actor":"local"}`))
		case "POST /api/v1/tls/issuance/credentials":
			_ = json.NewDecoder(r.Body).Decode(&credBody)
			_, _ = w.Write([]byte(`{"provider":"cloudflare","stored":["api_token"]}`))
		case "POST /api/v1/tls/issuance/apply":
			_, _ = w.Write([]byte(`{"id":12}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	run := func(args ...string) (int, string, string) {
		return invoke(t, append([]string{"--server", ts.URL, "tls", "issuance"}, args...))
	}
	code, stdout, _ := run("plan")
	for _, want := range []string{"www (not approved)", "staging", "/d/privkey.pem", "mode 0600", "digest:    abc"} {
		if code != exitOK || !strings.Contains(stdout, want) {
			t.Fatalf("plan exit %d missing %q in %q", code, want, stdout)
		}
	}
	if code, _, stderr := run("plan", "nope"); code != exitError || !strings.Contains(stderr, "no issuable certificate") {
		t.Fatalf("plan nope = %d %q", code, stderr)
	}
	if code, stdout, _ := run("approvals"); code != exitOK || !strings.Contains(stdout, "no issuance approvals") {
		t.Fatalf("approvals = %d %q", code, stdout)
	}
	// Approve needs the digest; consent flags travel in the body.
	if code, _, _ := run("approve", "www"); code != exitUsage {
		t.Fatalf("approve without digest = %d", code)
	}
	code, stdout, _ = run("approve", "www", "--digest", "abc", "--agree-terms", "--confirm-production")
	if code != exitOK || !strings.Contains(stdout, "approved: www") ||
		approveBody["digest"] != "abc" || approveBody["agree_terms"] != true || approveBody["confirm_production"] != true {
		t.Fatalf("approve = %d %q body %v", code, stdout, approveBody)
	}
	if code, stdout, _ := run("apply", "www"); code != exitOK || !strings.Contains(stdout, "operation: 12") {
		t.Fatalf("apply = %d %q", code, stdout)
	}
	// Credentials come from a file or stdin, never argv, and never print.
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("s3cret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := run("credentials", "cloudflare", "api_token=@"+tokenFile)
	values, _ := credBody["values"].(map[string]any)
	if code != exitOK || values["api_token"] != "s3cret-token" || strings.Contains(stdout+stderr, "s3cret-token") {
		t.Fatalf("credentials = %d %q %q body %v", code, stdout, stderr, credBody)
	}
	if code, _, stderr := run("credentials", "cloudflare", "api_token=s3cret-token"); code != exitUsage ||
		!strings.Contains(stderr, "never the command line") || strings.Contains(stderr, "s3cret-token") {
		t.Fatalf("argv credential = %d %q", code, stderr)
	}
	if code, _, _ := run("credentials", "cloudflare"); code != exitUsage {
		t.Fatalf("credentials without fields = %d", code)
	}
	if code, _, _ := run("apply"); code != exitUsage {
		t.Fatalf("apply without cert = %d", code)
	}
	if code, _, _ := run("bogus"); code != exitUsage {
		t.Fatalf("bogus = %d", code)
	}
}

func TestReadCredentialFromStdin(t *testing.T) {
	name, value, err := readCredential("api_token=-", strings.NewReader("tok\r\n"))
	if err != nil || name != "api_token" || value != "tok" {
		t.Fatalf("readCredential = %q %q %v", name, value, err)
	}
	for _, bad := range []string{"api_token", "=-", "api_token=", "api_token=plain", "api_token=@"} {
		if _, _, err := readCredential(bad, strings.NewReader("")); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestReadCredentialCapsSize(t *testing.T) {
	big := strings.Repeat("a", 64<<10+1)
	if _, _, err := readCredential("api_token=-", strings.NewReader(big)); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("stdin = %v", err)
	}
	file := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(file, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readCredential("api_token=@"+file, strings.NewReader("")); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("file = %v", err)
	}
	ok := strings.Repeat("a", 64<<10)
	if _, v, err := readCredential("api_token=-", strings.NewReader(ok)); err != nil || len(v) != 64<<10 {
		t.Fatalf("at the cap = %d %v", len(v), err)
	}
}

func TestTLSRenewalShowsFailuresAndOwnerAction(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"recorded_issuances":1,"summary":"1 recorded issuance(s)","certificates":[{"certificate":"www","status":"renewal-due","days_remaining":9,"not_after":"2026-10-23T00:00:00Z","detail":"www expires soon","attempts":3,"last_error":"rate limited","next_attempt_at":"2026-10-15T04:00:00Z","account_recovery_required":true}]}`))
	}))
	defer ts.Close()
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "tls", "renewal"})
	if code != exitOK || !strings.Contains(stdout, "3 failed attempt(s), last: rate limited; next attempt 2026-10-15T04:00:00Z") ||
		!strings.Contains(stdout, "owner action required: homesteadctl tls issuance reregister-account www") {
		t.Fatalf("exit = %d stdout = %q", code, stdout)
	}
}

func TestTLSReregisterAccount(t *testing.T) {
	var body map[string]any
	class := "staging"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/tls/issuance/account/terms":
			_, _ = w.Write([]byte(`{"directory":"https://ca.example/dir","directory_class":"` + class +
				`","current_terms_url":"https://ca.example/terms-v2","recorded_terms_url":"https://ca.example/terms-v1","changed":true,"agreement_required":` +
				map[bool]string{true: "true", false: "false"}[class == "production"] + `}`))
		case "POST /api/v1/tls/issuance/account/reregister":
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["certificate"] == "bad" {
				http.Error(w, "cert: issuance not approved", http.StatusConflict)
				return
			}
			_, _ = w.Write([]byte(`{"certificate":"www","reregistered":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "tls", "issuance", "reregister-account", "www"})
	if code != exitOK || body["certificate"] != "www" || body["agree_terms"] != false ||
		!strings.Contains(stdout, "terms-v2") || !strings.Contains(stdout, "re-registered the ACME account for www") {
		t.Fatalf("exit = %d body = %v stdout = %q", code, body, stdout)
	}
	if code, _, _ = invoke(t, []string{"--server", ts.URL, "tls", "issuance", "reregister-account", "bad"}); code != exitError {
		t.Fatalf("refused exit = %d", code)
	}
	if code, _, _ = invoke(t, []string{"--server", ts.URL, "tls", "issuance", "reregister-account"}); code != exitUsage {
		t.Fatalf("usage exit = %d", code)
	}
	// Production shows the terms and refuses without a fresh agreement.
	class, body = "production", nil
	code, stdout, stderr := invoke(t, []string{"--server", ts.URL, "tls", "issuance", "reregister-account", "www"})
	if code != exitUsage || body != nil || !strings.Contains(stdout, "terms-v2") || !strings.Contains(stderr, "--agree-terms") {
		t.Fatalf("production without agreement: %d %q %q body=%v", code, stdout, stderr, body)
	}
	code, _, _ = invoke(t, []string{"--server", ts.URL, "tls", "issuance", "reregister-account", "www", "--agree-terms"})
	if code != exitOK || body["agree_terms"] != true {
		t.Fatalf("production with agreement: %d %v", code, body)
	}
}
