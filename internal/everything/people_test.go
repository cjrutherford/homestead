package everything

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (e *testEnv) signInAs(b *browser, sub, username string) string {
	e.p.claims = func(c map[string]any) {
		if username != "" {
			c["preferred_username"] = username
		}
	}
	defer func() { e.p.claims = nil }()
	return e.signIn(b, sub)
}

func peopleRow(t *testing.T, s *Store, sub string) (name string, purged bool, found bool) {
	t.Helper()
	var n, pa any
	err := s.db.QueryRow(`SELECT username, purged_at FROM people WHERE app='tasks' AND subject=?`, sub).Scan(&n, &pa)
	if err != nil {
		return "", false, false
	}
	s2, _ := n.(string)
	return s2, pa != nil, true
}

func TestSignInRecordsPersonAndWhoami(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	csrf := e.signInAs(b, "sub-alice", "Alice")
	code, out := e.whoamiFull(b, csrf)
	if code != 200 || out["username"] != "Alice" || out["uuid"] != e.uuidOf("sub-alice") || !strings.HasPrefix(out["uuid"], "p_") {
		t.Fatalf("whoami: %d %v", code, out)
	}
	if _, has := out["subject"]; has {
		t.Fatal("subject sent to page")
	}
	// Stable across sign-ins; different per person and per app.
	if e.uuidOf("sub-alice") == e.uuidOf("sub-bob") {
		t.Fatal("uuids collide")
	}
	other, _ := e.store.personUUID(context.Background(), "notes", "sub-alice")
	if other == e.uuidOf("sub-alice") {
		t.Fatal("uuid is not per app")
	}
	b2 := e.browser()
	csrf2 := e.signInAs(b2, "sub-alice", "Alice")
	if _, o2 := e.whoamiFull(b2, csrf2); o2["uuid"] != out["uuid"] {
		t.Fatal("uuid not stable")
	}
	// Case-insensitive key is stored.
	var key string
	e.store.db.QueryRow(`SELECT username_key FROM people WHERE subject='sub-alice'`).Scan(&key)
	if key != "alice" {
		t.Fatalf("key %q", key)
	}
}

func TestSignInWithoutUsernameStillWorks(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	csrf := e.signInAs(b, "sub-nameless", "")
	code, out := e.whoamiFull(b, csrf)
	if code != 200 || out["username"] != "" || out["uuid"] == "" {
		t.Fatalf("whoami: %d %v", code, out)
	}
	if n, _, found := peopleRow(t, e.store, "sub-nameless"); !found || n != "" {
		t.Fatal("nameless person not recorded without a name")
	}
}

func TestUsernameReuseMovesToNewestHolder(t *testing.T) {
	e := newEnv(t)
	e.signInAs(e.browser(), "sub-old", "pat")
	e.signInAs(e.browser(), "sub-new", "Pat")
	if n, _, _ := peopleRow(t, e.store, "sub-old"); n != "" {
		t.Fatalf("old holder kept name %q", n)
	}
	if n, _, _ := peopleRow(t, e.store, "sub-new"); n != "Pat" {
		t.Fatalf("new holder name %q", n)
	}
}

func TestGroupCheck(t *testing.T) {
	e := newEnv(t)
	e.o.cfg.Groups = []string{"tasks-users"}
	try := func(sub string, groups any) int {
		e.p.claims = func(c map[string]any) {
			if groups != nil {
				c["groups"] = groups
			}
		}
		defer func() { e.p.claims = nil }()
		b := e.browser()
		q := e.startLogin(b)
		return e.callback(b, q.Get("state"), e.approve(q, sub)).Code
	}
	if c := try("sub-a", []string{"other", "tasks-users"}); c != 302 {
		t.Fatalf("member: %d", c)
	}
	if c := try("sub-b", []string{"other"}); c != 403 {
		t.Fatalf("non-member: %d", c)
	}
	if _, _, found := peopleRow(t, e.store, "sub-b"); found {
		t.Fatal("refused sign-in was recorded")
	}
	if c := try("sub-c", nil); c != 302 {
		t.Fatalf("no groups claim passes: %d", c)
	}
	if c := try("sub-d", "tasks-users"); c != 403 {
		t.Fatalf("malformed claim: %d", c)
	}
}

func TestPurgeEndsSessionsAndIsPerAppAndPerPerson(t *testing.T) {
	e := newEnv(t)
	ba, bb := e.browser(), e.browser()
	ca := e.signInAs(ba, "sub-alice", "alice")
	cb := e.signInAs(bb, "sub-bob", "bob")
	ctx := context.Background()
	now := time.Now()
	// Another app's same-named person is untouched.
	if n, err := e.store.PurgePerson(ctx, "notes", "sub-alice", "", now); err != nil || n != 0 {
		t.Fatalf("cross-app purge: %d %v", n, err)
	}
	if code, _ := e.whoami(ba, ca); code != 200 {
		t.Fatal("cross-app purge ended a session")
	}
	if n, err := e.store.PurgePerson(ctx, "tasks", "", "@Alice", now); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if code, _ := e.whoami(ba, ca); code != 401 {
		t.Fatalf("purged session still works: %d", code)
	}
	if code, _ := e.whoami(bb, cb); code != 200 {
		t.Fatal("purge hit another person")
	}
	if _, purged, _ := peopleRow(t, e.store, "sub-alice"); !purged {
		t.Fatal("not marked")
	}
	// Idempotent and uniform for unknown names.
	for _, u := range []string{"alice", "nobody"} {
		if n, err := e.store.PurgePerson(ctx, "tasks", "", u, now); err != nil || n != 0 {
			t.Fatalf("repeat/unknown %s: %d %v", u, n, err)
		}
	}
	if _, err := e.store.PurgePerson(ctx, "tasks", "", "", now); err == nil {
		t.Fatal("empty target accepted")
	}
	// Data stays.
	// A purged person who signs in again (access restored) is live again.
	e.signInAs(e.browser(), "sub-alice", "alice")
	if n, purged, _ := peopleRow(t, e.store, "sub-alice"); purged || n != "alice" {
		t.Fatalf("not restored: %q %v", n, purged)
	}
}

func TestPurgeKeepsData(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	sc := Scope{App: "tasks", Subject: "s1"}
	st.RecordSignIn(ctx, "tasks", "s1", "zed", time.Now())
	if err := st.Put(ctx, sc, KindKV, "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.PurgePerson(ctx, "tasks", "s1", "", time.Now()); n != 1 {
		t.Fatal("not purged")
	}
	if v, err := st.Get(ctx, sc, KindKV, "k"); err != nil || string(v) != "v" {
		t.Fatalf("data lost: %q %v", v, err)
	}
}
