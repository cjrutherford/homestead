package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/claim"
	"homestead/internal/store"
)

// The daemon must never print the plaintext token (S288): not at start and
// not when the background timer mints a replacement.
func TestStartClaimTokenNeverPrintsPlaintext(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := claim.New(st)
	path := filepath.Join(t.TempDir(), "claim-token")
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := startClaimToken(ctx, svc, path, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	tf, err := claim.ReadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "homesteadctl claim token") {
		t.Fatalf("stdout = %q, want the command that shows the token", out.String())
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("stdout = %q, want exactly one line", out.String())
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(out.String()+errOut.String(), tf.Token) {
		t.Fatal("plaintext claim token reached stdout or stderr")
	}
}

func TestStartClaimTokenClaimedRemovesFile(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := claim.New(st)
	path := filepath.Join(t.TempDir(), "claim-token")
	if _, err := svc.EnsureTokenFile(path, claim.TokenTTL); err != nil {
		t.Fatal(err)
	}
	tf, _ := claim.ReadTokenFile(path)
	if err := svc.Claim(tf.Token); err != nil {
		t.Fatal(err)
	}
	if err := claim.WriteTokenFile(path, "stale", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := startClaimToken(context.Background(), svc, path, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("claimed boot printed %q", out.String())
	}
	if _, err := claim.ReadTokenFile(path); err == nil {
		t.Fatal("stale token file survived a claimed boot")
	}
}
