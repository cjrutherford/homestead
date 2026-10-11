package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain keeps every test's stored CLI session out of the real home.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "homesteadctl-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Unsetenv("HOMESTEAD_TOKEN")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOMESTEAD_TOKEN", "")
	return home
}

func TestLoginStoresSessionAndLogoutDeletesIt(t *testing.T) {
	home := isolatedHome(t)
	url := authDaemon(t)
	serverArgs := []string{"--server", url}
	pass := passwordFile(t, "long-enough-password")
	invoke(t, append(serverArgs, "admin", "setup", "--username", "owner", "--password-file", pass))

	code, stdout, _ := invoke(t, append(serverArgs, "login", "--username", "owner", "--password-file", pass))
	if code != exitOK {
		t.Fatalf("login exit = %d", code)
	}
	if strings.Contains(stdout, "csrf") {
		t.Fatalf("login output leaks the browser CSRF token: %q", stdout)
	}
	path := filepath.Join(home, ".config", "homestead", "session")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("session file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode = %v, want 0600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("session dir mode = %v (%v), want 0700", dir.Mode().Perm(), err)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if got := loadSession(url); got != out.Token {
		t.Fatalf("loadSession = %q, want the minted token", got)
	}

	// Reuse without --token: the stored session authenticates the call.
	if code, stdout, _ := invoke(t, append(serverArgs, "admin", "lockout")); code != exitOK || !strings.Contains(stdout, "unlocked") {
		t.Fatalf("lockout with stored session: %d %q", code, stdout)
	}

	if code, stdout, _ := invoke(t, append(serverArgs, "logout")); code != exitOK || !strings.Contains(stdout, "logged out") {
		t.Fatalf("logout: %d %q", code, stdout)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file survived logout: %v", err)
	}
	if code, _, _ := invoke(t, append(serverArgs, "admin", "lockout")); code != exitUsage {
		t.Fatalf("lockout after logout exit = %d, want usage (no credentials)", code)
	}
}

func TestStoredSessionIsBoundToServerAndExpiry(t *testing.T) {
	isolatedHome(t)
	if err := saveSession(storedSession{Server: "http://a", Token: "t", ExpiresAt: farFuture()}); err != nil {
		t.Fatal(err)
	}
	if got := loadSession("http://a"); got != "t" {
		t.Fatalf("same server = %q", got)
	}
	if got := loadSession("http://b"); got != "" {
		t.Fatalf("other server got token %q", got)
	}
	if err := saveSession(storedSession{Server: "http://a", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if got := loadSession("http://a"); got != "" {
		t.Fatalf("expired session returned %q", got)
	}
}

func TestLoosePermissionsInvalidateStoredSession(t *testing.T) {
	home := isolatedHome(t)
	if err := saveSession(storedSession{Server: "http://a", Token: "t", ExpiresAt: farFuture()}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "homestead", "session")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadSession("http://a"); got != "" {
		t.Fatalf("group/world readable session file was trusted: %q", got)
	}
}

func farFuture() (t time.Time) { return time.Now().Add(24 * time.Hour) }
