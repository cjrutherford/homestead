package everything

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func grantStore(t *testing.T) (*Store, string) {
	t.Helper()
	s, p := newTestStore(t)
	ctx := context.Background()
	for _, u := range [][2]string{{"s-alice", "Alice"}, {"s-bob", "Bob"}, {"s-carol", "Carol"}} {
		for _, app := range []string{"tasks", "notes"} {
			if err := s.RecordSignIn(ctx, app, u[0], u[1], time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s, p
}

var (
	gAlice = Scope{App: "tasks", Subject: "s-alice"}
	gBob   = Scope{App: "tasks", Subject: "s-bob"}
	gCarol = Scope{App: "tasks", Subject: "s-carol"}
)

func mustGrant(t *testing.T, s *Store, owner Scope, g GrantRequest) Grant {
	t.Helper()
	got, _, err := s.CreateGrant(context.Background(), owner, g, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGrantKVRoundTripAndRelativeKeys(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	if err := s.Put(ctx, gAlice, KindKV, "tasks:list1:a", []byte("A")); err != nil {
		t.Fatal(err)
	}
	g := mustGrant(t, s, gAlice, GrantRequest{Username: "@bob", Kind: "kv", Key: "tasks:list1:", IsPrefix: true, Access: "write", Label: "L"})
	if len(g.ID) < 22 || g.OwnerName != "Alice" || g.RecipName != "Bob" || g.RevokedAt != 0 {
		t.Fatalf("grant: %+v", g)
	}
	if v, err := s.GrantGet(ctx, gBob, g.ID, "kv", "a"); err != nil || string(v) != "A" {
		t.Fatalf("get: %q %v", v, err)
	}
	if err := s.GrantPut(ctx, gBob, g.ID, "kv", "b", []byte("B")); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(ctx, gAlice, KindKV, "tasks:list1:b"); err != nil || string(v) != "B" {
		t.Fatalf("owner copy: %q %v", v, err)
	}
	if _, err := s.Get(ctx, gBob, KindKV, "tasks:list1:b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rows were copied to the recipient: %v", err)
	}
	if err := s.GrantDelete(ctx, gBob, g.ID, "kv", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, gAlice, KindKV, "tasks:list1:a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("delete did not reach owner")
	}
}

func TestGrantReadOnlyRefusesWrites(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	g := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "kv", Key: "p:", IsPrefix: true, Access: "read"})
	if err := s.GrantPut(ctx, gBob, g.ID, "kv", "x", []byte("1")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("put: %v", err)
	}
	if err := s.GrantDelete(ctx, gBob, g.ID, "kv", "x"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GrantGet(ctx, gBob, g.ID, "kv", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read of missing key: %v", err)
	}
}

func TestGrantCrossPersonAndCrossAppDenial(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	s.Put(ctx, gAlice, KindKV, "p:x", []byte("secret"))
	g := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "kv", Key: "p:", IsPrefix: true, Access: "write"})
	for name, c := range map[string]Scope{
		"carol":      gCarol,
		"owner":      gAlice,
		"other app":  {App: "notes", Subject: "s-bob"},
		"stranger":   {App: "tasks", Subject: "nobody"},
		"empty":      {App: "tasks", Subject: ""},
		"bad app":    {App: "../x", Subject: "s-bob"},
		"carol/note": {App: "notes", Subject: "s-carol"},
	} {
		if _, err := s.GrantGet(ctx, c, g.ID, "kv", "x"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s get: %v", name, err)
		}
		if err := s.GrantPut(ctx, c, g.ID, "kv", "y", []byte("1")); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s put: %v", name, err)
		}
	}
	// Wrong kind and unknown or malformed ids look the same.
	for _, id := range []string{g.ID + "x", "", strings.Repeat("a", 200), "' OR 1=1 --"} {
		if _, err := s.GrantGet(ctx, gBob, id, "kv", "x"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("id %q: %v", id, err)
		}
	}
	if _, err := s.GrantGet(ctx, gBob, g.ID, "fs", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("kind: %v", err)
	}
	// Another app's same-named person cannot revoke or see it.
	if err := s.RevokeGrant(ctx, Scope{App: "notes", Subject: "s-alice"}, g.ID, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross app revoke: %v", err)
	}
	if err := s.RevokeGrant(ctx, gBob, g.ID, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recipient revoke: %v", err)
	}
	if gs, _ := s.GrantsForRecipient(ctx, gCarol, "kv"); len(gs) != 0 {
		t.Fatalf("carol sees %v", gs)
	}
	if gs, _ := s.GrantsByOwner(ctx, gBob, "kv"); len(gs) != 0 {
		t.Fatalf("bob owns %v", gs)
	}
}

