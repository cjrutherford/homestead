package claim

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenFileModeAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claim-token")
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := WriteTokenFile(path, "abc", expires); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v; want 0600", info.Mode().Perm(), err)
	}
	tf, err := ReadTokenFile(path)
	if err != nil || tf.Token != "abc" || !tf.Expires.Equal(expires) {
		t.Fatalf("read = %+v, %v", tf, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestEnsureKeepsOneLiveTokenAndReusesFile(t *testing.T) {
	_, svc := openClaimStore(t)
	path := filepath.Join(t.TempDir(), "claim-token")
	if _, err := svc.EnsureTokenFile(path, TokenTTL); err != nil {
		t.Fatal(err)
	}
	first, _ := ReadTokenFile(path)
	if _, err := svc.EnsureTokenFile(path, TokenTTL); err != nil {
		t.Fatal(err)
	}
	again, _ := ReadTokenFile(path)
	if first.Token != again.Token {
		t.Fatal("a live token was replaced on restart")
	}
	// File lost (reboot): a fresh token replaces the old one, leaving one live row.
	os.Remove(path)
	if _, err := svc.EnsureTokenFile(path, TokenTTL); err != nil {
		t.Fatal(err)
	}
	fresh, _ := ReadTokenFile(path)
	if fresh.Token == first.Token {
		t.Fatal("expected a new token")
	}
	if err := svc.Claim(first.Token); err != ErrTokenInvalid {
		t.Fatalf("old token Claim = %v, want ErrTokenInvalid", err)
	}
	var n int
	svc.db.QueryRow(`SELECT COUNT(*) FROM claim_tokens`).Scan(&n)
	if n != 1 {
		t.Fatalf("token rows = %d, want 1", n)
	}
}

func TestMaintainRemintsAfterExpiryAndRemovesOnClaim(t *testing.T) {
	_, svc := openClaimStore(t)
	path := filepath.Join(t.TempDir(), "claim-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		svc.MaintainTokenFile(ctx, path, 1500*time.Millisecond, nil)
		close(done)
	}()
	waitFor := func(cond func() bool) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if cond() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("condition not met in time")
	}
	waitFor(func() bool { _, err := ReadTokenFile(path); return err == nil })
	first, _ := ReadTokenFile(path)
	waitFor(func() bool { tf, err := ReadTokenFile(path); return err == nil && tf.Token != first.Token })
	second, _ := ReadTokenFile(path)
	if err := svc.Claim(first.Token); err == nil {
		t.Fatal("expired token redeemed")
	}
	if err := svc.Claim(second.Token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	waitFor(func() bool { _, err := os.Stat(path); return os.IsNotExist(err) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("maintainer kept running after claim")
	}
}
