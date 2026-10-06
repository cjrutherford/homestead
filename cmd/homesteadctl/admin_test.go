package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/server"
	"homestead/internal/store"
)

func authDaemon(t *testing.T) string {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	claimSvc := claim.New(st)
	srv := server.New("127.0.0.1:0")
	srv.MountClaim(claimSvc)
	srv.MountAuth(auth.New(st, claimSvc))
	token, err := claimSvc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := claimSvc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func passwordFile(t *testing.T, secret string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return path
}

func TestAdminSetupLoginLogout(t *testing.T) {
	url := authDaemon(t)
	serverArgs := []string{"--server", url}
	pass := passwordFile(t, "long-enough-password")

	code, stdout, _ := invoke(t, append(serverArgs, "admin", "setup", "--username", "owner", "--password-file", pass))
	if code != exitOK {
		t.Fatalf("setup exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, `"owner"`) {
		t.Fatalf("stdout = %q, want username", stdout)
	}
	code, _, _ = invoke(t, append(serverArgs, "admin", "setup", "--username", "second", "--password-file", pass))
	if code != exitError {
		t.Fatalf("second setup exit = %d, want 1", code)
	}

	code, stdout, _ = invoke(t, append(serverArgs, "login", "--username", "owner", "--password-file", pass))
	if code != exitOK {
		t.Fatalf("login exit = %d, want 0", code)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(stdout), &login); err != nil || login.Token == "" {
		t.Fatalf("stdout = %q, want token", stdout)
	}
	token := login.Token

	code, _, _ = invoke(t, append(serverArgs, "logout", "--token", token))
	if code != exitOK {
		t.Fatalf("logout exit = %d, want 0", code)
	}
	code, _, stderr := invoke(t, append(serverArgs, "logout", "--token", token))
	if code != exitError || !strings.Contains(stderr, "invalid or expired session") {
		t.Fatalf("second logout exit = %d, stderr = %q", code, stderr)
	}
}

func TestLoginFailures(t *testing.T) {
	url := authDaemon(t)
	serverArgs := []string{"--server", url}
	pass := passwordFile(t, "long-enough-password")

	code, _, _ := invoke(t, append(serverArgs, "admin", "setup", "--username", "owner", "--password-file", pass))
	if code != exitOK {
		t.Fatalf("setup exit = %d, want 0", code)
	}
	wrong := passwordFile(t, "wrong-password-here")
	code, _, stderr := invoke(t, append(serverArgs, "login", "--username", "owner", "--password-file", wrong))
	if code != exitError || !strings.Contains(stderr, "invalid username or password") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	weak := passwordFile(t, "short")
	code, _, stderr = invoke(t, append(serverArgs, "admin", "setup", "--username", "weak", "--password-file", weak))
	if code != exitError || !strings.Contains(stderr, "invalid administrator") {
		t.Fatalf("exit = %d, stderr = %q; want 422 message", code, stderr)
	}
}

func TestAdminSetupRequiresFlags(t *testing.T) {
	url := authDaemon(t)
	if code, _, _ := invoke(t, []string{"--server", url, "admin", "setup"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "admin"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "login"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "logout"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "admin", "bogus"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
}