func TestGrantKeyEscapesRefused(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	s.Put(ctx, gAlice, KindFile, "docs/a.txt", []byte("A"))
	s.Put(ctx, gAlice, KindFile, "docs-private/s.txt", []byte("S"))
	s.Put(ctx, gAlice, KindFile, "top.txt", []byte("T"))
	g := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "fs", Key: "docs", IsPrefix: true, Access: "write"})
	if v, err := s.GrantGet(ctx, gBob, g.ID, "fs", "a.txt"); err != nil || string(v) != "A" {
		t.Fatalf("%q %v", v, err)
	}
	for _, rel := range []string{"../top.txt", "..", "/top.txt", "a/../../top.txt", `..\top.txt`, "./a.txt", "a.txt/", "../docs-private/s.txt"} {
		if _, err := s.GrantGet(ctx, gBob, g.ID, "fs", rel); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%q: %v", rel, err)
		}
		if err := s.GrantPut(ctx, gBob, g.ID, "fs", rel, []byte("x")); !errors.Is(err, ErrNotFound) {
			t.Fatalf("put %q: %v", rel, err)
		}
	}
	// A file grant covers that file only.
	f := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "fs", Key: "top.txt", Access: "write"})
	if v, err := s.GrantGet(ctx, gBob, f.ID, "fs", ""); err != nil || string(v) != "T" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := s.GrantGet(ctx, gBob, f.ID, "fs", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.GrantPut(ctx, gBob, f.ID, "fs", "", []byte("T2")); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantDelete(ctx, gBob, f.ID, "fs", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("file grant delete: %v", err)
	}
	if v, _ := s.Get(ctx, gAlice, KindFile, "top.txt"); string(v) != "T2" {
		t.Fatalf("owner file %q", v)
	}
	// A folder grant with write may delete inside, not the folder itself.
	if err := s.GrantDelete(ctx, gBob, g.ID, "fs", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("folder delete: %v", err)
	}
	if err := s.GrantDelete(ctx, gBob, g.ID, "fs", "a.txt"); err != nil {
		t.Fatal(err)
	}
}

func TestGrantRecipientWritesChargeOwnerQuota(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	s.SetQuota(100, 0)
	g := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "fs", Key: "d", IsPrefix: true, Access: "write"})
	if err := s.GrantPut(ctx, gBob, g.ID, "fs", "a", bytes.Repeat([]byte("x"), 60)); err != nil {
		t.Fatal(err)
	}
	// Bob's own quota is untouched, so only the owner's usage can refuse this.
	if err := s.GrantPut(ctx, gBob, g.ID, "fs", "b", bytes.Repeat([]byte("x"), 60)); !errors.Is(err, ErrQuota) {
		t.Fatalf("want owner quota: %v", err)
	}
	if err := s.Put(ctx, gBob, KindFile, "mine", bytes.Repeat([]byte("x"), 90)); err != nil {
		t.Fatalf("recipient's own quota was charged: %v", err)
	}
	if err := s.Put(ctx, gAlice, KindFile, "mine", bytes.Repeat([]byte("x"), 60)); !errors.Is(err, ErrQuota) {
		t.Fatalf("owner usage should include the shared write: %v", err)
	}
}

