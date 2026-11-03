package everything

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func (c *caller) fsShare(path, who, mode string) (int, map[string]any) {
	return c.do(fmt.Sprintf(`{"operation":"fs.share","key":%q,"recipient":%q,"access":%q}`, path, who, mode))
}

func (c *caller) fsDo(op, key string, extra string) (int, map[string]any) {
	return c.do(fmt.Sprintf(`{"operation":%q,"kind":"file","key":%q%s}`, op, key, extra))
}

func putFile(t *testing.T, s *Store, sc Scope, key, v string) {
	t.Helper()
	if err := s.Put(context.Background(), sc, KindFile, key, []byte(v)); err != nil {
		t.Fatal(err)
	}
}

// shareID pulls the grant id out of a listShared path.
func firstShared(t *testing.T, c *caller) map[string]any {
	t.Helper()
	st, out := c.do(`{"operation":"fs.listShared"}`)
	items, _ := out["items"].([]any)
	if st != 200 || len(items) == 0 {
		t.Fatalf("listShared: %d %v", st, out)
	}
	return items[0].(map[string]any)
}

func TestFSShareFileRoundTrip(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob, carol := e.as(gAlice), e.as(gBob), e.as(gCarol)
	putFile(t, e.store, gAlice, "proj/a.circuits", "v1")
	st, out := alice.fsShare("proj/a.circuits", "@bob", "write")
	if st != 200 || out["isNew"] != true || out["holder"] != "Bob" || out["mode"] != "write" {
		t.Fatalf("share: %d %v", st, out)
	}
	if _, out = alice.fsShare("proj/a.circuits", "bob", "read"); out["isNew"] != false || out["mode"] != "read" {
		t.Fatalf("update: %v", out)
	}
	_, _ = alice.fsShare("proj/a.circuits", "bob", "write")
	it := firstShared(t, bob)
	p := it["path"].(string)
	if !strings.HasPrefix(p, "~shared/") || !strings.HasSuffix(p, "/a.circuits") || it["name"] != "a.circuits" ||
		it["owner"] != "Alice" || it["mode"] != "write" || it["is_dir"] != false {
		t.Fatalf("item: %v", it)
	}
	st, out = bob.fsDo("get", p, "")
	if st != 200 || out["value"] != enc64("v1") {
		t.Fatalf("read: %d %v", st, out)
	}
	// The bare id path names the same file.
	if st, _ = bob.fsDo("get", p[:strings.LastIndex(p, "/")], ""); st != 200 {
		t.Fatalf("bare id: %d", st)
	}
	if st, out = bob.fsDo("stat", p, ""); st != 200 || out["name"] != "a.circuits" || out["size"] != float64(2) {
		t.Fatalf("stat: %d %v", st, out)
	}
	st, out = bob.fsDo("put", p, fmt.Sprintf(`,"value":%q`, enc64("v2")))
	if st != 200 || out["size"] != float64(2) {
		t.Fatalf("write: %d %v", st, out)
	}
	if v, _ := e.store.Get(context.Background(), gAlice, KindFile, "proj/a.circuits"); string(v) != "v2" {
		t.Fatalf("owner file: %q", v)
	}
	if _, err := e.store.Get(context.Background(), gBob, KindFile, "proj/a.circuits"); err == nil {
		t.Fatal("recipient got a copy")
	}
	// A file grant never deletes, renames or reaches siblings.
	putFile(t, e.store, gAlice, "proj/b.circuits", "B")
	for _, c := range []struct{ op, key, extra string }{
		{"delete", p, ""}, {"deletetree", p, ""},
		{"rename", p, fmt.Sprintf(`,"to":%q`, p[:strings.LastIndex(p, "/")]+"/z.circuits")},
		{"get", p[:strings.LastIndex(p, "/")] + "/b.circuits", ""},
		{"put", p[:strings.LastIndex(p, "/")] + "/new.circuits", fmt.Sprintf(`,"value":%q`, enc64("x"))},
	} {
		if st, _ = bob.fsDo(c.op, c.key, c.extra); st != 400 && st != 404 {
			t.Fatalf("%s: %d", c.op, st)
		}
	}
	if _, err := e.store.Get(context.Background(), gAlice, KindFile, "proj/a.circuits"); err != nil {
		t.Fatal("file lost")
	}
	// Cross-person: Carol and Alice (as a would-be recipient) cannot use it.
	for _, c := range []*caller{carol, alice} {
		if st, _ = c.fsDo("get", p, ""); st != 404 {
			t.Fatalf("other person: %d", st)
		}
	}
	// Owner view and revoke.
	st, out = alice.do(`{"operation":"fs.getShares","key":"proj/a.circuits"}`)
	items := out["items"].([]any)
	if st != 200 || len(items) != 1 || items[0].(map[string]any)["holder"] != "Bob" || items[0].(map[string]any)["mode"] != "write" {
		t.Fatalf("getShares: %d %v", st, out)
	}
	st, out = alice.do(`{"operation":"fs.unshare","key":"proj/a.circuits","recipient":"bob"}`)
	if st != 200 || out["revoked"] != float64(1) {
		t.Fatalf("unshare: %d %v", st, out)
	}
	if st, _ = bob.fsDo("get", p, ""); st != 404 {
		t.Fatalf("after revoke: %d", st)
	}
	if _, out = bob.do(`{"operation":"fs.listShared"}`); len(out["items"].([]any)) != 0 {
		t.Fatal("revoked share listed")
	}
	_, out = alice.do(`{"operation":"fs.getShares","key":"proj/a.circuits"}`)
	if len(out["items"].([]any)) != 0 {
		t.Fatalf("revoked in getShares: %v", out)
	}
}

