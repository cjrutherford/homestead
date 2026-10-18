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

func craftFilesStub(t *testing.T) (string, *[]string) {
	t.Helper()
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(raw))
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/craft/files":
			_, _ = w.Write([]byte(`{"linked":true,"base_url":"https://cloud.lan","account":"alice","root":"Homestead","credential_stored":true,"daemon_created":false,"apps":[{"name":"gridcraft","title":"GridCraft","mode":"open-save","local_only":false,"host":"gridcraft.lan","ready":true},{"name":"pdfcraft","title":"PdfCraft","mode":"open-save","local_only":true,"ready":false}]}`))
		case "POST /api/v1/craft/files/plan":
			_, _ = w.Write([]byte(`{"id":"p1","action":"link","summary":["Link it."],"warnings":["Homestead enters the file path."],"expires_at":"2026-10-17T12:15:00Z"}`))
		case "POST /api/v1/craft/files/approve":
			_, _ = w.Write([]byte(`{"linked":true,"base_url":"https://cloud.lan","account":"alice","root":"Homestead","credential_stored":true,"apps":[]}`))
		case "POST /api/v1/craft/files/launch":
			_, _ = w.Write([]byte(`{"url":"https://gridcraft.lan/_homestead/open.html#code=abc","app":"gridcraft","expires_at":"2099-01-01T00:00:00Z"}`))
		case "POST /api/v1/craft/files/local-only":
			_, _ = w.Write([]byte(`{"linked":false,"apps":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL, &seen
}

func TestCraftFilesCLI(t *testing.T) {
	url, seen := craftFilesStub(t)
	code, out, _ := invoke(t, []string{"--server", url, "craft", "files", "status"})
	if code != exitOK || !strings.Contains(out, "alice") || !strings.Contains(out, "app password sealed") || !strings.Contains(out, "browser-local (opted out)") {
		t.Fatalf("status: %d %s", code, out)
	}
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte("the-app-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Without --yes only the plan is shown and nothing is approved.
	code, out, _ = invoke(t, []string{"--server", url, "craft", "files", "link", "--url", "https://cloud.lan", "--account", "alice", "--password-file", pw})
	if code != exitOK || !strings.Contains(out, "rerun with --yes") || strings.Contains(out, "the-app-password") {
		t.Fatalf("link plan: %d %s", code, out)
	}
	for _, s := range *seen {
		if strings.Contains(s, "approve") {
			t.Fatalf("approved without --yes: %v", *seen)
		}
	}
	code, out, _ = invoke(t, []string{"--server", url, "craft", "files", "link", "--url", "https://cloud.lan", "--account", "alice", "--password-file", pw, "--yes"})
	if code != exitOK || strings.Contains(out, "the-app-password") {
		t.Fatalf("link: %d %s", code, out)
	}
	joined := strings.Join(*seen, "\n")
	if !strings.Contains(joined, `"root":"Homestead"`) || !strings.Contains(joined, `"app_password":"the-app-password"`) {
		t.Fatalf("requests: %s", joined)
	}
	code, out, _ = invoke(t, []string{"--server", url, "craft", "files", "launch", "gridcraft"})
	if code != exitOK || !strings.Contains(out, "https://gridcraft.lan/_homestead/open.html#code=abc") {
		t.Fatalf("launch: %d %s", code, out)
	}
	if code, _, _ = invoke(t, []string{"--server", url, "craft", "files", "local-only", "gridcraft", "on"}); code != exitOK {
		t.Fatal("local-only")
	}
	if code, _, _ = invoke(t, []string{"--server", url, "craft", "files", "local-only", "gridcraft", "maybe"}); code != exitUsage {
		t.Fatal("local-only usage")
	}
	if code, _, _ = invoke(t, []string{"--server", url, "craft", "files", "link", "--url", "x"}); code != exitUsage {
		t.Fatal("link without a password source must be a usage error")
	}
}

func TestCraftFilesCLIInsecureHTTPOptIn(t *testing.T) {
	url, seen := craftFilesStub(t)
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte("the-app-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--server", url, "craft", "files", "link", "--url", "http://cloud.lan", "--account", "alice", "--password-file", pw}
	invoke(t, args)
	invoke(t, append(args, "--allow-insecure-http"))
	if len(*seen) != 2 || strings.Contains((*seen)[0], `"allow_insecure_http":true`) || !strings.Contains((*seen)[1], `"allow_insecure_http":true`) {
		t.Fatalf("plan requests: %v", *seen)
	}
}
