package auth

import (
	"strings"
	"testing"
	"time"
)

func sessionRig(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	svc := claimedAuthService(t)
	if _, err := svc.SetupAdmin("owner", "long-enough-password"); err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	svc.now = func() time.Time { return clock }
	return svc, &clock
}

func TestSessionIdleExpiry(t *testing.T) {
	svc, clock := sessionRig(t)
	sess, err := svc.Login("owner", "long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(IdleTTL - time.Minute)
	if _, err := svc.Verify(sess.Token); err != nil {
		t.Fatalf("inside idle window: %v", err)
	}
	*clock = clock.Add(IdleTTL + time.Minute)
	if _, err := svc.Verify(sess.Token); err != ErrInvalidSession {
		t.Fatalf("after idle window = %v, want ErrInvalidSession", err)
	}
	if _, err := svc.Verify(sess.Token); err != ErrInvalidSession {
		t.Fatalf("expired session must stay dead: %v", err)
	}
}

func TestSessionIdleRefreshesOnUseButAbsoluteDoesNot(t *testing.T) {
	svc, clock := sessionRig(t)
	sess, err := svc.Login("owner", "long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	start := *clock
	// Use it every 6 hours: idle never lapses, absolute eventually does.
	for elapsed := 6 * time.Hour; elapsed < AbsoluteTTL; elapsed += 6 * time.Hour {
		*clock = start.Add(elapsed)
		v, err := svc.Verify(sess.Token)
		if err != nil {
			t.Fatalf("at +%v: %v", elapsed, err)
		}
		if !v.ExpiresAt.Equal(start.Add(AbsoluteTTL)) {
			t.Fatalf("absolute expiry moved: %v", v.ExpiresAt)
		}
	}
	*clock = start.Add(AbsoluteTTL + time.Second)
	if _, err := svc.Verify(sess.Token); err != ErrInvalidSession {
		t.Fatalf("after absolute lifetime = %v, want ErrInvalidSession", err)
	}
}

func TestSessionLegacyThirtyDayRowIsCapped(t *testing.T) {
	svc, clock := sessionRig(t)
	sess, err := svc.Login("owner", "long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	legacy := formatTime(clock.Add(30 * 24 * time.Hour))
	if _, err := svc.db.Exec(`UPDATE sessions SET expires_at = ?`, legacy); err != nil {
		t.Fatal(err)
	}
	for d := 12 * time.Hour; d <= AbsoluteTTL; d += 12 * time.Hour {
		*clock = clock.Add(11 * time.Hour)
		if _, err := svc.Verify(sess.Token); err != nil {
			t.Fatalf("keepalive: %v", err)
		}
	}
	*clock = clock.Add(24 * time.Hour)
	if _, err := svc.Verify(sess.Token); err != ErrInvalidSession {
		t.Fatalf("legacy 30-day row outlived the 7-day cap: %v", err)
	}
}

func TestSessionCSRFToken(t *testing.T) {
	svc, _ := sessionRig(t)
	sess, err := svc.Login("owner", "long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	if sess.CSRFToken == "" || sess.CSRFToken == sess.Token {
		t.Fatalf("csrf token %q must be distinct and non-empty", sess.CSRFToken)
	}
	v, err := svc.Verify(sess.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !v.CSRFMatches(sess.CSRFToken) {
		t.Fatal("own csrf token rejected")
	}
	for _, bad := range []string{"", "nope", sess.Token, sess.CSRFToken + "x"} {
		if v.CSRFMatches(bad) {
			t.Fatalf("csrf %q accepted", bad)
		}
	}
	other, _ := svc.Login("owner", "long-enough-password")
	if v.CSRFMatches(other.CSRFToken) {
		t.Fatal("another session's csrf token accepted")
	}
	// Neither secret is stored in the clear.
	var n int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = CAST(? AS BLOB) OR csrf_hash = CAST(? AS BLOB)`,
		sess.Token, sess.CSRFToken).Scan(&n); err != nil || n != 0 {
		t.Fatalf("plaintext stored: n=%d err=%v", n, err)
	}
	if (VerifiedSession{}).CSRFMatches("x") {
		t.Fatal("session without csrf hash matched")
	}
}

func TestPasswordChangeRevocation(t *testing.T) {
	svc, _ := sessionRig(t)
	a, _ := svc.Login("owner", "long-enough-password")
	b, _ := svc.Login("owner", "long-enough-password")
	c, _ := svc.Login("owner", "long-enough-password")
	if _, err := svc.ChangePasswordKeepingSession("owner", "long-enough-password", "replacement-password-long", b.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(b.Token); err != nil {
		t.Fatalf("caller's session must survive: %v", err)
	}
	for _, tok := range []string{a.Token, c.Token} {
		if _, err := svc.Verify(tok); err != ErrInvalidSession {
			t.Fatalf("other session survived password change: %v", err)
		}
	}
	// Plain ChangePassword (and recovery) still revoke everything.
	if _, err := svc.ChangePassword("owner", "replacement-password-long", "another-long-password-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(b.Token); err != ErrInvalidSession {
		t.Fatalf("ChangePassword left a session: %v", err)
	}
}

func TestLoginSweepsExpiredSessions(t *testing.T) {
	svc, clock := sessionRig(t)
	if _, err := svc.Login("owner", "long-enough-password"); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(AbsoluteTTL + time.Hour)
	if _, err := svc.Login("owner", "long-enough-password"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("sessions after sweep = %d (%v), want 1", n, err)
	}
}

func TestVerifyUsesTokenHashIndex(t *testing.T) {
	svc, _ := sessionRig(t)
	rows, err := svc.db.Query(`EXPLAIN QUERY PLAN SELECT s.id FROM sessions s JOIN identities i ON i.id = s.identity_id WHERE s.token_hash = ?`, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, "sessions_token_hash_unique") {
		t.Fatalf("Verify lookup is not index-driven: %s", joined)
	}
}