func TestFSShareReadOnlyFile(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	putFile(t, e.store, gAlice, "a.txt", "A")
	_, _ = alice.fsShare("a.txt", "bob", "read")
	p := firstShared(t, bob)["path"].(string)
	if st, _ := bob.fsDo("get", p, ""); st != 200 {
		t.Fatal("read denied")
	}
	for _, op := range []string{"put", "delete"} {
		st, out := bob.fsDo(op, p, fmt.Sprintf(`,"value":%q`, enc64("x")))
		wantCode(t, st, out, 422, "share_read_only")
	}
	if v, _ := e.store.Get(context.Background(), gAlice, KindFile, "a.txt"); string(v) != "A" {
		t.Fatal("read-only share wrote")
	}
}

func TestFSShareFolder(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	ctx := context.Background()
	putFile(t, e.store, gAlice, "team/x.txt", "X")
	putFile(t, e.store, gAlice, "team/sub/y.txt", "Y")
	putFile(t, e.store, gAlice, "team2/secret.txt", "S")
	putFile(t, e.store, gAlice, "private.txt", "P")
	if st, out := alice.fsShare("team", "bob", "write"); st != 200 || out["isNew"] != true {
		t.Fatalf("share folder: %d %v", st, out)
	}
	it := firstShared(t, bob)
	root := it["path"].(string)
	if it["is_dir"] != true || it["name"] != "team" || strings.Contains(root, "team") {
		t.Fatalf("item: %v", it)
	}
	// readdir and stat through the grant.
	st, out := bob.fsDo("readdir", root, "")
	ents := out["entries"].([]any)
	if st != 200 || len(ents) != 2 || ents[0].(map[string]any)["name"] != "sub" || ents[1].(map[string]any)["name"] != "x.txt" {
		t.Fatalf("readdir: %d %v", st, out)
	}
	if st, out = bob.fsDo("readdir", root+"/sub", ""); st != 200 || len(out["entries"].([]any)) != 1 {
		t.Fatalf("readdir sub: %d %v", st, out)
	}
	if st, out = bob.fsDo("stat", root, ""); st != 200 || out["is_dir"] != true {
		t.Fatalf("stat root: %d %v", st, out)
	}
	if st, out = bob.fsDo("stat", root+"/x.txt", ""); st != 200 || out["size"] != float64(1) {
		t.Fatalf("stat file: %d %v", st, out)
	}
	if st, _ = bob.fsDo("stat", root+"/nope", ""); st != 404 {
		t.Fatalf("stat missing: %d", st)
	}
	if st, out = bob.fsDo("get", root+"/sub/y.txt", ""); st != 200 || out["value"] != enc64("Y") {
		t.Fatalf("get nested: %d %v", st, out)
	}
	// Escapes are denied: dot-dot, sibling folder by prefix, backslash, folder id alone.
	for _, k := range []string{root + "/../private.txt", root + "/../team2/secret.txt", root + `/..\private.txt`, root + "/./x.txt", root + "//x.txt", root + "/"} {
		if st, _ = bob.fsDo("get", k, ""); st != 404 {
			t.Fatalf("escape %q: %d", k, st)
		}
	}
	if st, _ = bob.fsDo("get", root, ""); st != 404 {
		t.Fatalf("get folder: %d", st)
	}
	// Write inside creates and overwrites; the owner's quota pays.
	e.store.SetQuota(100, 100)
	st, out = bob.fsDo("put", root+"/new/z.txt", fmt.Sprintf(`,"value":%q`, enc64(strings.Repeat("z", 50))))
	if st != 413 {
		t.Fatalf("owner quota not charged: %d %v", st, out)
	}
	e.store.SetQuota(DefaultPersonQuota, DefaultAppQuota)
	if st, out = bob.fsDo("put", root+"/new/z.txt", fmt.Sprintf(`,"value":%q`, enc64("zz"))); st != 200 {
		t.Fatalf("create: %d %v", st, out)
	}
	if v, _ := e.store.Get(ctx, gAlice, KindFile, "team/new/z.txt"); string(v) != "zz" {
		t.Fatal("owner copy missing")
	}
	if st, _ = bob.fsDo("put", root, fmt.Sprintf(`,"value":%q`, enc64("x"))); st != 400 {
		t.Fatalf("write at folder: %d", st)
	}
	// Rename inside the folder (same folder only, never over a file).
	st, out = bob.fsDo("rename", root+"/x.txt", fmt.Sprintf(`,"to":%q`, root+"/w.txt"))
	if st != 200 {
		t.Fatalf("rename: %d %v", st, out)
	}
	if _, err := e.store.Get(ctx, gAlice, KindFile, "team/w.txt"); err != nil {
		t.Fatal("rename not applied")
	}
	if st, _ = bob.fsDo("rename", root+"/w.txt", fmt.Sprintf(`,"to":%q`, root+"/sub/w.txt")); st != 400 {
		t.Fatalf("rename across folders: %d", st)
	}
	putFile(t, e.store, gAlice, "team/other.txt", "O")
	if st, _ = bob.fsDo("rename", root+"/w.txt", fmt.Sprintf(`,"to":%q`, root+"/other.txt")); st != 409 {
		t.Fatalf("rename over file: %d", st)
	}
	for _, to := range []string{"team/q.txt", "~shared/otherid/q.txt", root, root + "/", "../q.txt"} {
		if st, _ = bob.fsDo("rename", root+"/w.txt", fmt.Sprintf(`,"to":%q`, to)); st != 400 && st != 404 {
			t.Fatalf("rename to %q: %d", to, st)
		}
	}
	if st, _ = bob.fsDo("rename", root, fmt.Sprintf(`,"to":%q`, root+"/zz")); st != 400 {
		t.Fatalf("rename folder itself: %d", st)
	}
	// Delete inside; the folder itself stays.
	if st, _ = bob.fsDo("delete", root+"/w.txt", ""); st != 200 {
		t.Fatalf("delete: %d", st)
	}
	if st, _ = bob.fsDo("deletetree", root+"/sub", ""); st != 200 {
		t.Fatalf("deletetree: %d", st)
	}
	if _, err := e.store.Get(ctx, gAlice, KindFile, "team/sub/y.txt"); err == nil {
		t.Fatal("tree not deleted")
	}
	for _, op := range []string{"delete", "deletetree"} {
		if st, _ = bob.fsDo(op, root, ""); st != 400 {
			t.Fatalf("%s folder itself: %d", op, st)
		}
	}
	if _, err := e.store.Get(ctx, gAlice, KindFile, "team/other.txt"); err != nil {
		t.Fatal("collateral loss")
	}
	// getShares reports a grant inherited from a folder.
	_, out = alice.do(`{"operation":"fs.getShares","key":"team/other.txt"}`)
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["inheritedFrom"] != "team" {
		t.Fatalf("inherited: %v", out)
	}
	// Read-only folder grant refuses writes of every kind.
	_, _ = alice.fsShare("team", "bob", "read")
	for _, c := range []struct{ op, key, extra string }{
		{"put", root + "/a", fmt.Sprintf(`,"value":%q`, enc64("x"))},
		{"delete", root + "/other.txt", ""},
		{"deletetree", root + "/other.txt", ""},
		{"rename", root + "/other.txt", fmt.Sprintf(`,"to":%q`, root+"/o2.txt")},
	} {
		st, out = bob.fsDo(c.op, c.key, c.extra)
		wantCode(t, st, out, 422, "share_read_only")
	}
	if st, _ = bob.fsDo("get", root+"/other.txt", ""); st != 200 {
		t.Fatal("read grant cannot read")
	}
}

