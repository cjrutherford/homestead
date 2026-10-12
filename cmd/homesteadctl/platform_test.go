package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func platformStub(t *testing.T, verifyState string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/platform/updates":
			_, _ = w.Write([]byte(`{"running":{},"plan":{"Components":[{"Component":"homestead-server","Current":"0.0.0","Target":"1.0.0","Verification":"verified"}],"Backup":true,"BackupWhy":"state","Warnings":[]},
				"releases":[{"component":"homestead-server","version":"1.0.0","counter":4,"state":"verified"},{"component":"homesteadctl","version":"","counter":0,"state":"failed","detail":"signature key is not trusted"}],"installed":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/platform/updates/verify":
			if verifyState == "failed" {
				_, _ = w.Write([]byte(`{"component":"homestead-server","version":"0.1.0","counter":1,"state":"failed","refusal":"rollback","detail":"older"}`))
				return
			}
			_, _ = w.Write([]byte(`{"component":"homestead-server","version":"1.0.0","counter":4,"state":"verified","key_id":"ABCD"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/platform/trust-keys":
			_, _ = w.Write([]byte(`[{"key_id":"ABCD","source":"embedded","label":"HAI"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/platform/trust-keys":
			_, _ = w.Write([]byte(`{"key_id":"EEFF","source":"owner"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/platform/trust-keys/ABCD":
			_, _ = w.Write([]byte(`{"key_id":"EEFF","source":"owner"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/platform/trust-keys/ABCD":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestUpdatesPlatformShowsVerification(t *testing.T) {
	url := platformStub(t, "")
	code, stdout, _ := invoke(t, []string{"--server", url, "updates", "platform"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"homestead-server 1.0.0 (counter 4): verified", "homesteadctl - (counter 0): failed", "not trusted", "[verified]"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestUpdatesVerify(t *testing.T) {
	url := platformStub(t, "")
	args := []string{"--server", url, "updates", "verify", "--component", "homestead-server", "--artifact", "a", "--checksums", "b", "--signature", "c"}
	code, stdout, _ := invoke(t, args)
	if code != exitOK || !strings.Contains(stdout, "verified with key ABCD") {
		t.Fatalf("exit=%d stdout=%s", code, stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "updates", "verify", "--component", "x"}); code != exitUsage {
		t.Fatalf("missing flags exit = %d", code)
	}
	failURL := platformStub(t, "failed")
	code, _, stderr := invoke(t, append([]string{"--server", failURL}, args[2:]...))
	if code != exitError || !strings.Contains(stderr, "--allow-older") {
		t.Fatalf("failed exit=%d stderr=%s", code, stderr)
	}
}

func TestUpdatesTrust(t *testing.T) {
	url := platformStub(t, "")
	keyFile := filepath.Join(t.TempDir(), "k.pub")
	if err := os.WriteFile(keyFile, []byte("untrusted comment: x\nabc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := invoke(t, []string{"--server", url, "updates", "trust", "list"}); code != exitOK || !strings.Contains(out, "ABCD  embedded") {
		t.Fatalf("list: %d %s", code, out)
	}
	if code, out, _ := invoke(t, []string{"--server", url, "updates", "trust", "add", "--key-file", keyFile}); code != exitOK || !strings.Contains(out, "EEFF") {
		t.Fatalf("add: %d %s", code, out)
	}
	if code, out, _ := invoke(t, []string{"--server", url, "updates", "trust", "replace", "ABCD", "--key-file", keyFile}); code != exitOK || !strings.Contains(out, "replaced by EEFF") {
		t.Fatalf("replace: %d %s", code, out)
	}
	if code, out, _ := invoke(t, []string{"--server", url, "updates", "trust", "remove", "ABCD"}); code != exitOK || !strings.Contains(out, "removed") {
		t.Fatalf("remove: %d %s", code, out)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "updates", "trust", "add"}); code != exitUsage {
		t.Fatalf("add without key exit = %d", code)
	}
}
