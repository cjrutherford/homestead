package auth

import (
	"errors"
	"testing"
)

func personRig(t *testing.T, state string) *Service {
	t.Helper()
	svc := claimedAuthService(t)
	if _, err := svc.SetupAdmin("owner", oidcPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO people (id, display_name, username, email, authentik_pk, state, origin, created_by, created_at)
		VALUES (3, 'Sam', 'sam', '', 9, ?, 'created', 'o', '2026-01-01T00:00:00Z')`, state); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestPersonSessionVerifiesAsAPersonWithoutAnIdentity(t *testing.T) {
	svc := personRig(t, "active")
	sess, err := svc.MintPersonSession(3)
	if err != nil {
		t.Fatal(err)
	}
	v, err := svc.Verify(sess.Token)
	if err != nil || v.Role != RolePerson || v.PersonID != 3 || v.DisplayName != "Sam" || v.Method != MethodOIDC {
		t.Fatalf("Verify = %+v, %v", v, err)
	}
	// An owner session is still the owner.
	pw, _ := svc.Login("owner", oidcPassword)
	if o, err := svc.Verify(pw.Token); err != nil || o.Role != RoleOwner || o.PersonID != 0 {
		t.Fatalf("owner = %+v, %v", o, err)
	}
}

func TestPersonSessionRefusesInactiveAndUnknownPeople(t *testing.T) {
	for _, state := range []string{"disabled", "missing"} {
		svc := personRig(t, state)
		if _, err := svc.MintPersonSession(3); err == nil {
			t.Fatalf("%s person got a session", state)
		}
	}
	svc := personRig(t, "active")
	if _, err := svc.MintPersonSession(99); err == nil {
		t.Fatal("unknown person got a session")
	}
}

func TestPersonSessionDiesWithThePerson(t *testing.T) {
	svc := personRig(t, "active")
	sess, _ := svc.MintPersonSession(3)
	if _, err := svc.db.Exec(`UPDATE people SET state = 'disabled' WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(sess.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Verify = %v", err)
	}
	var n int
	_ = svc.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE person_id = 3`).Scan(&n)
	if n != 0 {
		t.Fatal("the dead session row was kept")
	}
}

func TestRevokePersonSessions(t *testing.T) {
	svc := personRig(t, "active")
	_, _ = svc.db.Exec(`INSERT INTO people (id, display_name, username, email, authentik_pk, state, origin, created_by, created_at)
		VALUES (4, 'Kim', 'kim', '', 10, 'active', 'created', 'o', '2026-01-01T00:00:00Z')`)
	a, _ := svc.MintPersonSession(3)
	b, _ := svc.MintPersonSession(4)
	pw, _ := svc.Login("owner", oidcPassword)
	svc.RevokePersonSessions(3)
	if _, err := svc.Verify(a.Token); err == nil {
		t.Fatal("person 3 kept a session")
	}
	if _, err := svc.Verify(b.Token); err != nil {
		t.Fatalf("person 4 lost a session: %v", err)
	}
	svc.RevokePersonSessions(0)
	if _, err := svc.Verify(b.Token); err == nil {
		t.Fatal("revoke-all left a person session")
	}
	if _, err := svc.Verify(pw.Token); err != nil {
		t.Fatalf("owner session revoked: %v", err)
	}
}
