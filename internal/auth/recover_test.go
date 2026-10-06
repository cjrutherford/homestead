package auth

import (
	"path/filepath"
	"testing"
	"time"

	"homestead/internal/claim"
	"homestead/internal/store"

	"golang.org/x/crypto/bcrypt"
)

func recoverService(t *testing.T) *Service {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	claimSvc := claim.New(s)
	token, err := claimSvc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := claimSvc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	svc := New(s, claimSvc)
	if _, err := svc.SetupAdmin("owner", "long-enough-password"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	return svc
}

func TestRecoverResetsPassword(t *testing.T) {
	svc := recoverService(t)
	if _, err := svc.Recover("owner", "brand-new-password"); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if _, err := svc.Login("owner", "long-enough-password"); err != ErrInvalidCredentials {
		t.Fatalf("old password Login = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Login("owner", "brand-new-password"); err != nil {
		t.Fatalf("new password Login: %v", err)
	}
}

func TestRecoverUnknownUser(t *testing.T) {
	svc := recoverService(t)
	if _, err := svc.Recover("ghost", "brand-new-password"); err != ErrAdminNotFound {
		t.Fatalf("Recover = %v, want ErrAdminNotFound", err)
	}
}

func TestRecoverValidatesInput(t *testing.T) {
	svc := recoverService(t)
	if _, err := svc.Recover("Bad!", "brand-new-password"); err != ErrBadUsername {
		t.Fatalf("Recover = %v, want ErrBadUsername", err)
	}
	if _, err := svc.Recover("owner", "short"); err != ErrWeakPassword {
		t.Fatalf("Recover = %v, want ErrWeakPassword", err)
	}
}

func TestRecoverIgnoresClaimState(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	hash, err := bcrypt.GenerateFromPassword([]byte("old-password-here"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	now := formatTime(time.Now())
	if _, err := s.DB().Exec(
		`INSERT INTO identities (username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"owner", hash, now, now,
	); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	svc := New(s, claim.New(s))
	state, err := claim.New(s).CurrentState()
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state != claim.StateUnclaimed {
		t.Fatalf("state = %q, want UNCLAIMED for this test", state)
	}
	if _, err := svc.Recover("owner", "recovered-password"); err != nil {
		t.Fatalf("Recover unclaimed: %v", err)
	}
	if _, err := svc.Login("owner", "recovered-password"); err != nil {
		t.Fatalf("Login after recover: %v", err)
	}
}