func TestFSSharedPathsAreReserved(t *testing.T) {
	e := newShareEnv(t, true)
	alice := e.as(gAlice)
	ctx := context.Background()
	for _, k := range []string{"~shared", "~shared/x", "~shared/x/y"} {
		if err := e.store.Put(ctx, gAlice, KindFile, k, []byte("x")); err == nil {
			t.Fatalf("put %q accepted", k)
		}
		if err := e.store.PutBatch(ctx, gAlice, KindFile, []Entry{{Key: k, Value: []byte("x")}}); err == nil {
			t.Fatalf("batch %q accepted", k)
		}
		// Through the handler an unknown id is a plain 404 and nothing is stored.
		if st, _ := alice.fsDo("put", k, fmt.Sprintf(`,"value":%q`, enc64("x"))); st != 404 {
			t.Fatalf("handler put %q: %d", k, st)
		}
	}
	if st, _ := alice.fsDo("put", "ok.txt", fmt.Sprintf(`,"value":%q`, enc64("x"))); st != 200 {
		t.Fatal("normal put")
	}
	if st, _ := alice.fsDo("rename", "ok.txt", `,"to":"~shared/ok.txt"`); st != 400 {
		t.Fatalf("rename into ~shared: %d", st)
	}
	if st, out := alice.fsShare("~shared/x", "bob", "read"); st != 400 || out["error"] != "invalid_argument" {
		t.Fatalf("share of reserved path: %d %v", st, out)
	}
	// A key that merely starts with the letters is an ordinary name.
	if err := e.store.Put(ctx, gAlice, KindFile, "~shared2/x", []byte("x")); err != nil {
		t.Fatal(err)
	}
}

