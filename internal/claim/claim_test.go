package claim

import (
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"homestead/internal/store"
)

func openClaimStore(t *testing.T) (*store.Store, *Service) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, New(s)
}

func TestFreshApplianceIsUnclaimed(t *testing.T) {
	_, svc := openClaimStore(t)
	state, err := svc.CurrentState()
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state != StateUnclaimed {
		t.Fatalf("state = %q, want UNCLAIMED", state)
	}
}

func TestMintClaimRoundTrip(t *testing.T) {
	_, svc := openClaimStore(t)
	token, err := svc.MintToken(TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if token == "" {
		t.Fatal("empty token")
	}
	live, err := svc.LiveToken()
	if err != nil || !live {
		t.Fatalf("LiveToken = %v, %v; want true", live, err)
	}
	if err := svc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	state, err := svc.CurrentState()
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state != StateClaimed {
		t.Fatalf("state = %q, want CLAIMED", state)
	}
}

func TestTokenIsSingleUse(t *testing.T) {
	_, svc := openClaimStore(t)
	token, err := svc.MintToken(TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := svc.Claim(token); err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if err := svc.Claim(token); err == nil {
		t.Fatal("second Claim succeeded, want failure")
	}
}

func TestUnknownTokenRejected(t *testing.T) {
	_, svc := openClaimStore(t)
	if _, err := svc.MintToken(TokenTTL); err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := svc.Claim("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err != ErrTokenInvalid {
		t.Fatalf("Claim = %v, want ErrTokenInvalid", err)
	}
	if err := svc.Claim(""); err != ErrTokenInvalid {
		t.Fatalf("empty Claim = %v, want ErrTokenInvalid", err)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	_, svc := openClaimStore(t)
	token, err := svc.MintToken(-time.Minute)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := svc.Claim(token); err != ErrTokenExpired {
		t.Fatalf("Claim = %v, want ErrTokenExpired", err)
	}
	state, err := svc.CurrentState()
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state != StateUnclaimed {
		t.Fatalf("state = %q, want still UNCLAIMED", state)
	}
}

func TestMintOnClaimedFails(t *testing.T) {
	_, svc := openClaimStore(t)
	token, err := svc.MintToken(TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := svc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := svc.MintToken(TokenTTL); err != ErrAlreadyClaimed {
		t.Fatalf("MintToken = %v, want ErrAlreadyClaimed", err)
	}
}

func TestPlaintextNeverStored(t *testing.T) {
	db, svc := openClaimStore(t)
	token, err := svc.MintToken(TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	var hash []byte
	if err := db.DB().QueryRow(`SELECT token_hash FROM claim_tokens`).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	sum := sha256.Sum256([]byte(token))
	if string(hash) != string(sum[:]) {
		t.Fatal("stored hash does not match token SHA-256")
	}
	var count int
	if err := db.DB().QueryRow(
		`SELECT COUNT(*) FROM claim_tokens WHERE CAST(token_hash AS TEXT) LIKE '%' || ? || '%'`, token,
	).Scan(&count); err != nil {
		t.Fatalf("plaintext probe: %v", err)
	}
	if count != 0 {
		t.Fatal("plaintext token found in database")
	}
}

func TestExpiredTokensPrunedOnMint(t *testing.T) {
	_, svc := openClaimStore(t)
	if _, err := svc.MintToken(-time.Minute); err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if _, err := svc.MintToken(TokenTTL); err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	live, err := svc.LiveToken()
	if err != nil || !live {
		t.Fatalf("LiveToken = %v, %v; want true", live, err)
	}
}
