package claim

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestMaintainRemovesFileOnShutdown(t *testing.T) {
	_, svc := openClaimStore(t)
	path := filepath.Join(t.TempDir(), "claim-token")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.MaintainTokenFile(ctx, path, TokenTTL, nil)
		close(done)
	}()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("token file never appeared: %v", err)
	}
	cancel()
	<-done
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file survived a clean stop: %v", err)
	}
}

func TestEnsureRemovesFileWhenClaimedDuringWrite(t *testing.T) {
	_, svc := openClaimStore(t)
	path := filepath.Join(t.TempDir(), "claim-token")
	afterTokenWrite = func() {
		tf, err := ReadTokenFile(path)
		if err != nil {
			t.Error(err)
			return
		}
		// Claim commits without the file hook having a file to remove yet.
		if _, err := svc.db.Exec(`UPDATE claim_tokens SET used_at = ? WHERE 1`, formatTime(time.Now())); err != nil {
			t.Error(err)
		}
		if _, err := svc.db.Exec(`UPDATE appliance SET state = 'CLAIMED' WHERE id = 1`); err != nil {
			t.Error(err)
		}
		_ = tf
	}
	defer func() { afterTokenWrite = nil }()
	expiry, err := svc.EnsureTokenFile(path, TokenTTL)
	if err != nil || !expiry.IsZero() {
		t.Fatalf("expiry = %v, err = %v; want zero, nil", expiry, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file left after claim race: %v", err)
	}
}

func TestEnsureWriteFailureReusesMintedToken(t *testing.T) {
	_, svc := openClaimStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "claim-token")
	for i := 0; i < 3; i++ {
		if _, err := svc.EnsureTokenFile(path, TokenTTL); err == nil {
			t.Fatal("expected a write failure")
		}
	}
	var n int
	var id int64
	svc.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(id),0) FROM claim_tokens`).Scan(&n, &id)
	if n != 1 || id != 1 {
		t.Fatalf("rows = %d, max id = %d; want one row minted once", n, id)
	}
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnsureTokenFile(path, TokenTTL); err != nil {
		t.Fatal(err)
	}
	svc.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(id),0) FROM claim_tokens`).Scan(&n, &id)
	if n != 1 || id != 1 {
		t.Fatalf("recovery re-minted: rows = %d, max id = %d", n, id)
	}
}

func TestMaintainLogsFirstFailureOnceThenRecovery(t *testing.T) {
	_, svc := openClaimStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "claim-token")
	old := retryEvery
	retryEvery = 20 * time.Millisecond
	defer func() { retryEvery = old }()
	var mu sync.Mutex
	var errs, recoveries int
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.MaintainTokenFile(ctx, path, TokenTTL, func(err error) {
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				recoveries++
			} else {
				errs++
			}
		})
		close(done)
	}()
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if errs != 1 {
		t.Fatalf("failure logged %d times, want once", errs)
	}
	mu.Unlock()
	os.Mkdir(filepath.Dir(path), 0o700)
	for i := 0; i < 100; i++ {
		mu.Lock()
		r := recoveries
		mu.Unlock()
		if r == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if recoveries != 1 || errs != 1 {
		t.Fatalf("errs = %d, recoveries = %d; want 1 and 1", errs, recoveries)
	}
}

func TestMaintainReportsRemoveFailureWithoutToken(t *testing.T) {
	_, svc := openClaimStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "claim-token")
	var got []error
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.MaintainTokenFile(ctx, path, TokenTTL, func(err error) {
			mu.Lock()
			got = append(got, err)
			mu.Unlock()
		})
		close(done)
	}()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	tf, _ := ReadTokenFile(path)
	// Make removal fail: replace the file with a non-empty directory.
	os.Remove(path)
	os.Mkdir(path, 0o700)
	os.WriteFile(filepath.Join(path, "x"), []byte("x"), 0o600)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatal("remove failure was not reported")
	}
	for _, err := range got {
		if err != nil && strings.Contains(err.Error(), tf.Token) {
			t.Fatalf("error leaks the token: %v", err)
		}
	}
}
