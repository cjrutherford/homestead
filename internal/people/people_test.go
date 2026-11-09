package people

import (
	"context"
	"errors"
	"strings"
	"testing"

	"homestead/internal/purge"
)

var bg = context.Background()

func (r *rig) add(username string, grants ...Grant) (Person, *Revealed) {
	r.t.Helper()
	res, err := r.svc.Add(bg, "owner", AddRequest{DisplayName: "Ada Lovelace", Username: username, Email: username + "@example.test", Grants: grants})
	if err != nil || res.Warning != "" || res.SignInFailed {
		r.t.Fatalf("add: %v %+v", err, res)
	}
	return res.Person, res.Revealed
}

func TestAddCreatesTaggedUserRecordsItAndRevealsALinkOnce(t *testing.T) {
	r := newRig(t)
	p, rev := r.add("ada")
	if rev == nil || rev.Kind != "link" || !strings.Contains(rev.Value, "recover") {
		t.Fatalf("revealed: %+v", rev)
	}
	u := r.f.users[p.pk]
	if u == nil || u.Attrs[managed] != true || u.Path != "users/homestead" || u.Type != "internal" || !u.Active {
		t.Fatalf("user: %+v", u)
	}
	v, _ := r.svc.List(bg)
	if len(v.People) != 1 || v.People[0].Status != StatusOK || v.People[0].Origin != OriginCreated {
		t.Fatalf("list: %+v", v.People)
	}
	for _, l := range r.logs {
		if strings.Contains(l, "recover/") || strings.Contains(l, rev.Value) {
			t.Fatalf("audit holds the link: %q", l)
		}
	}
}

const managed = "homestead.managed"

func TestGeneratedPasswordIsRevealedOnceAndNotAudited(t *testing.T) {
	r := newRig(t)
	res, err := r.svc.Add(bg, "owner", AddRequest{DisplayName: "Bo", Username: "bo", Method: MethodGenerated})
	p, rev := res.Person, res.Revealed
	if err != nil || rev == nil || rev.Kind != "password" || len(rev.Value) < 16 {
		t.Fatalf("add: %v %+v", err, rev)
	}
	if r.f.passwords[p.pk] != rev.Value {
		t.Fatal("the revealed password is not the one set")
	}
	rev2, err := r.svc.ResetPassword(bg, "owner", p.ID, MethodGenerated, "")
	if err != nil || rev2.Value == rev.Value || r.f.passwords[p.pk] != rev2.Value {
		t.Fatalf("reset: %v %+v", err, rev2)
	}
	// An owner-chosen password is set and never returned.
	rev3, err := r.svc.ResetPassword(bg, "owner", p.ID, "", "owner-chosen-pw-1")
	if err != nil || rev3 != nil || r.f.passwords[p.pk] != "owner-chosen-pw-1" {
		t.Fatalf("given: %v %+v", err, rev3)
	}
	for _, l := range r.logs {
		for _, secret := range []string{rev.Value, rev2.Value, "owner-chosen-pw-1"} {
			if strings.Contains(l, secret) {
				t.Fatalf("audit holds a secret: %q", l)
			}
		}
	}
}

