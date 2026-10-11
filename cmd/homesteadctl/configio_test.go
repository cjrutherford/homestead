package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/desired"
	"homestead/internal/secret"
	"homestead/internal/server"
	"homestead/internal/store"
)

// configIODaemon serves export/import over scratch state: one secret,
// one configured setting.
func configIODaemon(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	secrets, err := secret.New(s, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	if err := secrets.Set("dns_api_token", "token-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	desiredPath := filepath.Join(dir, "desired.yaml")
	if err := desired.Save(desiredPath, desired.State{Version: desired.CurrentVersion, Settings: map[string]any{"hostname": "homestead"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	srv := server.New("127.0.0.1:0")
	srv.MountConfigIO(desiredPath, secrets)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestConfigExportFile(t *testing.T) {
	serverArgs := []string{"--server", configIODaemon(t)}
	out := filepath.Join(t.TempDir(), "export.yaml")
	code, _, stderr := invoke(t, append(serverArgs, "config", "export", "-f", out))
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"hostname", "dns_api_token", "store_schema"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("export missing %q", want)
		}
	}
	if strings.Contains(string(raw), "token-value") {
		t.Fatal("export leaks secret value")
	}
}

func TestConfigExportStdout(t *testing.T) {
	code, stdout, _ := invoke(t, []string{"--server", configIODaemon(t), "config", "export"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, `"secrets"`) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestConfigImportFile(t *testing.T) {
	serverArgs := []string{"--server", configIODaemon(t)}
	exportFile := filepath.Join(t.TempDir(), "export.yaml")
	code, _, stderr := invoke(t, append(serverArgs, "config", "export", "-f", exportFile))
	if code != exitOK {
		t.Fatalf("export exit = %d, %q", code, stderr)
	}
	secretsFile := writeTemp(t, "secrets.json", `{"dns_api_token": "rotated-value"}`)
	code, stdout, stderr := invoke(t, append(serverArgs, "config", "import", "-f", exportFile, "--secrets-file", secretsFile))
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "secrets: 1") {
		t.Fatalf("stdout = %q", stdout)
	}
	// Missing secrets file fails naming the gap.
	code, _, stderr = invoke(t, append(serverArgs, "config", "import", "-f", exportFile))
	if code == exitOK || !strings.Contains(stderr, "dns_api_token") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	// Missing document fails usage-first.
	code, _, _ = invoke(t, append(serverArgs, "config", "import"))
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}
