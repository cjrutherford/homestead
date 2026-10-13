package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recipientStub(t *testing.T, exports *int) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/backups/recipients":
			_, _ = w.Write([]byte(`{"recipients":[{"fingerprint":"aa","public_key":"age1app","kind":"appliance","label":"appliance identity"}],"owner_held":false,"note":"New runs only."}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/recipients":
			_, _ = w.Write([]byte(`{"fingerprint":"bb","public_key":"age1own","kind":"owner"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/backups/recipients/bb":
			_, _ = w.Write([]byte(`{"fingerprint":"bb","kind":"owner"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/backups/identity":
			_, _ = w.Write([]byte(`{"recipient":"age1app","fingerprint":"aa","exported":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/identity/export":
			*exports++
			_, _ = w.Write([]byte(`{"identity":"AGE-SECRET-KEY-TEST","recipient":"age1app","fingerprint":"aa"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestBackupRecipientsCLI(t *testing.T) {
	n := 0
	url := recipientStub(t, &n)
	code, out, _ := invoke(t, []string{"--server", url, "backup", "recipients", "list"})
	if code != exitOK || !strings.Contains(out, "appliance") || !strings.Contains(out, "warning") {
		t.Fatalf("list %d %q", code, out)
	}
	if code, out, _ = invoke(t, []string{"--server", url, "backup", "recipients", "add", "--key", "age1own"}); code != exitOK || !strings.Contains(out, "bb") {
		t.Fatalf("add %d %q", code, out)
	}
	if code, _, _ = invoke(t, []string{"--server", url, "backup", "recipients", "add"}); code != exitUsage {
		t.Fatalf("add without key = %d", code)
	}
	if code, out, _ = invoke(t, []string{"--server", url, "backup", "recipients", "remove", "--fingerprint", "bb"}); code != exitOK || !strings.Contains(out, "removed") {
		t.Fatalf("remove %d %q", code, out)
	}
}

func TestBackupIdentityExportRequiresExplicitDestination(t *testing.T) {
	n := 0
	url := recipientStub(t, &n)
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "export"}); code != exitUsage || n != 0 {
		t.Fatalf("bare export code=%d exports=%d", code, n)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "export", "--stdout", "--out", "x"}); code != exitUsage || n != 0 {
		t.Fatalf("both flags code=%d", code)
	}
	file := filepath.Join(t.TempDir(), "id.txt")
	code, out, _ := invoke(t, []string{"--server", url, "backup", "identity", "export", "--out", file})
	if code != exitOK || n != 1 || strings.Contains(out, "AGE-SECRET-KEY") {
		t.Fatalf("file export code=%d out=%q", code, out)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), "AGE-SECRET-KEY-TEST") {
		t.Fatalf("file = %q", raw)
	}
	// Never overwrites an existing file, and does not export.
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "export", "--out", file}); code == exitOK || n != 1 {
		t.Fatalf("overwrite code=%d exports=%d", code, n)
	}
	code, out, _ = invoke(t, []string{"--server", url, "backup", "identity", "export", "--stdout"})
	if code != exitOK || !strings.Contains(out, "AGE-SECRET-KEY-TEST") {
		t.Fatalf("stdout export %d %q", code, out)
	}
	if code, out, _ = invoke(t, []string{"--server", url, "backup", "identity", "status"}); code != exitOK || !strings.Contains(out, "never exported") {
		t.Fatalf("status %d %q", code, out)
	}
}
