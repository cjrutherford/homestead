package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestClaimTokenCommand(t *testing.T) {
	url, svc := claimDaemon(t)
	path := filepath.Join(t.TempDir(), "claim-token")
	if _, err := svc.EnsureTokenFile(path, claim.TokenTTL); err != nil {
		t.Fatalf("EnsureTokenFile: %v", err)
	}
	tf, err := claim.ReadTokenFile(path)
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}
	code, stdout, _ := invoke(t, []string{"--server", url, "claim", "token", "--file", path})
	if code != exitOK || !strings.Contains(stdout, "token: "+tf.Token) || !strings.Contains(stdout, "expires:") {
		t.Fatalf("exit = %d stdout = %q", code, stdout)
	}
	// The printed token redeems.
	if code, _, _ := invoke(t, []string{"--server", url, "claim", tf.Token}); code != exitOK {
		t.Fatalf("redeem exit = %d", code)
	}
	// Claimed: the next ensure (the daemon's claimed hook) removes the file
	// and the command says so plainly.
	if _, err := svc.EnsureTokenFile(path, claim.TokenTTL); err != nil {
		t.Fatal(err)
	}
	// Claimed: the file is gone and the command says so plainly.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file still present after claim: %v", err)
	}
	code, _, stderr := invoke(t, []string{"--server", url, "claim", "token", "--file", path})
	if code != exitError || !strings.Contains(stderr, "already claimed") {
		t.Fatalf("exit = %d stderr = %q", code, stderr)
	}
}

func TestClaimTokenDaemonDownAndExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claim-token")
	code, _, stderr := invoke(t, []string{"--server", "http://127.0.0.1:1", "claim", "token", "--file", path})
	if code != exitError || !strings.Contains(stderr, "not running") {
		t.Fatalf("exit = %d stderr = %q", code, stderr)
	}
	if err := claim.WriteTokenFile(path, "tok", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(t, []string{"--server", "http://127.0.0.1:1", "claim", "token", "--file", path})
	if code != exitError || !strings.Contains(stderr, "expired") || strings.Contains(stdout, "tok") {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
}

func TestClaimTokenStaleFileDaemonDownPrintsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claim-token")
	if err := claim.WriteTokenFile(path, "stale-secret", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(t, []string{"--server", "http://127.0.0.1:1", "claim", "token", "--file", path})
	if code != exitError || !strings.Contains(stderr, "Homestead is not running") {
		t.Fatalf("exit = %d stderr = %q", code, stderr)
	}
	if strings.Contains(stdout+stderr, "stale-secret") {
		t.Fatalf("a dead token was printed: %q %q", stdout, stderr)
	}
}

func TestClaimTokenStaleFileClaimedPrintsNothing(t *testing.T) {
	url, svc := claimDaemon(t)
	path := filepath.Join(t.TempDir(), "claim-token")
	if _, err := svc.EnsureTokenFile(path, claim.TokenTTL); err != nil {
		t.Fatal(err)
	}
	tf, _ := claim.ReadTokenFile(path)
	if err := svc.Claim(tf.Token); err != nil {
		t.Fatal(err)
	}
	// A file left behind (preserved runtime dir) must not be printed.
	if err := claim.WriteTokenFile(path, tf.Token, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(t, []string{"--server", url, "claim", "token", "--file", path})
	if code != exitError || !strings.Contains(stderr, "already claimed") || strings.Contains(stdout, tf.Token) {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
}
