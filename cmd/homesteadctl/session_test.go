package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	code, stdout, _ := invoke(t, append(serverArgs, "login", "--username", "owner", "--password-file", pass, "--print-token"))
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

func TestServerMismatchPrintsHint(t *testing.T) {
	isolatedHome(t)
	if err := saveSession(storedSession{Server: "http://other:1", Token: "t", ExpiresAt: farFuture()}); err != nil {
		t.Fatal(err)
	}
	if tok, hint := loadSessionHint("http://here:2"); tok != "" || !strings.Contains(hint, "stored session is for http://other:1") || !strings.Contains(hint, "homesteadctl login") {
		t.Fatalf("token %q hint %q", tok, hint)
	}
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	_, _, stderr := invoke(t, []string{"--server", ts.URL, "audit", "list"})
	if !strings.Contains(stderr, "run homesteadctl login") {
		t.Fatalf("stderr lacks hint: %q", stderr)
	}
}

func TestSaveSessionRefusesSymlinks(t *testing.T) {
	home := isolatedHome(t)
	target := t.TempDir()
	cfg := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(cfg, "homestead")); err != nil {
		t.Fatal(err)
	}
	if err := saveSession(storedSession{Server: "s", Token: "t", ExpiresAt: farFuture()}); err == nil {
		t.Fatal("saveSession followed a symlinked directory")
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatalf("token written through symlink: %v", entries)
	}
	os.Remove(filepath.Join(cfg, "homestead"))
	dir := filepath.Join(cfg, "homestead")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, "x"), filepath.Join(dir, "session")); err != nil {
		t.Fatal(err)
	}
	if err := saveSession(storedSession{Server: "s", Token: "t", ExpiresAt: farFuture()}); err == nil {
		t.Fatal("saveSession replaced a symlinked session file")
	}
}

func TestSaveSessionKeepsExistingDirMode(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".config", "homestead")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := saveSession(storedSession{Server: "s", Token: "t", ExpiresAt: farFuture()}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o750 {
		t.Fatalf("pre-existing dir mode changed to %v", info.Mode().Perm())
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := saveSession(storedSession{Server: "s", Token: "t", ExpiresAt: farFuture()}); err == nil {
		t.Fatal("saveSession accepted a world-writable directory")
	}
}

func TestEnvTokenWinsOverStoredSession(t *testing.T) {
	isolatedHome(t)
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer ts.Close()
	if err := saveSession(storedSession{Server: ts.URL, Token: "stored", ExpiresAt: farFuture()}); err != nil {
		t.Fatal(err)
	}
	invoke(t, []string{"--server", ts.URL, "audit", "list"})
	t.Setenv("HOMESTEAD_TOKEN", "from-env")
	invoke(t, []string{"--server", ts.URL, "audit", "list"})
	if len(got) != 2 || got[0] != "Bearer stored" || got[1] != "Bearer from-env" {
		t.Fatalf("Authorization headers = %q, want stored then env", got)
	}
}

func TestAdminSetupLogsInAndStoresSession(t *testing.T) {
	isolatedHome(t)
	url := authDaemon(t)
	serverArgs := []string{"--server", url}
	pass := passwordFile(t, "long-enough-password")
	code, stdout, _ := invoke(t, append(serverArgs, "admin", "setup", "--username", "owner", "--password-file", pass))
	if code != exitOK {
		t.Fatalf("setup exit = %d", code)
	}
	if strings.Contains(stdout, "token") {
		t.Fatalf("setup output leaks the session token: %q", stdout)
	}
	if loadSession(url) == "" {
		t.Fatal("setup did not store a session")
	}
	// The enforcing boundary accepts the stored session, no login needed.
	if code, out, _ := invoke(t, append(serverArgs, "admin", "lockout")); code != exitOK || !strings.Contains(out, "unlocked") {
		t.Fatalf("lockout after setup: %d %q", code, out)
	}
}

func TestRejectedSessionSuggestsLogin(t *testing.T) {
	isolatedHome(t)
	url := authDaemon(t)
	pass := passwordFile(t, "long-enough-password")
	invoke(t, []string{"--server", url, "admin", "setup", "--username", "owner", "--password-file", pass})
	if err := saveSession(storedSession{Server: url, Token: "revoked-or-bogus", ExpiresAt: farFuture()}); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := invoke(t, []string{"--server", url, "admin", "lockout"})
	if code != exitError || !strings.Contains(stderr, "homesteadctl login") {
		t.Fatalf("exit %d stderr %q, want a login hint", code, stderr)
	}
}
