package people

import (
	"testing"

	"homestead/internal/sso"
)

// S338 (ADR-050 §1, §3): matching a sign-in subject to a person, and the
// dashboard sign-in group.

func (r *rig) inPeopleGroup(pk int) bool { return r.f.member("homestead-people", pk) }

func TestSignInMatchesTheStoredUidOnly(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	uid := r.f.users[p.pk].UID
	id, user, name, ok, err := r.svc.SignIn(bg, uid)
	if err != nil || !ok || id != p.ID || user != "ada" || name != "Ada Lovelace" {
		t.Fatalf("sign-in = %d %q %q %v %v", id, user, name, ok, err)
	}
	// Neither the username, nor the email, nor an unknown subject matches.
	for _, sub := range []string{"ada", "ada@example.test", "uid-999", "", "owner-sub"} {
		if _, _, _, ok, err := r.svc.SignIn(bg, sub); ok || err != nil {
			t.Fatalf("subject %q matched (%v, %v)", sub, ok, err)
		}
	}
	var stored string
	_ = r.db.QueryRow(`SELECT authentik_uid FROM people WHERE id = ?`, p.ID).Scan(&stored)
	if stored != uid {
		t.Fatalf("stored uid = %q, want %q", stored, uid)
	}
}

func TestDisabledAndRemovedPeopleCannotSignIn(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	uid := r.f.users[p.pk].UID
	var ended []int64
	r.svc.EndSessions = func(id int64) { ended = append(ended, id) }
	if err := r.svc.SetActive(bg, "owner", p.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok, _ := r.svc.SignIn(bg, uid); ok {
		t.Fatal("a disabled person signed in")
	}
	if len(ended) != 1 || ended[0] != p.ID {
		t.Fatalf("sessions ended for %v", ended)
	}
	if err := r.svc.SetActive(bg, "owner", p.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok, _ := r.svc.SignIn(bg, uid); !ok {
		t.Fatal("an enabled person could not sign in")
	}
	if err := r.svc.Remove(bg, "owner", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok, _ := r.svc.SignIn(bg, uid); ok {
		t.Fatal("a removed person signed in")
	}
	if len(ended) != 2 {
		t.Fatalf("remove must end sessions: %v", ended)
	}
}

func TestOlderRecordsAreMatchedThroughAuthentikByPKAndRemembered(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	uid := r.f.users[p.pk].UID
	// A record from before S338 has no uid.
	if _, err := r.db.Exec(`UPDATE people SET authentik_uid = ''`); err != nil {
		t.Fatal(err)
	}
	// Another Authentik user's subject does not match, and nothing is guessed.
	if _, _, _, ok, _ := r.svc.SignIn(bg, "uid-other"); ok {
		t.Fatal("matched a stranger")
	}
	id, _, _, ok, err := r.svc.SignIn(bg, uid)
	if err != nil || !ok || id != p.ID {
		t.Fatalf("fallback sign-in = %d %v %v", id, ok, err)
	}
	var stored string
	_ = r.db.QueryRow(`SELECT authentik_uid FROM people WHERE id = ?`, p.ID).Scan(&stored)
	if stored != uid {
		t.Fatalf("uid not remembered: %q", stored)
	}
	// Without the kept token the fallback cannot match.
	_, _ = r.db.Exec(`UPDATE people SET authentik_uid = ''`)
	r.kept = false
	if _, _, _, ok, _ := r.svc.SignIn(bg, uid); ok {
		t.Fatal("matched without a way to ask Authentik")
	}
}

func TestListRemembersAMissingUidButNeverReplacesOne(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	uid := r.f.users[p.pk].UID
	_, _ = r.db.Exec(`UPDATE people SET authentik_uid = ''`)
	_, _ = r.svc.List(bg)
	var stored string
	_ = r.db.QueryRow(`SELECT authentik_uid FROM people WHERE id = ?`, p.ID).Scan(&stored)
	if stored != uid {
		t.Fatalf("read-back kept %q", stored)
	}
	r.f.users[p.pk].UID = "changed"
	_, _ = r.svc.List(bg)
	_ = r.db.QueryRow(`SELECT authentik_uid FROM people WHERE id = ?`, p.ID).Scan(&stored)
	if stored != uid {
		t.Fatalf("a stored uid was replaced: %q", stored)
	}
}

func TestPeopleGroupMembershipFollowsAddDisableEnableRemove(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	if !r.inPeopleGroup(p.pk) {
		t.Fatal("an added person joins homestead-people")
	}
	if err := r.svc.SetActive(bg, "owner", p.ID, false); err != nil || r.inPeopleGroup(p.pk) {
		t.Fatalf("disable: %v, still in group %v", err, r.inPeopleGroup(p.pk))
	}
	if err := r.svc.SetActive(bg, "owner", p.ID, true); err != nil || !r.inPeopleGroup(p.pk) {
		t.Fatalf("enable: %v", err)
	}
	// An adopted person joins and is released from it on removal.
	a, err := r.svc.Adopt(bg, "owner", "cousin")
	if err != nil || !r.inPeopleGroup(a.pk) {
		t.Fatalf("adopt: %v", err)
	}
	if err := r.svc.Remove(bg, "owner", a.ID); err != nil || r.inPeopleGroup(a.pk) {
		t.Fatalf("release: %v", err)
	}
	if err := r.svc.Remove(bg, "owner", p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPeopleGroupMayNotExistYet(t *testing.T) {
	r := newRig(t)
	delete(r.f.groups, "homestead-people")
	p, _ := r.add("ada") // sign-in not re-planned yet: no warning, no failure
	if err := r.svc.SetActive(bg, "owner", p.ID, false); err != nil {
		t.Fatalf("disable before the group exists: %v", err)
	}
	// Applying the plan creates the group; SyncGroup fills it.
	r.f.groups["homestead-people"] = nil
	q, _ := r.add("bob")
	if err := r.svc.SyncGroup(bg, mustAPI(t, r)); err != nil {
		t.Fatal(err)
	}
	if r.inPeopleGroup(p.pk) || !r.inPeopleGroup(q.pk) {
		t.Fatalf("group = %v", r.f.groups["homestead-people"])
	}
}

func TestPutBackAndRecreateKeepTheGroupAndUid(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	oldUID := r.f.users[p.pk].UID
	// Authentik loses the membership: put back restores it.
	r.f.groups["homestead-people"] = nil
	if _, err := r.svc.Resolve(bg, "owner", p.ID, ActionPutBack, ""); err != nil || !r.inPeopleGroup(p.pk) {
		t.Fatalf("put back: %v", err)
	}
	// The user is deleted in Authentik and recreated: a new uid, and the
	// old subject no longer signs in.
	delete(r.f.users, p.pk)
	if _, err := r.svc.Resolve(bg, "owner", p.ID, ActionRecreate, "link"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok, _ := r.svc.SignIn(bg, oldUID); ok {
		t.Fatal("the old uid still signs in")
	}
	var pk int
	_ = r.db.QueryRow(`SELECT authentik_pk FROM people WHERE id = ?`, p.ID).Scan(&pk)
	if _, _, _, ok, _ := r.svc.SignIn(bg, r.f.users[pk].UID); !ok || !r.inPeopleGroup(pk) {
		t.Fatalf("the recreated user cannot sign in (group %v)", r.inPeopleGroup(pk))
	}
}

func mustAPI(t *testing.T, r *rig) *sso.API {
	t.Helper()
	api, err := r.svc.API()
	if err != nil {
		t.Fatal(err)
	}
	return api
}
