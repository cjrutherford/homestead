package auth

import (
	"strings"
	"testing"
	"time"
)

const (
	hardeningPassword    = "long-enough-password"
	hardeningReplacement = "replacement-password-long"
)

// setupOwner creates the first administrator for hardening tests.
func setupOwner(t *testing.T, svc *Service) {
	t.Helper()
	if _, err := svc.SetupAdmin("owner", hardeningPassword); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
}

func TestChangePasswordRoundTrip(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	before, err := svc.Login("owner", hardeningPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := svc.ChangePassword("owner", hardeningPassword, hardeningReplacement); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := svc.Login("owner", hardeningPassword); err != ErrInvalidCredentials {
		t.Fatalf("old password Login = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Login("owner", hardeningReplacement); err != nil {
		t.Fatalf("new password Login: %v", err)
	}
	// Pre-rotation sessions die at the boundary.
	if _, err := svc.Verify(before.Token); err != ErrInvalidSession {
		t.Fatalf("old session Verify = %v, want ErrInvalidSession", err)
	}
}

func TestChangePasswordRejects(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	if _, err := svc.ChangePassword("owner", "wrong-current-password", hardeningReplacement); err != ErrInvalidCredentials {
		t.Fatalf("wrong current = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.ChangePassword("owner", hardeningPassword, "short"); err != ErrWeakPassword {
		t.Fatalf("weak replacement = %v, want ErrWeakPassword", err)
	}
	if _, err := svc.ChangePassword("ghost", hardeningPassword, hardeningReplacement); err != ErrInvalidCredentials {
		t.Fatalf("unknown user = %v, want ErrInvalidCredentials", err)
	}
	// Rejections change nothing: the original credential still works.
	if _, err := svc.Login("owner", hardeningPassword); err != nil {
		t.Fatalf("Login after rejections: %v", err)
	}
}

// failLogins burns wrong-password attempts, returning the last error.
func failLogins(t *testing.T, svc *Service, username string, count int) error {
	t.Helper()
	var err error
	for i := 0; i < count; i++ {
		_, err = svc.Login(username, "wrong-password-long")
	}
	return err
}

func TestLoginLockoutTrips(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	if err := failLogins(t, svc, "owner", MaxLoginFailures-1); err != ErrInvalidCredentials {
		t.Fatalf("pre-threshold = %v, want ErrInvalidCredentials", err)
	}
	if err := failLogins(t, svc, "owner", 1); err != ErrAccountLocked {
		t.Fatalf("threshold trip = %v, want ErrAccountLocked", err)
	}
	// The right password does not open a locked account.
	if _, err := svc.Login("owner", hardeningPassword); err != ErrAccountLocked {
		t.Fatalf("locked Login = %v, want ErrAccountLocked", err)
	}
}

func TestLoginLockoutExpires(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	if err := failLogins(t, svc, "owner", MaxLoginFailures); err != ErrAccountLocked {
		t.Fatalf("trip = %v, want ErrAccountLocked", err)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	if _, err := svc.db.Exec(`UPDATE login_attempts SET locked_until = ? WHERE username = ?`, past, "owner"); err != nil {
		t.Fatalf("expire lockout: %v", err)
	}
	if _, err := svc.Login("owner", hardeningPassword); err != nil {
		t.Fatalf("Login after expiry: %v", err)
	}
}

func TestLoginLockoutUniformForUnknown(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	// Unknown names lock exactly like known ones: lockout never
	// confirms an account exists.
	if err := failLogins(t, svc, "ghost", MaxLoginFailures-1); err != ErrInvalidCredentials {
		t.Fatalf("pre-threshold = %v, want ErrInvalidCredentials", err)
	}
	if err := failLogins(t, svc, "ghost", 1); err != ErrAccountLocked {
		t.Fatalf("threshold trip = %v, want ErrAccountLocked", err)
	}
}

func TestLoginSuccessClearsFailures(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	if err := failLogins(t, svc, "owner", MaxLoginFailures-1); err != ErrInvalidCredentials {
		t.Fatalf("failures = %v", err)
	}
	if _, err := svc.Login("owner", hardeningPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// The slate is clean: a fresh run of failures starts counting over.
	if err := failLogins(t, svc, "owner", MaxLoginFailures-1); err != ErrInvalidCredentials {
		t.Fatalf("post-success failures = %v, want ErrInvalidCredentials", err)
	}
}

func TestChangePasswordFeedsLockout(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	var err error
	for i := 0; i < MaxLoginFailures; i++ {
		_, err = svc.ChangePassword("owner", "wrong-current-password", hardeningReplacement)
	}
	if err != ErrAccountLocked {
		t.Fatalf("rotation guesses = %v, want ErrAccountLocked", err)
	}
	if _, err := svc.ChangePassword("owner", hardeningPassword, hardeningReplacement); err != ErrAccountLocked {
		t.Fatalf("rotation while locked = %v, want ErrAccountLocked", err)
	}
}

func TestRecoverRevokesSessions(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	first, err := svc.Login("owner", hardeningPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	second, err := svc.Login("owner", hardeningPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := svc.Recover("owner", hardeningReplacement); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	for _, token := range []string{first.Token, second.Token} {
		if _, err := svc.Verify(token); err != ErrInvalidSession {
			t.Fatalf("pre-recovery Verify = %v, want ErrInvalidSession", err)
		}
	}
	if _, err := svc.Login("owner", hardeningReplacement); err != nil {
		t.Fatalf("Login after recovery: %v", err)
	}
}

func TestRecoverClearsLockout(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	if err := failLogins(t, svc, "owner", MaxLoginFailures); err != ErrAccountLocked {
		t.Fatalf("trip = %v, want ErrAccountLocked", err)
	}
	// Socket possession overrides the throttle: recovery resets the
	// whole authentication state, lockout included.
	if _, err := svc.Recover("owner", hardeningReplacement); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if _, err := svc.Login("owner", hardeningReplacement); err != nil {
		t.Fatalf("Login after recovery: %v", err)
	}
}

func TestPasswordMaxBytes(t *testing.T) {
	svc := claimedAuthService(t)
	tooLong := strings.Repeat("a", MaxPasswordLength+1)
	atLimit := strings.Repeat("a", MaxPasswordLength)
	// Multibyte: 37 two-byte runes is 74 bytes though only 37 characters.
	multibyte := strings.Repeat("é", 37)
	for _, pw := range []string{tooLong, multibyte} {
		if _, err := svc.SetupAdmin("owner", pw); err != ErrPasswordTooLong {
			t.Fatalf("SetupAdmin(%d bytes) = %v, want ErrPasswordTooLong", len(pw), err)
		}
	}
	setupOwner(t, svc)
	// Refused before the current password is checked and without a failure.
	if _, err := svc.ChangePassword("owner", "wrong-current-password", tooLong); err != ErrPasswordTooLong {
		t.Fatalf("ChangePassword too long = %v", err)
	}
	if info, _ := svc.LockoutDetail("owner"); info.Failures != 0 {
		t.Fatalf("too-long password counted as failure: %+v", info)
	}
	if _, err := svc.ChangePassword("owner", hardeningPassword, atLimit); err != nil {
		t.Fatalf("72-byte password: %v", err)
	}
	if _, err := svc.Recover("owner", tooLong); err != ErrPasswordTooLong {
		t.Fatalf("Recover too long = %v", err)
	}
}

func TestLockoutDetail(t *testing.T) {
	svc := claimedAuthService(t)
	setupOwner(t, svc)
	if info, err := svc.LockoutDetail("owner"); err != nil || info.Locked || info.Failures != 0 {
		t.Fatalf("fresh = %+v, %v", info, err)
	}
	for range 2 {
		_, _ = svc.Login("owner", "wrong-password-here")
	}
	if info, _ := svc.LockoutDetail("owner"); info.Locked || info.Failures != 2 {
		t.Fatalf("after 2 failures = %+v", info)
	}
	for range MaxLoginFailures {
		_, _ = svc.Login("owner", "wrong-password-here")
	}
	info, _ := svc.LockoutDetail("owner")
	if !info.Locked || !info.LockedUntil.After(time.Now()) || info.Failures == 0 {
		t.Fatalf("locked = %+v", info)
	}
}
