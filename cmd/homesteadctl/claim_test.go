package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/claim"
	"homestead/internal/server"
	"homestead/internal/store"
)

func claimDaemon(t *testing.T) (string, *claim.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := claim.New(st)
	srv := server.New("127.0.0.1:0")
	srv.MountClaim(svc)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL, svc
}

func TestClaimCommand(t *testing.T) {
	url, svc := claimDaemon(t)
	token, err := svc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	code, stdout, _ := invoke(t, []string{"--server", url, "claim", token})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "CLAIMED") {
		t.Fatalf("stdout = %q, want CLAIMED", stdout)
	}
}

func TestClaimCommandFailures(t *testing.T) {
	url, svc := claimDaemon(t)
	code, _, stderr := invoke(t, []string{"--server", url, "claim", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	if code != exitError || !strings.Contains(stderr, "invalid or expired") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	token, err := svc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "claim", token}); code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	code, _, stderr = invoke(t, []string{"--server", url, "claim", token})
	if code != exitError || !strings.Contains(stderr, "already claimed") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestClaimCommandUsage(t *testing.T) {
	if code, _, _ := invoke(t, []string{"claim"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
}