func TestGrantRevokeAndListing(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	s.Put(ctx, gAlice, KindKV, "p:x", []byte("1"))
	g := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "kv", Key: "p:", IsPrefix: true, Access: "read"})
	again, isNew, err := s.CreateGrant(ctx, gAlice, GrantRequest{Username: "BOB", Kind: "kv", Key: "p:", IsPrefix: true, Access: "write", Label: "x"}, time.Now())
	if err != nil || isNew || again.ID != g.ID || again.Access != "write" {
		t.Fatalf("upsert: %+v %v %v", again, isNew, err)
	}
	if gs, _ := s.GrantsForRecipient(ctx, gBob, "kv"); len(gs) != 1 || gs[0].OwnerName != "Alice" {
		t.Fatalf("listShared: %+v", gs)
	}
	if err := s.RevokeGrant(ctx, gAlice, g.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeGrant(ctx, gAlice, g.ID, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
	if _, err := s.GrantGet(ctx, gBob, g.ID, "kv", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked still resolves: %v", err)
	}
	if gs, _ := s.GrantsForRecipient(ctx, gBob, "kv"); len(gs) != 0 {
		t.Fatalf("revoked listed for recipient: %+v", gs)
	}
	if gs, _ := s.GrantsByOwner(ctx, gAlice, "kv"); len(gs) != 1 || gs[0].RevokedAt == 0 {
		t.Fatalf("listSharedByMe: %+v", gs)
	}
	// A new share after revoke is a new id; the old id stays dead.
	n := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "kv", Key: "p:", IsPrefix: true, Access: "read"})
	if n.ID == g.ID {
		t.Fatal("id reused")
	}
	// RevokeShare by name (fs.unshare).
	if c, err := s.RevokeShare(ctx, gAlice, "bob", "kv", "p:", time.Now()); err != nil || c != 1 {
		t.Fatalf("%d %v", c, err)
	}
	if c, err := s.RevokeShare(ctx, gAlice, "bob", "kv", "p:", time.Now()); err != nil || c != 0 {
		t.Fatalf("%d %v", c, err)
	}
	if _, err := s.RevokeShare(ctx, gAlice, "nobody", "kv", "p:", time.Now()); !errors.Is(err, ErrNoRecipient) {
		t.Fatal(err)
	}
}

func TestGrantRecipientErrorsAreUniform(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	s.RecordSignIn(ctx, "tasks", "s-nameless", "", time.Now())
	s.RecordSignIn(ctx, "notes", "s-dave", "Dave", time.Now()) // other app only
	s.RecordSignIn(ctx, "tasks", "s-erin", "Erin", time.Now())
	s.PurgePerson(ctx, "tasks", "s-erin", "", time.Now())
	for _, name := range []string{"nobody", "Dave", "Erin", "", "@", "a\nb", strings.Repeat("a", 300)} {
		_, _, err := s.CreateGrant(ctx, gAlice, GrantRequest{Username: name, Kind: "kv", Key: "p:", IsPrefix: true, Access: "read"}, time.Now())
		if !errors.Is(err, ErrNoRecipient) {
			t.Fatalf("%q: %v", name, err)
		}
	}
	if _, _, err := s.CreateGrant(ctx, gAlice, GrantRequest{Username: "alice", Kind: "kv", Key: "p:", IsPrefix: true, Access: "read"}, time.Now()); !errors.Is(err, ErrSelfShare) {
		t.Fatal(err)
	}
	bad := []GrantRequest{
		{Username: "bob", Kind: "kv", Key: "p:*", IsPrefix: true, Access: "read"},
		{Username: "bob", Kind: "kv", Key: "p:", Access: "read"},
		{Username: "bob", Kind: "kv", Key: "", IsPrefix: true, Access: "read"},
		{Username: "bob", Kind: "fs", Key: "../x", Access: "read"},
		{Username: "bob", Kind: "fs", Key: "/x", Access: "read"},
		{Username: "bob", Kind: "fs", Key: "x", Access: "manage"},
		{Username: "bob", Kind: "net", Key: "x", Access: "read"},
		{Username: "bob", Kind: "fs", Key: "x", Access: "read", Label: "a\x00b"},
	}
	for i, b := range bad {
		if _, _, err := s.CreateGrant(ctx, gAlice, b, time.Now()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%d: %v", i, err)
		}
	}
	// A purged owner cannot share.
	if _, _, err := s.CreateGrant(ctx, Scope{App: "tasks", Subject: "s-erin"}, GrantRequest{Username: "bob", Kind: "kv", Key: "p:", IsPrefix: true, Access: "read"}, time.Now()); !errors.Is(err, ErrNoRecipient) {
		t.Fatal(err)
	}
}

