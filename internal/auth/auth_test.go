package auth

import (
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/claim"
	"homestead/internal/store"
)

func openAuthService(t *testing.T) *Service {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s, claim.New(s))
}

func claimedAuthService(t *testing.T) *Service {
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
	return New(s, claimSvc)
}

func TestSetupRequiresClaim(t *testing.T) {
	svc := openAuthService(t)
	if _, err := svc.SetupAdmin("owner", "long-enough-password"); err != ErrNotClaimed {
		t.Fatalf("SetupAdmin = %v, want ErrNotClaimed", err)
	}
}

func TestSetupValidatesInput(t *testing.T) {
	svc := claimedAuthService(t)
	if _, err := svc.SetupAdmin("Bad Name!", "long-enough-password"); err != ErrBadUsername {
		t.Fatalf("SetupAdmin = %v, want ErrBadUsername", err)
	}
	if _, err := svc.SetupAdmin("owner", "short"); err != ErrWeakPassword {
		t.Fatalf("SetupAdmin = %v, want ErrWeakPassword", err)
	}
}

func TestSetupExactlyOnce(t *testing.T) {
	svc := claimedAuthService(t)
	admin, err := svc.SetupAdmin("owner", "long-enough-password")
	if err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	if admin.ID == 0 || admin.Username != "owner" {
		t.Fatalf("Admin = %+v, want id and username", admin)
	}
	if _, err := svc.SetupAdmin("second", "another-long-password"); err != ErrAdminExists {
		t.Fatalf("second SetupAdmin = %v, want ErrAdminExists", err)
	}
}

func TestLoginRoundTrip(t *testing.T) {
	svc := claimedAuthService(t)
	if _, err := svc.SetupAdmin("owner", "long-enough-password"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	session, err := svc.Login("owner", "long-enough-password")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if session.Token == "" {
		t.Fatal("empty session token")
	}
	verified, err := svc.Verify(session.Token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.Username != "owner" {
		t.Fatalf("verified username = %q, want owner", verified.Username)
	}
	if err := svc.Logout(session.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := svc.Verify(session.Token); err != ErrInvalidSession {
		t.Fatalf("Verify after logout = %v, want ErrInvalidSession", err)
	}
}

func TestLoginHidesReason(t *testing.T) {
	svc := claimedAuthService(t)
	if _, err := svc.SetupAdmin("owner", "long-enough-password"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	for _, tc := range [][2]string{
		{"nobody", "long-enough-password"},
		{"owner", "wrong-password-here"},
	} {
		if _, err := svc.Login(tc[0], tc[1]); err != ErrInvalidCredentials {
			t.Fatalf("Login(%q) = %v, want ErrInvalidCredentials", tc[0], err)
		}
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	svc := claimedAuthService(t)
	for _, token := range []string{"", "bogus"} {
		if _, err := svc.Verify(token); err != ErrInvalidSession {
			t.Fatalf("Verify(%q) = %v, want ErrInvalidSession", token, err)
		}
	}
	if err := svc.Logout("bogus"); err != ErrInvalidSession {
		t.Fatalf("Logout(bogus) = %v, want ErrInvalidSession", err)
	}
}

func TestPasswordStoredHashed(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	claimSvc := claim.New(s)
	token, err := claimSvc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := claimSvc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := New(s, claimSvc).SetupAdmin("owner", "long-enough-password"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	var hash []byte
	if err := s.DB().QueryRow(`SELECT password_hash FROM identities WHERE username = 'owner'`).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if string(hash) == "long-enough-password" || !strings.HasPrefix(string(hash), "$2a$") {
		t.Fatal("password_hash is not a bcrypt hash")
	}
}
