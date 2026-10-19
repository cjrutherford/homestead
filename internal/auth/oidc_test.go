package auth

import (
	"errors"
	"testing"
)

const oidcPassword = "long-enough-password"

func TestOIDCSessionRecordsItsMethodAndPasswordSessionsStayPassword(t *testing.T) {
	svc := claimedAuthService(t)
	admin, err := svc.SetupAdmin("owner", oidcPassword)
	if err != nil {
		t.Fatal(err)
	}
	pw, err := svc.Login("owner", oidcPassword)
	if err != nil {
		t.Fatal(err)
	}
	oidc, err := svc.MintOIDCSession(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	for token, want := range map[string]string{pw.Token: MethodPassword, oidc.Token: MethodOIDC} {
		v, err := svc.Verify(token)
		if err != nil || v.Method != want {
			t.Fatalf("Verify = %+v, %v; want method %s", v, err, want)
		}
	}
	// Both are ordinary sessions: same TTLs and a CSRF token.
	if oidc.CSRFToken == "" || oidc.ExpiresAt.Sub(pw.ExpiresAt).Abs().Seconds() > 5 {
		t.Fatalf("oidc session differs from a password session: %+v vs %+v", oidc, pw)
	}
}

func TestMintOIDCSessionIgnoresAndPreservesLockout(t *testing.T) {
	svc := claimedAuthService(t)
	admin, _ := svc.SetupAdmin("owner", oidcPassword)
	for i := 0; i < MaxLoginFailures; i++ {
		_, _ = svc.Login("owner", "wrong-password-value")
	}
	if locked, _ := svc.Locked("owner"); !locked {
		t.Fatal("expected the local account to be locked")
	}
	if _, err := svc.MintOIDCSession(admin.ID); err != nil {
		t.Fatalf("a locked local account must not block SSO: %v", err)
	}
	if locked, _ := svc.Locked("owner"); !locked {
		t.Fatal("SSO success must not clear local lockout")
	}
	if _, err := svc.MintOIDCSession(admin.ID + 99); err == nil {
		t.Fatal("minting for an unknown identity must fail")
	}
}

func TestRevokeMethodSessionsKeepsOtherMethods(t *testing.T) {
	svc := claimedAuthService(t)
	admin, _ := svc.SetupAdmin("owner", oidcPassword)
	pw, _ := svc.Login("owner", oidcPassword)
	oidc, _ := svc.MintOIDCSession(admin.ID)
	if err := svc.RevokeMethodSessions(admin.ID, MethodOIDC); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(oidc.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("oidc session after revoke = %v", err)
	}
	if _, err := svc.Verify(pw.Token); err != nil {
		t.Fatalf("password session was revoked too: %v", err)
	}
}

func TestVerifyPasswordFeedsLockoutAndDoesNotForgive(t *testing.T) {
	svc := claimedAuthService(t)
	_, _ = svc.SetupAdmin("owner", oidcPassword)
	if err := svc.VerifyPassword("owner", oidcPassword); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyPassword("owner", "wrong-password-value"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v", err)
	}
	// A correct re-auth does not erase the earlier failure.
	if err := svc.VerifyPassword("owner", oidcPassword); err != nil {
		t.Fatal(err)
	}
	info, _ := svc.LockoutDetail("owner")
	if info.Failures != 1 {
		t.Fatalf("failures = %d, want the one wrong guess kept", info.Failures)
	}
	var last error
	for i := 0; i < MaxLoginFailures; i++ {
		last = svc.VerifyPassword("owner", "wrong-password-value")
	}
	if !errors.Is(last, ErrAccountLocked) {
		t.Fatalf("repeated wrong re-auth = %v, want lockout", last)
	}
	if err := svc.VerifyPassword("owner", oidcPassword); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("locked re-auth = %v", err)
	}
}
