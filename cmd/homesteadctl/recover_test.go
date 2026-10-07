package main

import (
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/server"
	"homestead/internal/store"
)

func recoverDaemon(t *testing.T) string {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	claimSvc := claim.New(st)
	authSvc := auth.New(st, claimSvc)
	srv := server.New("127.0.0.1:0")
	srv.MountClaim(claimSvc)
	srv.MountAuth(authSvc, audit.New(st))
	srv.MountRecovery(authSvc)
	token, err := claimSvc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := claimSvc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := authSvc.SetupAdmin("owner", "long-enough-password"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	socketPath := filepath.Join(t.TempDir(), "homestead.sock")
	ts := httptest.NewUnstartedServer(srv.SocketHandler())
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	ts.Listener = ln
	ts.Start()
	t.Cleanup(ts.Close)
	return socketPath
}

func TestAdminRecover(t *testing.T) {
	socketPath := recoverDaemon(t)
	pass := passwordFile(t, "recovered-password")
	code, stdout, _ := invoke(t, []string{
		"admin", "recover",
		"--username", "owner",
		"--password-file", pass,
		"--socket", socketPath,
	})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, `"owner"`) {
		t.Fatalf("stdout = %q, want username", stdout)
	}
}

func TestAdminRecoverFailures(t *testing.T) {
	socketPath := recoverDaemon(t)
	pass := passwordFile(t, "recovered-password")
	code, _, stderr := invoke(t, []string{
		"admin", "recover",
		"--username", "ghost",
		"--password-file", pass,
		"--socket", socketPath,
	})
	if code != exitError || !strings.Contains(stderr, "not found") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	code, _, _ = invoke(t, []string{
		"admin", "recover",
		"--username", "owner",
		"--password-file", pass,
		"--socket", filepath.Join(t.TempDir(), "absent.sock"),
	})
	if code != exitError {
		t.Fatalf("exit = %d, want 1 for missing socket", code)
	}
	if code, _, _ := invoke(t, []string{"admin", "recover"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
}