func TestAddValidatesAndRefusesDuplicates(t *testing.T) {
	r := newRig(t)
	for _, bad := range []AddRequest{
		{DisplayName: "", Username: "x"},
		{DisplayName: "X", Username: "has space"},
		{DisplayName: "X", Username: "x", Email: "nope"},
		{DisplayName: "X", Username: "x", Method: "carrier-pigeon"},
		{DisplayName: "X", Username: "x", Grants: []Grant{{App: "jellyfin", Role: "user"}}},
		{DisplayName: "X", Username: "x", Grants: []Grant{{App: "immich", Role: "king"}}},
	} {
		if _, err := r.svc.Add(bg, "owner", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	r.add("dup")
	if _, err := r.svc.Add(bg, "owner", AddRequest{DisplayName: "D", Username: "DUP"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	// Authentik already has the name (a user Homestead did not make).
	if _, err := r.svc.Add(bg, "owner", AddRequest{DisplayName: "C", Username: "cousin"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("existing in Authentik: %v", err)
	}
}

func TestAddWithoutARecoveryFlowKeepsThePersonAndWarns(t *testing.T) {
	r := newRig(t)
	r.f.noRecover = true
	res, err := r.svc.Add(bg, "owner", AddRequest{DisplayName: "Cy", Username: "cy", Grants: []Grant{{"immich", RoleUser}}})
	if err != nil || res.Revealed != nil || !res.SignInFailed || !strings.Contains(res.Warning, "was added") || res.Person.ID == 0 {
		t.Fatalf("%v %+v", err, res)
	}
	// The grants are made anyway, and a generated password still works.
	if len(res.Person.Grants) != 1 || !r.f.member("homestead-immich-users", res.Person.pk) {
		t.Fatalf("grants after a failed sign-in: %+v", res.Person.Grants)
	}
	rev, err := r.svc.ResetPassword(bg, "owner", res.Person.ID, MethodGenerated, "")
	if err != nil || rev == nil || rev.Kind != "password" {
		t.Fatalf("generate instead: %v %+v", err, rev)
	}
}

func TestGrantRevokeAndAdminImpliesUser(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada", Grant{"nextcloud", RoleUser})
	if !r.f.member("homestead-nextcloud-users", p.pk) || r.f.member("homestead-nextcloud-admins", p.pk) {
		t.Fatal("user grant memberships")
	}
	if _, err := r.svc.Grant(bg, "owner", p.ID, Grant{"nextcloud", RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if !r.f.member("homestead-nextcloud-users", p.pk) || !r.f.member("homestead-nextcloud-admins", p.pk) {
		t.Fatal("admin must be in both groups")
	}
	got, err := r.svc.Grant(bg, "owner", p.ID, Grant{"nextcloud", RoleUser})
	if err != nil || r.f.member("homestead-nextcloud-admins", p.pk) || got.Grants[0].Role != RoleUser {
		t.Fatalf("demote: %v %+v", err, got.Grants)
	}
	got, err = r.svc.Revoke(bg, "owner", p.ID, "nextcloud")
	if err != nil || len(got.Grants) != 0 || r.f.member("homestead-nextcloud-users", p.pk) {
		t.Fatalf("revoke: %v %+v", err, got.Grants)
	}
	if _, err := r.svc.Revoke(bg, "owner", p.ID, "nextcloud"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("revoke twice: %v", err)
	}
	if _, err := r.svc.Grant(bg, "owner", p.ID, Grant{"jellyfin", RoleUser}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown app: %v", err)
	}
}

func TestGrantWithoutAppGroupsExplains(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	delete(r.f.groups, "homestead-immich-users")
	_, err := r.svc.Grant(bg, "owner", p.ID, Grant{"immich", RoleUser})
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "plan its sign-in again") {
		t.Fatalf("%v", err)
	}
}

func TestOnlyHomesteadGroupsAreEverTouched(t *testing.T) {
	r := newRig(t)
	r.f.groups["plain-team"] = []int{11}
	p, _ := r.add("ada", Grant{"nextcloud", RoleAdmin})
	_, _ = r.svc.Revoke(bg, "owner", p.ID, "nextcloud")
	_ = r.svc.Remove(bg, "owner", p.ID)
	for _, c := range r.f.calls {
		if strings.Contains(c, "plain-team") {
			t.Fatalf("touched a group without the prefix: %s", c)
		}
	}
	if len(r.f.groups["plain-team"]) != 1 {
		t.Fatal("plain-team changed")
	}
}

func TestEditDisableEnableAndRemove(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada", Grant{"immich", RoleUser})
	name, email := "Ada King", "ada@new.example"
	got, err := r.svc.Edit(bg, "owner", p.ID, EditRequest{DisplayName: &name, Email: &email})
	if err != nil || got.DisplayName != name || r.f.users[p.pk].Email != email || r.f.users[p.pk].Name != name {
		t.Fatalf("edit: %v %+v", err, got)
	}
	bad := "not an email"
	if _, err := r.svc.Edit(bg, "owner", p.ID, EditRequest{Email: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad email: %v", err)
	}
	if _, err := r.svc.Edit(bg, "owner", p.ID, EditRequest{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty edit: %v", err)
	}
	if err := r.svc.SetActive(bg, "owner", p.ID, false); err != nil || r.f.users[p.pk].Active {
		t.Fatalf("disable: %v", err)
	}
	v, _ := r.svc.List(bg)
	if v.People[0].State != StateDisabled || v.People[0].Status != StatusOK {
		t.Fatalf("disabled person: %+v", v.People[0])
	}
	if err := r.svc.SetActive(bg, "owner", p.ID, true); err != nil || !r.f.users[p.pk].Active {
		t.Fatalf("enable: %v", err)
	}
	if err := r.svc.Remove(bg, "owner", p.ID); err != nil {
		t.Fatal(err)
	}
	if r.f.users[p.pk] != nil {
		t.Fatal("the created user must be deleted")
	}
	v, _ = r.svc.List(bg)
	if len(v.People) != 0 {
		t.Fatalf("still listed: %+v", v.People)
	}
	if err := r.svc.Remove(bg, "owner", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
}

func TestRemoveDeletesOnlyUsersCarryingTheTag(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	delete(r.f.users[p.pk].Attrs, managed) // someone untagged it in Authentik
	if err := r.svc.Remove(bg, "owner", p.ID); err != nil {
		t.Fatal(err)
	}
	if r.f.users[p.pk] == nil {
		t.Fatal("deleted a user that no longer carries the tag")
	}
}

func TestResetSecondFactorRemovesKnownDevices(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	r.f.devices[p.pk] = []fakeDevice{{"1", "authentik_stages_authenticator_totp.totpdevice"}, {"2", "authentik_stages_authenticator_webauthn.webauthndevice"}, {"3", "x.mysterydevice"}}
	removed, skipped, err := r.svc.ResetSecondFactor(bg, "owner", p.ID)
	if err != nil || removed != 2 || skipped != 1 || len(r.f.devices[p.pk]) != 1 {
		t.Fatalf("%d %d %v %+v", removed, skipped, err, r.f.devices[p.pk])
	}
}

func TestListHidesServiceAccountsOwnerAndAkadminAndShowsUnmanaged(t *testing.T) {
	r := newRig(t)
	r.add("ada")
	v, err := r.svc.List(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Unmanaged) != 1 || v.Unmanaged[0].Username != "cousin" {
		t.Fatalf("unmanaged: %+v", v.Unmanaged)
	}
	for _, p := range v.People {
		if strings.HasPrefix(p.Username, "ak-") || p.Username == "owner" || p.Username == "svc" {
			t.Fatalf("listed %s", p.Username)
		}
	}
}

func TestAdoptTagsImportsGroupsAndRefusesSystemAccounts(t *testing.T) {
	r := newRig(t)
	r.f.groups["homestead-immich-users"] = []int{11}
	r.f.groups["homestead-nextcloud-users"] = []int{11}
	r.f.groups["homestead-nextcloud-admins"] = []int{11}
	r.f.groups["plain-team"] = []int{11}
	p, err := r.svc.Adopt(bg, "owner", "cousin")
	if err != nil || p.Origin != OriginAdopted || len(p.Grants) != 2 {
		t.Fatalf("adopt: %v %+v", err, p)
	}
	if got, _ := grantFor(p.Grants, "nextcloud"); got.Role != RoleAdmin {
		t.Fatalf("grants: %+v", p.Grants)
	}
	if r.f.users[11].Attrs[managed] != true {
		t.Fatal("not tagged")
	}
	for _, name := range []string{"ak-outpost-abc", "svc", "owner", "nobody"} {
		if _, err := r.svc.Adopt(bg, "owner", name); err == nil {
			t.Errorf("adopted %s", name)
		}
	}
	if _, err := r.svc.Adopt(bg, "owner", "cousin"); !errors.Is(err, ErrConflict) {
		t.Fatalf("adopt twice: %v", err)
	}
	// akadmin is adoptable by explicit name only.
	if _, err := r.svc.Adopt(bg, "owner", "akadmin"); err != nil {
		t.Fatalf("explicit akadmin: %v", err)
	}
	// Removing an adopted person releases, never deletes.
	if err := r.svc.Remove(bg, "owner", p.ID); err != nil {
		t.Fatal(err)
	}
	u := r.f.users[11]
	if u == nil || u.Attrs[managed] != nil || r.f.member("homestead-nextcloud-admins", 11) || !r.f.member("plain-team", 11) {
		t.Fatalf("release: %+v %v", u, r.f.groups)
	}
}

func TestDriftPutBackRestoresHomesteadsRecord(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada", Grant{"nextcloud", RoleUser})
	u := r.f.users[p.pk]
	u.Email = "changed@evil.test"
	u.Active = false
	r.f.groups["homestead-immich-admins"] = append(r.f.groups["homestead-immich-admins"], p.pk) // extra admin
	r.f.groups["homestead-nextcloud-users"] = nil                                               // lost a grant
	v, _ := r.svc.List(bg)
	got := v.People[0]
	if got.Status != StatusDrifted || len(got.Drift) != 4 {
		t.Fatalf("drift: %+v", got.Drift)
	}
	// Nothing was reverted by reading.
	if u.Email != "changed@evil.test" {
		t.Fatal("the read changed Authentik")
	}
	if _, err := r.svc.Resolve(bg, "owner", p.ID, ActionPutBack, ""); err != nil {
		t.Fatal(err)
	}
	if u.Email != p.Email || !u.Active || r.f.member("homestead-immich-admins", p.pk) || !r.f.member("homestead-nextcloud-users", p.pk) {
		t.Fatalf("not put back: %+v %v", u, r.f.groups)
	}
	if v, _ = r.svc.List(bg); v.People[0].Status != StatusOK {
		t.Fatalf("still drifted: %+v", v.People[0].Drift)
	}
}

func TestDriftKeepAcceptsAuthentiksValues(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada", Grant{"nextcloud", RoleUser})
	u := r.f.users[p.pk]
	u.Email, u.Name, u.Active = "new@example.test", "Ada B", false
	r.f.groups["homestead-immich-users"] = []int{p.pk}
	r.f.groups["homestead-nextcloud-users"] = nil
	if _, err := r.svc.Resolve(bg, "owner", p.ID, ActionKeep, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := r.svc.person(p.ID)
	if got.Email != "new@example.test" || got.DisplayName != "Ada B" || got.State != StateDisabled ||
		len(got.Grants) != 1 || got.Grants[0].App != "immich" {
		t.Fatalf("kept: %+v", got)
	}
	if v, _ := r.svc.List(bg); v.People[0].Status != StatusOK {
		t.Fatalf("drift after keep: %+v", v.People[0].Drift)
	}
}

func TestMissingUserRecreateOrForget(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada", Grant{"nextcloud", RoleAdmin})
	delete(r.f.users, p.pk)
	v, _ := r.svc.List(bg)
	if v.People[0].Status != StatusMissing {
		t.Fatalf("status: %s", v.People[0].Status)
	}
	if _, err := r.svc.Resolve(bg, "owner", p.ID, ActionPutBack, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("put back on missing: %v", err)
	}
	rev, err := r.svc.Resolve(bg, "owner", p.ID, ActionRecreate, MethodGenerated)
	if err != nil || rev == nil || rev.Kind != "password" {
		t.Fatalf("recreate: %v %+v", err, rev)
	}
	got, _ := r.svc.person(p.ID)
	if got.pk == p.pk || !r.f.member("homestead-nextcloud-admins", got.pk) || !r.f.member("homestead-nextcloud-users", got.pk) {
		t.Fatalf("recreated: %+v %v", got, r.f.groups)
	}
	delete(r.f.users, got.pk)
	if _, err := r.svc.Resolve(bg, "owner", p.ID, ActionForget, ""); err != nil {
		t.Fatal(err)
	}
	if v, _ = r.svc.List(bg); len(v.People) != 0 {
		t.Fatal("forget kept the record")
	}
}

func TestWithoutAKeptTokenNothingActsAndTheListSaysSo(t *testing.T) {
	r := newRig(t)
	p, _ := r.add("ada")
	r.kept = false
	v, err := r.svc.List(bg)
	if err != nil || v.TokenKept || len(v.People) != 1 || v.People[0].Status != StatusUnchecked {
		t.Fatalf("list: %v %+v", err, v)
	}
	before := len(r.f.calls)
	_, e1 := r.svc.Add(bg, "owner", AddRequest{DisplayName: "X", Username: "x"})
	_, e2 := r.svc.Grant(bg, "owner", p.ID, Grant{"immich", RoleUser})
	e3 := r.svc.Remove(bg, "owner", p.ID)
	_, e4 := r.svc.ResetPassword(bg, "owner", p.ID, "", "")
	for i, e := range []error{e1, e2, e3, e4} {
		if !errors.Is(e, ErrNoToken) {
			t.Errorf("%d: %v", i, e)
		}
	}
	if len(r.f.calls) != before {
		t.Fatal("called Authentik without a token")
	}
}

func TestEveryActionIsAuditedWithoutSecrets(t *testing.T) {
	r := newRig(t)
	p, rev := r.add("ada", Grant{"immich", RoleUser})
	name := "Ada Z"
	_, _ = r.svc.Edit(bg, "owner", p.ID, EditRequest{DisplayName: &name})
	_, _ = r.svc.ResetPassword(bg, "owner", p.ID, MethodGenerated, "")
	_, _, _ = r.svc.ResetSecondFactor(bg, "owner", p.ID)
	_ = r.svc.SetActive(bg, "owner", p.ID, false)
	_ = r.svc.SetActive(bg, "owner", p.ID, true)
	_, _ = r.svc.Revoke(bg, "owner", p.ID, "immich")
	_, _ = r.svc.Resolve(bg, "owner", p.ID, ActionKeep, "")
	_ = r.svc.Remove(bg, "owner", p.ID)
	_, _ = r.svc.Adopt(bg, "owner", "cousin")
	want := []string{"people.add", "people.grant", "people.edit", "people.password-reset", "people.mfa-reset", "people.disable", "people.enable", "people.revoke", "people.resolve", "people.remove", "people.adopt"}
	all := strings.Join(r.logs, "\n")
	for _, a := range want {
		if !strings.Contains(all, "|"+a+"|ok|") {
			t.Errorf("no ok audit for %s", a)
		}
	}
	for _, secret := range []string{rev.Value, fakeToken, r.f.passwords[p.pk], "@example.test"} {
		if secret != "" && strings.Contains(all, secret) {
			t.Fatalf("audit holds %q", secret)
		}
	}
}

func TestLinkComesBackOnTheLocalAndThePublicAddress(t *testing.T) {
	r := newRig(t)
	r.svc.PublicAuthentik = func() string { return "https://home.example.com/apps/auth" }
	_, rev := r.add("ada")
	if rev == nil || rev.Kind != "link" || !strings.HasPrefix(rev.Value, "https://auth.example/recover/tok-") {
		t.Fatalf("local link: %+v", rev)
	}
	if want := "https://home.example.com/recover/" + strings.TrimPrefix(rev.Value, "https://auth.example/recover/"); rev.PublicValue != want {
		t.Fatalf("public link %q, want %q", rev.PublicValue, want)
	}
	// A reset issues both again.
	p, _ := r.svc.List(bg)
	again, err := r.svc.ResetPassword(bg, "owner", p.People[0].ID, MethodLink, "")
	if err != nil || again.PublicValue == "" || !strings.HasPrefix(again.Value, "https://auth.example/") {
		t.Fatalf("reset: %v %+v", err, again)
	}
	for _, l := range r.logs {
		if strings.Contains(l, rev.PublicValue) || strings.Contains(l, again.PublicValue) {
			t.Fatalf("audit holds a link: %q", l)
		}
	}
}

func TestNoPublicLinkWithoutAPublicAuthentikAddress(t *testing.T) {
	r := newRig(t)
	_, rev := r.add("ada")
	if rev == nil || rev.PublicValue != "" {
		t.Fatalf("no tunnel: %+v", rev)
	}
	r.svc.PublicAuthentik = func() string { return "" }
	p, _ := r.svc.List(bg)
	again, err := r.svc.ResetPassword(bg, "owner", p.People[0].ID, MethodLink, "")
	if err != nil || again.PublicValue != "" {
		t.Fatalf("empty public address: %v %+v", err, again)
	}
}

// purgeRig wires a purge service whose Everything apps are tasks and notes.
func purgeRig(t *testing.T) (*rig, *[]string, map[string]bool) {
	r := newRig(t)
	r.svc.Apps = func() ([]string, error) { return []string{"nextcloud", "immich", "tasks"}, nil }
	for _, g := range []string{"homestead-tasks-users", "homestead-tasks-admins"} {
		r.f.groups[g] = nil
	}
	var calls []string
	down := map[string]bool{}
	r.svc.Purge = &purge.Service{
		DB: r.db,
		Apps: func(context.Context) ([]purge.Target, error) {
			return []purge.Target{{App: "tasks", Container: "tasks-tasks"}}, nil
		},
		Exec: func(_ context.Context, c string, argv ...string) (string, error) {
			calls = append(calls, c+" "+strings.Join(argv, " "))
			if down[c] {
				return "", errors.New("not running")
			}
			return "purged 1 people\n", nil
		},
		Audit: func(actor, action, outcome, detail string) { r.logs = append(r.logs, action+"|"+outcome+"|"+detail) },
	}
	return r, &calls, down
}

func TestRevokePurgesOnlyEverythingApps(t *testing.T) {
	r, calls, _ := purgeRig(t)
	p, _ := r.add("ada", Grant{"nextcloud", RoleUser}, Grant{"tasks", RoleUser})
	if _, err := r.svc.Revoke(bg, "owner", p.ID, "nextcloud"); err != nil || len(*calls) != 0 {
		t.Fatalf("a non-Everything app is not purged: %v %q", err, *calls)
	}
	if _, err := r.svc.Revoke(bg, "owner", p.ID, "tasks"); err != nil || len(*calls) != 1 {
		t.Fatalf("%v %q", err, *calls)
	}
	if !strings.Contains((*calls)[0], "-app tasks -subject=") || !strings.Contains(strings.Join(r.logs, "\n"), "sharing purged in tasks") {
		t.Fatalf("%q %q", *calls, r.logs)
	}
}

func TestRemoveStoresPendingPurgeAndListShowsIt(t *testing.T) {
	r, calls, down := purgeRig(t)
	p, _ := r.add("ada", Grant{"immich", RoleUser})
	down["tasks-tasks"] = true
	if err := r.svc.Remove(bg, "owner", p.ID); err != nil {
		t.Fatalf("removal must not wait on the purge: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("%q", *calls)
	}
	v, _ := r.svc.List(bg)
	if len(v.People) != 0 || len(v.Purges) != 1 || v.Purges[0].App != "tasks" || v.Purges[0].Username != "ada" {
		t.Fatalf("%+v", v.Purges)
	}
	if !strings.Contains(strings.Join(r.logs, "\n"), "removed ada") || !strings.Contains(strings.Join(r.logs, "\n"), "purge pending in tasks") {
		t.Fatalf("%q", r.logs)
	}
}

func TestGrantAgainCancelsPendingPurge(t *testing.T) {
	r, _, down := purgeRig(t)
	p, _ := r.add("ada", Grant{"tasks", RoleUser})
	down["tasks-tasks"] = true
	if _, err := r.svc.Revoke(bg, "owner", p.ID, "tasks"); err != nil {
		t.Fatal(err)
	}
	if !r.svc.Purge.HasPending() {
		t.Fatal("pending expected")
	}
	if _, err := r.svc.Grant(bg, "owner", p.ID, Grant{"tasks", RoleUser}); err != nil {
		t.Fatal(err)
	}
	if r.svc.Purge.HasPending() {
		t.Fatal("access is back: the stored purge must be dropped")
	}
}

// S456: sign-in off then on recreates the app's groups empty; applying it
// restores the recorded grants, so nobody shows as drifted.
func TestRestoreAppRepopulatesGroupsFromGrants(t *testing.T) {
	r := newRig(t)
	u, _ := r.add("ada", Grant{"nextcloud", RoleUser})
	a, _ := r.add("bob", Grant{"nextcloud", RoleAdmin})
	r.f.groups["homestead-nextcloud-users"] = nil
	r.f.groups["homestead-nextcloud-admins"] = nil
	if v, _ := r.svc.List(bg); v.People[0].Status != StatusDrifted {
		t.Fatalf("expected drift: %+v", v.People)
	}
	n, err := r.svc.RestoreApp(bg, "nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || !r.f.member("homestead-nextcloud-users", u.pk) || r.f.member("homestead-nextcloud-admins", u.pk) ||
		!r.f.member("homestead-nextcloud-users", a.pk) || !r.f.member("homestead-nextcloud-admins", a.pk) {
		t.Fatalf("n=%d groups=%v", n, r.f.groups)
	}
	v, _ := r.svc.List(bg)
	for _, p := range v.People {
		if p.Status != StatusOK {
			t.Fatalf("%s still drifted: %+v", p.Username, p.Drift)
		}
	}
	if n, err := r.svc.RestoreApp(bg, "immich"); err != nil || n != 0 {
		t.Fatalf("an app nobody holds: %d %v", n, err)
	}
}