func TestFSShareErrorsUniformAndValidated(t *testing.T) {
	e := newShareEnv(t, true)
	alice := e.as(gAlice)
	putFile(t, e.store, gAlice, "a.txt", "A")
	// Purged and other-app-only people look like unknown names.
	_ = e.store.RecordSignIn(context.Background(), "notes", "s-dave", "Dave", time.Now())
	_ = e.store.RecordSignIn(context.Background(), "tasks", "s-erin", "Erin", time.Now())
	if _, err := e.store.PurgePerson(context.Background(), "tasks", "s-erin", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	var first string
	for i, who := range []string{"nobody", "dave", "erin", strings.Repeat("a", 500), "bob/x", "../x"} {
		st, out := alice.fsShare("a.txt", who, "read")
		wantCode(t, st, out, 404, "user_does_not_exist")
		b := fmt.Sprint(st, out)
		if i == 0 {
			first = b
		} else if b != first {
			t.Fatalf("answer differs for %q: %s vs %s", who, b, first)
		}
	}
	st, out := alice.fsShare("a.txt", "alice", "read")
	wantCode(t, st, out, 400, "cannot_share_with_self")
	for _, who := range []string{"bob@example.com", "", "@", "x@y"} {
		st, out = alice.fsShare("a.txt", who, "read")
		wantCode(t, st, out, 400, "invalid_recipient")
	}
	st, out = alice.fsShare("a.txt", "bob", "manage")
	wantCode(t, st, out, 400, "bridge_unsupported")
	st, out = alice.fsShare("a.txt", "bob", "owner")
	wantCode(t, st, out, 400, "invalid_argument")
	st, out = alice.fsShare("missing.txt", "bob", "read")
	wantCode(t, st, out, 404, "path_not_found")
	for _, k := range []string{"", "../a.txt", "/a.txt", "a//b", `a\b`} {
		st, out = alice.fsShare(k, "bob", "read")
		wantCode(t, st, out, 400, "invalid_argument")
	}
	// A recipient who is not yet a person of this app cannot be shared with,
	// and fs.unshare answers the same for unknown names and no share.
	st, out = alice.do(`{"operation":"fs.unshare","key":"a.txt","recipient":"nobody"}`)
	a := fmt.Sprint(st, out)
	st, out = alice.do(`{"operation":"fs.unshare","key":"a.txt","recipient":"bob"}`)
	if b := fmt.Sprint(st, out); a != b || out["revoked"] != float64(0) {
		t.Fatalf("unshare not uniform: %s vs %s", a, b)
	}
}

func TestFSShareLimitsAndAccess(t *testing.T) {
	e := newShareEnv(t, true)
	alice := e.as(gAlice)
	putFile(t, e.store, gAlice, "a.txt", "A")
	// Share attempts (fs and kv together) are limited per person and hour.
	for i := 0; i < ShareAttemptsPerHour; i++ {
		alice.fsShare("a.txt", "nobody", "read")
	}
	st, out := alice.fsShare("a.txt", "bob", "read")
	wantCode(t, st, out, 429, "share_rate_limited")
	st, out = alice.do(`{"operation":"fs.unshare","key":"a.txt","recipient":"bob"}`)
	wantCode(t, st, out, 429, "share_rate_limited")
	st, out = alice.share("p:", "bob", "read")
	wantCode(t, st, out, 429, "share_rate_limited")
	e.now = e.now.Add(61 * time.Minute)
	if st, _ = alice.fsShare("a.txt", "bob", "read"); st != 200 {
		t.Fatalf("after window: %d", st)
	}
	// Grant limit.
	e2 := newShareEnv(t, true)
	a2 := e2.as(gAlice)
	for i := 0; i < MaxLiveGrants; i++ {
		putFile(t, e2.store, gAlice, fmt.Sprintf("f%03d.txt", i), "x")
		e2.now = e2.now.Add(time.Minute)
		if st, out := a2.fsShare(fmt.Sprintf("f%03d.txt", i), "bob", "read"); st != 200 {
			t.Fatalf("share %d: %d %v", i, st, out)
		}
	}
	putFile(t, e2.store, gAlice, "over.txt", "x")
	e2.now = e2.now.Add(time.Hour)
	st, out = a2.fsShare("over.txt", "bob", "read")
	wantCode(t, st, out, 429, "share_limit")
}

func TestFSShareSessionRecheckSharingOffAndApps(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	putFile(t, e.store, gAlice, "a.txt", "A")
	_, _ = alice.fsShare("a.txt", "bob", "write")
	p := firstShared(t, bob)["path"].(string)
	// Stale recipient session: 401 for every shared fs call.
	bob.id.age = MaxSharedSessionAge
	for _, body := range []string{`{"operation":"fs.listShared"}`,
		fmt.Sprintf(`{"operation":"get","kind":"file","key":%q}`, p),
		fmt.Sprintf(`{"operation":"stat","kind":"file","key":%q}`, p),
		fmt.Sprintf(`{"operation":"put","kind":"file","key":%q,"value":"eA=="}`, p)} {
		if st, _ := bob.do(body); st != 401 {
			t.Fatalf("stale %s: %d", body, st)
		}
	}
	alice.id.age = 72 * time.Hour // the owner is not rechecked
	if st, _ := alice.do(`{"operation":"fs.getShares","key":"a.txt"}`); st != 200 {
		t.Fatal("owner blocked")
	}
	bob.id.age = 0
	// Same grant id in another app is not found.
	bobNotes := e.as(Scope{App: "notes", Subject: "s-bob"})
	if st, _ := bobNotes.fsDo("get", p, ""); st != 404 {
		t.Fatalf("cross app: %d", st)
	}
	if _, out := bobNotes.do(`{"operation":"fs.listShared"}`); len(out["items"].([]any)) != 0 {
		t.Fatal("share crossed apps")
	}
	// A kv handle is not an fs share and an fs id is not a kv handle.
	_, out := alice.share("p:", "bob", "write")
	h := out["handle"].(string)
	if st, _ := bob.fsDo("get", SharedRoot+"/"+h, ""); st != 404 {
		t.Fatalf("kv handle as fs: %d", st)
	}
	id := strings.Split(p, "/")[1]
	if st, out := bob.do(fmt.Sprintf(`{"operation":"get","kind":"kv","key":"a","handle":%q}`, id)); st != 404 || out["error"] != "kv_handle_not_found" {
		t.Fatalf("fs id as kv handle: %d %v", st, out)
	}
	// Purged owner makes the grant inert; the recipient answer is a plain 404.
	if _, err := e.store.PurgePerson(context.Background(), "tasks", "s-alice", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if st, _ := bob.fsDo("get", p, ""); st != 404 {
		t.Fatalf("purged owner: %d", st)
	}
	// Sharing off: every fs share operation answers disabled; plain files work.
	off := newShareEnv(t, false)
	o := off.as(gBob)
	for _, body := range []string{`{"operation":"fs.share","key":"a","recipient":"alice","access":"read"}`,
		`{"operation":"fs.unshare","key":"a","recipient":"alice"}`, `{"operation":"fs.getShares","key":"a"}`,
		`{"operation":"fs.listShared"}`, `{"operation":"get","kind":"file","key":"~shared/x/a"}`} {
		st, out := o.do(body)
		wantCode(t, st, out, 403, "fs_sharing_disabled")
	}
	if st, _ := o.fsDo("put", "plain.txt", `,"value":"eA=="`); st != 200 {
		t.Fatal("plain put with sharing off")
	}
}

func TestFSListSharedPaging(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	for i := 0; i < 5; i++ {
		k := fmt.Sprintf("f%d.txt", i)
		putFile(t, e.store, gAlice, k, "x")
		e.now = e.now.Add(time.Second)
		alice.fsShare(k, "bob", "read")
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 10; page++ {
		_, out := bob.do(fmt.Sprintf(`{"operation":"fs.listShared","limit":2,"cursor":%q}`, cursor))
		for _, it := range out["items"].([]any) {
			seen[it.(map[string]any)["name"].(string)] = true
		}
		c, _ := out["cursor"].(string)
		if c == "" {
			break
		}
		cursor = c
	}
	if len(seen) != 5 {
		t.Fatalf("paged %d items: %v", len(seen), seen)
	}
	if st, out := bob.do(`{"operation":"fs.listShared","cursor":"junk"}`); st != 400 {
		t.Fatalf("bad cursor: %d %v", st, out)
	}
	// A file the owner removed drops out of the list.
	_ = e.store.Delete(context.Background(), gAlice, KindFile, "f0.txt")
	_, out := bob.do(`{"operation":"fs.listShared"}`)
	if n := len(out["items"].([]any)); n != 4 {
		t.Fatalf("after owner delete: %d", n)
	}
}

func TestFSGrantDoesNotSurviveDeleteOrRename(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	ctx := context.Background()
	live := func(who *caller) int {
		_, out := who.do(`{"operation":"fs.listShared"}`)
		return len(out["items"].([]any))
	}
	// Delete then re-create: the recipient gets the uniform 404.
	putFile(t, e.store, gAlice, "a.txt", "A")
	alice.fsShare("a.txt", "bob", "write")
	p := firstShared(t, bob)["path"].(string)
	if err := e.store.Delete(ctx, gAlice, KindFile, "a.txt"); err != nil {
		t.Fatal(err)
	}
	putFile(t, e.store, gAlice, "a.txt", "A2")
	if st, _ := bob.fsDo("get", p, ""); st != 404 {
		t.Fatalf("revived after delete: %d", st)
	}
	if st, _ := bob.fsDo("put", p, fmt.Sprintf(`,"value":%q`, enc64("x"))); st != 404 {
		t.Fatalf("write revived: %d", st)
	}
	if live(bob) != 0 {
		t.Fatal("still listed")
	}
	// Owner rename revokes too.
	putFile(t, e.store, gAlice, "r.txt", "R")
	alice.fsShare("r.txt", "bob", "read")
	p = firstShared(t, bob)["path"].(string)
	if err := e.store.Rename(ctx, gAlice, "r.txt", "r2.txt"); err != nil {
		t.Fatal(err)
	}
	putFile(t, e.store, gAlice, "r.txt", "again")
	if st, _ := bob.fsDo("get", p, ""); st != 404 {
		t.Fatalf("revived after rename: %d", st)
	}
	// Folder tree delete revokes the folder grant and nested file grants.
	putFile(t, e.store, gAlice, "d/f.txt", "F")
	putFile(t, e.store, gAlice, "d/sub/g.txt", "G")
	putFile(t, e.store, gAlice, "dd/keep.txt", "K")
	alice.fsShare("d", "bob", "read")
	alice.fsShare("d/sub/g.txt", "bob", "read")
	alice.fsShare("dd/keep.txt", "bob", "read")
	if live(bob) != 3 {
		t.Fatalf("setup: %d", live(bob))
	}
	if _, err := e.store.DeleteTree(ctx, gAlice, "d"); err != nil {
		t.Fatal(err)
	}
	if n := live(bob); n != 1 { // the sibling "dd" share stays
		t.Fatalf("after tree delete: %d", n)
	}
	// A recipient's delete through a folder grant revokes nested file grants.
	putFile(t, e.store, gAlice, "w/x.txt", "X")
	putFile(t, e.store, gAlice, "w/y.txt", "Y")
	alice.fsShare("w", "bob", "write")
	alice.fsShare("w/y.txt", "carol", "read")
	var root string
	_, out := bob.do(`{"operation":"fs.listShared"}`)
	for _, it := range out["items"].([]any) {
		if m := it.(map[string]any); m["name"] == "w" {
			root = m["path"].(string)
		}
	}
	if st, _ := bob.fsDo("delete", root+"/y.txt", ""); st != 200 {
		t.Fatalf("recipient delete: %d", st)
	}
	putFile(t, e.store, gAlice, "w/y.txt", "Y2")
	carol := e.as(gCarol)
	if live(carol) != 0 {
		t.Fatal("nested grant survived a recipient delete")
	}
	if st, _ := bob.fsDo("get", root+"/y.txt", ""); st != 200 {
		t.Fatal("folder grant itself should remain")
	}
	if st, _ := bob.fsDo("deletetree", root+"/y.txt", ""); st != 200 {
		t.Fatal("recipient deletetree")
	}
}