func TestGrantLimitPerOwnerPerApp(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	for i := 0; i < MaxLiveGrants; i++ {
		if _, _, err := s.CreateGrant(ctx, gAlice, GrantRequest{Username: "bob", Kind: "fs", Key: fmt.Sprintf("f%d", i), Access: "read"}, time.Now()); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, _, err := s.CreateGrant(ctx, gAlice, GrantRequest{Username: "bob", Kind: "fs", Key: "over", Access: "read"}, time.Now()); !errors.Is(err, ErrGrantLimit) {
		t.Fatalf("over: %v", err)
	}
	// Updating an existing share is not a new one; other apps and owners are separate.
	if _, _, err := s.CreateGrant(ctx, gAlice, GrantRequest{Username: "bob", Kind: "fs", Key: "f0", Access: "write"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateGrant(ctx, Scope{App: "notes", Subject: "s-alice"}, GrantRequest{Username: "bob", Kind: "fs", Key: "n", Access: "read"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateGrant(ctx, gCarol, GrantRequest{Username: "bob", Kind: "fs", Key: "n", Access: "read"}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeRevokesGrantsAsOwnerAndRecipient(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	s.Put(ctx, gAlice, KindKV, "p:x", []byte("1"))
	s.Put(ctx, gBob, KindKV, "q:x", []byte("2"))
	a2b := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "kv", Key: "p:", IsPrefix: true, Access: "read"})
	b2a := mustGrant(t, s, gBob, GrantRequest{Username: "alice", Kind: "kv", Key: "q:", IsPrefix: true, Access: "read"})
	c2a := mustGrant(t, s, gCarol, GrantRequest{Username: "alice", Kind: "kv", Key: "c:", IsPrefix: true, Access: "read"})
	other := mustGrant(t, s, Scope{App: "notes", Subject: "s-alice"}, GrantRequest{Username: "bob", Kind: "kv", Key: "n:", IsPrefix: true, Access: "read"})
	if n, err := s.PurgePerson(ctx, "tasks", "s-bob", "", time.Now()); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	for _, id := range []string{a2b.ID, b2a.ID} {
		g, _ := s.grantByID(ctx, id)
		if g.RevokedAt == 0 {
			t.Fatalf("grant %s survived purge", id)
		}
	}
	if g, _ := s.grantByID(ctx, c2a.ID); g.RevokedAt != 0 {
		t.Fatal("unrelated grant revoked")
	}
	if g, _ := s.grantByID(ctx, other.ID); g.RevokedAt != 0 {
		t.Fatal("same person's grant in another app revoked")
	}
	// Data stays; signing in again does not revive the grants.
	if v, err := s.Get(ctx, gBob, KindKV, "q:x"); err != nil || string(v) != "2" {
		t.Fatalf("data: %q %v", v, err)
	}
	s.RecordSignIn(ctx, "tasks", "s-bob", "Bob", time.Now())
	if _, err := s.GrantGet(ctx, gBob, a2b.ID, "kv", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revived: %v", err)
	}
}

func TestGrantInertWhenPeoplePurgedWithoutRevokeRow(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	s.Put(ctx, gAlice, KindKV, "p:x", []byte("1"))
	g := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "kv", Key: "p:", IsPrefix: true, Access: "read"})
	// Mark the owner purged behind the store's back (for example a restored older file).
	if _, err := s.db.Exec(`UPDATE people SET purged_at = 1 WHERE app='tasks' AND subject='s-alice'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantGet(ctx, gBob, g.ID, "kv", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inert owner: %v", err)
	}
	if gs, _ := s.GrantsForRecipient(ctx, gBob, "kv"); len(gs) != 0 {
		t.Fatal("inert grant listed")
	}
}

func TestExportV2RoundTripGrantsAndPeople(t *testing.T) {
	s, p := grantStore(t)
	ctx := context.Background()
	s.Put(ctx, gAlice, KindKV, "p:x", []byte(`{"a":1}`))
	g := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "kv", Key: "p:", IsPrefix: true, Access: "write", Label: "shared"})
	rev := mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "fs", Key: "f", Access: "read"})
	s.RevokeGrant(ctx, gAlice, rev.ID, time.Now())
	s.createSession(ctx, "tasks", "s-alice", timeNow())
	s.Close()
	out := filepath.Join(t.TempDir(), "x")
	if _, err := Export(ctx, p, "", "", out); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	m := string(raw)
	if !strings.Contains(m, `"homestead-everything-export/2"`) || !strings.Contains(m, g.ID) || !strings.Contains(m, `"username":"Bob"`) {
		t.Fatalf("manifest: %s", m)
	}
	for _, bad := range []string{"session", "hmac", "csrf", "token", "uuid", "p_"} {
		if strings.Contains(m, bad) {
			t.Fatalf("manifest leaks %q: %s", bad, m)
		}
	}
	// Into a store that already has the people: grants return, same ids.
	s2, _ := newTestStore(t)
	for _, u := range [][2]string{{"s-alice", "Alice"}, {"s-bob", "Bob"}} {
		s2.RecordSignIn(ctx, "tasks", u[0], u[1], time.Now())
	}
	r, err := ImportAll(ctx, s2, out)
	if err != nil || r.Grants != 2 || r.Skipped != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if v, err := s2.GrantGet(ctx, gBob, g.ID, "kv", "x"); err != nil || string(v) != `{"a":1}` {
		t.Fatalf("restored grant: %q %v", v, err)
	}
	if gs, _ := s2.GrantsByOwner(ctx, gAlice, "fs"); len(gs) != 1 || gs[0].RevokedAt == 0 {
		t.Fatalf("revoked state lost: %+v", gs)
	}
	// Importing twice changes nothing.
	if r, err = ImportAll(ctx, s2, out); err != nil || r.Grants != 0 {
		t.Fatalf("again: %+v %v", r, err)
	}
	// Into a fresh store with no people, or only the owner: none restored, objects are.
	for _, who := range [][][2]string{nil, {{"s-alice", "Alice"}}} {
		s3, _ := newTestStore(t)
		for _, u := range who {
			s3.RecordSignIn(ctx, "tasks", u[0], u[1], time.Now())
		}
		r, err := ImportAll(ctx, s3, out)
		if err != nil || r.Grants != 0 || r.Skipped != 2 || r.Objects != 1 {
			t.Fatalf("%v: %+v %v", who, r, err)
		}
		if gs, _ := s3.AllGrants(ctx, "", true); len(gs) != 0 {
			t.Fatalf("grants restored without people: %+v", gs)
		}
	}
	// A purged recipient in the target is not restored to.
	s4, _ := newTestStore(t)
	for _, u := range [][2]string{{"s-alice", "Alice"}, {"s-bob", "Bob"}} {
		s4.RecordSignIn(ctx, "tasks", u[0], u[1], time.Now())
	}
	s4.PurgePerson(ctx, "tasks", "s-bob", "", time.Now())
	if r, _ = ImportAll(ctx, s4, out); r.Grants != 0 || r.Skipped != 2 {
		t.Fatalf("purged: %+v", r)
	}
}

func TestExportFilterAndV1Import(t *testing.T) {
	s, p := grantStore(t)
	ctx := context.Background()
	mustGrant(t, s, gAlice, GrantRequest{Username: "bob", Kind: "fs", Key: "f", Access: "read"})
	mustGrant(t, s, gCarol, GrantRequest{Username: "bob", Kind: "fs", Key: "g", Access: "read"})
	s.Close()
	out := filepath.Join(t.TempDir(), "x")
	if _, err := Export(ctx, p, "s-alice", "tasks", out); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	if strings.Contains(string(raw), "s-carol") || !strings.Contains(string(raw), `"owner":"s-alice"`) {
		t.Fatalf("filter: %s", raw)
	}
	// A /1 manifest still imports and restores no grants.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"format": "homestead-everything-export/1", "objects": [{"app":"tasks","subject":"a","kind":"kv","key":"k","value":1,"bytes":1}]}`), 0o600)
	s2, _ := newTestStore(t)
	if r, err := ImportAll(ctx, s2, dir); err != nil || r.Objects != 1 || r.Grants != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"format": "homestead-everything-export/3", "objects": []}`), 0o600)
	if _, err := ImportAll(ctx, s2, dir); err == nil {
		t.Fatal("unknown format accepted")
	}
}

func TestOpenStoreUpgradesOlderFile(t *testing.T) {
	s, p := newTestStore(t)
	s.db.Exec(`DROP TABLE grants`)
	s.Close()
	s2, err := OpenStore(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if gs, err := s2.AllGrants(context.Background(), "", true); err != nil || len(gs) != 0 {
		t.Fatal(gs, err)
	}
}
