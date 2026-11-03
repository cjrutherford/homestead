package everything

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type agedIdentity struct {
	scope Scope
	age   time.Duration
}

func (a *agedIdentity) Authenticate(*http.Request) (Scope, error) { return a.scope, nil }
func (a *agedIdentity) CheckCSRF(*http.Request) bool              { return true }
func (a *agedIdentity) SessionAge(*http.Request) (time.Duration, bool) {
	return a.age, true
}

type shareEnv struct {
	t     *testing.T
	store *Store
	now   time.Time
}

func newShareEnv(t *testing.T, sharing bool) *shareEnv {
	s, _ := grantStore(t)
	s.SetSharing(sharing)
	return &shareEnv{t: t, store: s, now: time.Now()}
}

type caller struct {
	e  *shareEnv
	id *agedIdentity
	h  *handler
}

func (e *shareEnv) as(scope Scope) *caller {
	id := &agedIdentity{scope: scope}
	h, err := NewHandler(e.store, id, []string{"https://home.example"})
	if err != nil {
		e.t.Fatal(err)
	}
	hh := h.(*handler)
	hh.now = func() time.Time { return e.now }
	return &caller{e: e, id: id, h: hh}
}

// do posts a request and decodes the JSON reply (or keeps the text).
func (c *caller) do(body string) (int, map[string]any) {
	req := httptest.NewRequest("POST", "https://home.example/bridge", bytes.NewBufferString(body))
	req.Header.Set("Origin", "https://home.example")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out == nil {
		out = map[string]any{"text": rec.Body.String()}
	}
	return rec.Code, out
}

func enc64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func (c *caller) share(prefix, who, access string) (int, map[string]any) {
	return c.do(fmt.Sprintf(`{"operation":"kv.share","prefix":%q,"recipient":%q,"access":%q,"label":"L"}`, prefix, who, access))
}

func wantCode(t *testing.T, status int, out map[string]any, wantStatus int, code string) {
	t.Helper()
	if status != wantStatus || out["error"] != code {
		t.Fatalf("got %d %v, want %d %s", status, out, wantStatus, code)
	}
}

func TestKVShareRoundTripThroughHandle(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob, carol := e.as(gAlice), e.as(gBob), e.as(gCarol)
	ctx := context.Background()
	if err := e.store.Put(ctx, gAlice, KindKV, "tasks:collab:x:meta", []byte(`"M"`)); err != nil {
		t.Fatal(err)
	}
	st, out := alice.share("tasks:collab:x:", "@bob", "write")
	if st != 200 || out["holder"] != "Bob" || out["owner"] != "Alice" || out["prefix"] != "tasks:collab:x:" ||
		out["access"] != "write" || out["appUid"] != "app-tasks" || out["revokedAt"] != nil || out["label"] != "L" {
		t.Fatalf("share: %d %v", st, out)
	}
	h := out["handle"].(string)
	if len(h) < 22 {
		t.Fatal("short handle")
	}
	// Bob reads, writes and lists with relative keys.
	st, out = bob.do(fmt.Sprintf(`{"operation":"get","kind":"kv","key":"meta","handle":%q}`, h))
	if st != 200 || out["value"] != enc64(`"M"`) {
		t.Fatalf("get: %d %v", st, out)
	}
	if st, out = bob.do(fmt.Sprintf(`{"operation":"put","kind":"kv","key":"t:1","value":%q,"handle":%q}`, enc64(`1`), h)); st != 200 {
		t.Fatalf("put: %d %v", st, out)
	}
	if v, err := e.store.Get(ctx, gAlice, KindKV, "tasks:collab:x:t:1"); err != nil || string(v) != "1" {
		t.Fatalf("owner copy: %q %v", v, err)
	}
	if _, err := e.store.Get(ctx, gBob, KindKV, "t:1"); err == nil {
		t.Fatal("recipient got a copy")
	}
	if st, out = bob.do(fmt.Sprintf(`{"operation":"putbatch","kind":"kv","entries":[{"key":"t:2","value":%q},{"key":"t:3","value":%q}],"handle":%q}`, enc64(`2`), enc64(`3`), h)); st != 200 {
		t.Fatalf("batch: %d %v", st, out)
	}
	st, out = bob.do(fmt.Sprintf(`{"operation":"list","kind":"kv","key":"t:","handle":%q}`, h))
	if st != 200 || fmt.Sprint(out["keys"]) != "[t:1 t:2 t:3]" {
		t.Fatalf("list: %d %v", st, out)
	}
	st, out = bob.do(fmt.Sprintf(`{"operation":"listvalues","kind":"kv","key":"","handle":%q}`, h))
	if st != 200 || len(out["entries"].([]any)) != 4 || out["next"] != "" {
		t.Fatalf("listvalues: %d %v", st, out)
	}
	if st, _ = bob.do(fmt.Sprintf(`{"operation":"delete","kind":"kv","key":"t:2","handle":%q}`, h)); st != 200 {
		t.Fatal("delete")
	}
	if st, _ = bob.do(fmt.Sprintf(`{"operation":"get","kind":"kv","key":"t:2","handle":%q}`, h)); st != 404 {
		t.Fatalf("deleted key: %d", st)
	}
	// Carol, a third person, and Alice's own handle use are denied.
	for _, c := range []*caller{carol, alice} {
		st, out = c.do(fmt.Sprintf(`{"operation":"get","kind":"kv","key":"meta","handle":%q}`, h))
		wantCode(t, st, out, 404, "kv_handle_not_found")
	}
	// Listing for Bob and for Alice.
	st, out = bob.do(`{"operation":"kv.listShared"}`)
	items := out["items"].([]any)
	if st != 200 || len(items) != 1 || items[0].(map[string]any)["handle"] != h || items[0].(map[string]any)["owner"] != "Alice" {
		t.Fatalf("listShared: %d %v", st, out)
	}
	if st, out = carol.do(`{"operation":"kv.listShared"}`); len(out["items"].([]any)) != 0 {
		t.Fatalf("carol sees shares: %v", out)
	}
	// Revoke: Bob's operations stop, byMe still lists it with revokedAt.
	if st, out = alice.do(fmt.Sprintf(`{"operation":"kv.unshare","handle":%q}`, h)); st != 200 {
		t.Fatalf("unshare: %d %v", st, out)
	}
	st, out = bob.do(fmt.Sprintf(`{"operation":"get","kind":"kv","key":"meta","handle":%q}`, h))
	wantCode(t, st, out, 404, "kv_handle_not_found")
	if _, out = bob.do(`{"operation":"kv.listShared"}`); len(out["items"].([]any)) != 0 {
		t.Fatal("revoked share still listed")
	}
	_, out = alice.do(`{"operation":"kv.listSharedByMe"}`)
	if it := out["items"].([]any)[0].(map[string]any); it["revokedAt"] == nil || it["holder"] != "Bob" {
		t.Fatalf("byMe: %v", out)
	}
	// Unshare of a revoked, unknown or another person's handle is one answer.
	for _, c := range []*caller{alice, bob, carol} {
		st, out = c.do(fmt.Sprintf(`{"operation":"kv.unshare","handle":%q}`, h))
		wantCode(t, st, out, 404, "subject_does_not_exist")
	}
	st, out = alice.do(`{"operation":"kv.unshare","handle":"nope"}`)
	wantCode(t, st, out, 404, "subject_does_not_exist")
}

func TestKVShareReadOnlyAndPrefixIsolation(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	ctx := context.Background()
	_ = e.store.Put(ctx, gAlice, KindKV, "tasks:collab:x:a", []byte("A"))
	_ = e.store.Put(ctx, gAlice, KindKV, "tasks:collab:y:secret", []byte("S"))
	_, out := alice.share("tasks:collab:x:", "bob", "read")
	h := out["handle"].(string)
	st, out := bob.do(fmt.Sprintf(`{"operation":"put","kind":"kv","key":"z","value":%q,"handle":%q}`, enc64(`1`), h))
	wantCode(t, st, out, 422, "kv_handle_read_only")
	st, out = bob.do(fmt.Sprintf(`{"operation":"delete","kind":"kv","key":"a","handle":%q}`, h))
	wantCode(t, st, out, 422, "kv_handle_read_only")
	if st, _ = bob.do(fmt.Sprintf(`{"operation":"get","kind":"kv","key":"a","handle":%q}`, h)); st != 200 {
		t.Fatal("read denied")
	}
	// A bare-prefix value cannot be written and keys cannot leave the prefix
	// (the relative key is appended, so "../" is just text under the prefix).
	_, out = alice.share("tasks:collab:x:", "bob", "write") // upsert keeps the handle
	if out["handle"] != h {
		t.Fatal("upsert changed the handle")
	}
	if st, _ = bob.do(fmt.Sprintf(`{"operation":"put","kind":"kv","key":"","value":%q,"handle":%q}`, enc64(`1`), h)); st != 400 {
		t.Fatalf("bare prefix write: %d", st)
	}
	if st, _ = bob.do(fmt.Sprintf(`{"operation":"get","kind":"kv","key":"y:secret","handle":%q}`, h)); st != 404 {
		t.Fatalf("sibling prefix: %d", st)
	}
	// Handle with the file kind is not a KV share.
	if st, _ = bob.do(fmt.Sprintf(`{"operation":"get","kind":"file","key":"a","handle":%q}`, h)); st != 400 {
		t.Fatalf("file kind: %d", st)
	}
}

func TestKVHandleCrossAppDenied(t *testing.T) {
	e := newShareEnv(t, true)
	alice := e.as(gAlice)
	_ = e.store.Put(context.Background(), gAlice, KindKV, "tasks:collab:x:a", []byte("A"))
	_, out := alice.share("tasks:collab:x:", "bob", "write")
	h := out["handle"].(string)
	bobNotes := e.as(Scope{App: "notes", Subject: "s-bob"})
	st, out := bobNotes.do(fmt.Sprintf(`{"operation":"get","kind":"kv","key":"a","handle":%q}`, h))
	wantCode(t, st, out, 404, "kv_handle_not_found")
	if _, out = bobNotes.do(`{"operation":"kv.listShared"}`); len(out["items"].([]any)) != 0 {
		t.Fatal("share crossed apps")
	}
}

func TestKVShareErrorsAreUniformAndValidated(t *testing.T) {
	e := newShareEnv(t, true)
	alice := e.as(gAlice)
	// Unknown, other-app-only, purged and malformed names: one answer.
	if _, err := e.store.PurgePerson(context.Background(), "tasks", "s-carol", "", e.now); err != nil {
		t.Fatal(err)
	}
	if err := e.store.RecordSignIn(context.Background(), "notes", "s-dave", "Dave", e.now); err != nil {
		t.Fatal(err)
	}
	var first map[string]any
	for _, who := range []string{"nobody", "Carol", "dave", "bad name!", strings.Repeat("x", 300)} {
		st, out := alice.share("tasks:collab:x:", who, "write")
		wantCode(t, st, out, 404, "subject_does_not_exist")
		if first == nil {
			first = out
		} else if fmt.Sprint(first) != fmt.Sprint(out) {
			t.Fatalf("answers differ: %v vs %v", first, out)
		}
	}
	st, out := alice.share("tasks:collab:x:", "alice", "write")
	wantCode(t, st, out, 400, "cannot_share_with_self")
	st, out = alice.share("tasks:collab:x:", "bob@example.com", "write")
	wantCode(t, st, out, 400, "invalid_recipient")
	st, out = alice.share("tasks:collab:x:", "", "write")
	wantCode(t, st, out, 400, "invalid_recipient")
	st, out = alice.share("tasks:collab:x:", "bob", "manage")
	wantCode(t, st, out, 400, "bridge_unsupported")
	st, out = alice.share("tasks:collab:x:", "bob", "owner")
	wantCode(t, st, out, 400, "invalid_argument")
	st, out = alice.share("tasks:*", "bob", "read")
	wantCode(t, st, out, 400, "invalid_argument")
	st, out = alice.share("", "bob", "read")
	wantCode(t, st, out, 400, "invalid_argument")
	st, out = alice.do(`{"operation":"kv.share","prefix":"p:","recipient":"bob","access":"read","label":"bad\u0001"}`)
	wantCode(t, st, out, 400, "kv_share_label_invalid")
}

func TestKVShareAttemptRateLimit(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	for i := 0; i < ShareAttemptsPerHour; i++ {
		if st, out := alice.share("p:", "nobody", "read"); st != 404 {
			t.Fatalf("attempt %d: %d %v", i, st, out)
		}
	}
	st, out := alice.share("p:", "bob", "read")
	wantCode(t, st, out, 429, "share_rate_limited")
	// Other people and the later hour are unaffected; unshare is not counted.
	if st, _ = bob.share("p:", "alice", "read"); st != 200 {
		t.Fatalf("bob limited: %d", st)
	}
	if st, _ = alice.do(`{"operation":"kv.unshare","handle":"x"}`); st != 404 {
		t.Fatalf("unshare limited: %d", st)
	}
	e.now = e.now.Add(61 * time.Minute)
	if st, _ = alice.share("p:", "bob", "read"); st != 200 {
		t.Fatalf("still limited after an hour: %d", st)
	}
}

func TestKVShareGrantLimit(t *testing.T) {
	e := newShareEnv(t, true)
	alice := e.as(gAlice)
	for i := 0; i < MaxLiveGrants; i++ {
		if _, err := e.store.db.Exec(`INSERT INTO grants (id, app, owner_subject, recipient_subject, kind, object_key, is_prefix, access, label, created_at, updated_at)
            VALUES (?, 'tasks', 's-alice', 's-bob', 'kv', ?, 1, 'read', '', 1, 1)`, fmt.Sprintf("g%d", i), fmt.Sprintf("p%d:", i)); err != nil {
			t.Fatal(err)
		}
	}
	st, out := alice.share("new:", "bob", "read")
	wantCode(t, st, out, 429, "share_limit")
}

func TestSharedOperationsNeedFreshSession(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	_ = e.store.Put(context.Background(), gAlice, KindKV, "p:a", []byte("A"))
	_, out := alice.share("p:", "bob", "write")
	h := out["handle"].(string)
	get := fmt.Sprintf(`{"operation":"get","kind":"kv","key":"a","handle":%q}`, h)
	bob.id.age = MaxSharedSessionAge - time.Minute
	if st, _ := bob.do(get); st != 200 {
		t.Fatalf("fresh: %d", st)
	}
	bob.id.age = MaxSharedSessionAge
	for _, body := range []string{get, `{"operation":"kv.listShared"}`,
		fmt.Sprintf(`{"operation":"put","kind":"kv","key":"a","value":%q,"handle":%q}`, enc64(`1`), h)} {
		if st, _ := bob.do(body); st != 401 {
			t.Fatalf("stale session %s: %d", body, st)
		}
	}
	// The owner's own operations are not affected by session age.
	alice.id.age = 48 * time.Hour
	if st, _ := alice.do(`{"operation":"kv.listSharedByMe"}`); st != 200 {
		t.Fatal("owner blocked by age")
	}
	if st, _ := alice.do(`{"operation":"get","kind":"kv","key":"p:a"}`); st != 200 {
		t.Fatal("own get blocked by age")
	}
	// An identity that cannot report age is refused for shared operations.
	h2, _ := NewHandler(e.store, fakeIdentity{scope: gBob, csrf: true}, []string{"https://home.example"})
	req := httptest.NewRequest("POST", "https://home.example/b", strings.NewReader(get))
	req.Header.Set("Origin", "https://home.example")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("no age: %d", rec.Code)
	}
}

func TestSharingOffAnswersDisabled(t *testing.T) {
	e := newShareEnv(t, false)
	bob := e.as(gBob)
	st, out := bob.share("p:", "alice", "read")
	wantCode(t, st, out, 403, "kv_handles_disabled")
	st, out = bob.do(`{"operation":"kv.listShared"}`)
	wantCode(t, st, out, 403, "kv_handles_disabled")
	st, out = bob.do(`{"operation":"get","kind":"kv","key":"a","handle":"h"}`)
	wantCode(t, st, out, 403, "kv_handles_disabled")
	st, out = bob.do(`{"operation":"perms.request","resource":"kvShare","prefix":"p:","access":"write"}`)
	wantCode(t, st, out, 400, "invalid_argument")
	// Plain operations still work.
	if st, _ = bob.do(`{"operation":"put","kind":"kv","key":"a","value":"MQ=="}`); st != 200 {
		t.Fatal("plain put")
	}
}

func TestPermsRequest(t *testing.T) {
	e := newShareEnv(t, true)
	bob := e.as(gBob)
	p, err := e.store.Person(context.Background(), gBob)
	if err != nil || p.UUID == "" {
		t.Fatal(err)
	}
	ok := func(body string) {
		t.Helper()
		if st, out := bob.do(body); st != 200 || out["granted"] != true {
			t.Fatalf("%s: %d %v", body, st, out)
		}
	}
	bad := func(body string) {
		t.Helper()
		st, out := bob.do(body)
		wantCode(t, st, out, 400, "invalid_argument")
	}
	ok(`{"operation":"perms.request","resource":"kvShare","prefix":"tasks:collab:","access":"write"}`)
	ok(`{"operation":"perms.request","resource":"kvShare","prefix":"tasks:collab:","access":"read"}`)
	ok(fmt.Sprintf(`{"operation":"perms.request","resource":"permission","permission":"manage:kv-share-write:%s:app-tasks:tasks:collab"}`, p.UUID))
	bad(`{"operation":"perms.request","resource":"kvShare","prefix":"tasks:collab:","access":"manage"}`)
	bad(`{"operation":"perms.request","resource":"kvShare","prefix":"","access":"write"}`)
	bad(`{"operation":"perms.request","resource":"kvShare","prefix":"x*","access":"write"}`)
	bad(`{"operation":"perms.request","resource":"other"}`)
	bad(`{"operation":"perms.request"}`)
	bad(fmt.Sprintf(`{"operation":"perms.request","resource":"permission","permission":"manage:kv-share-write:%s:app-notes:tasks:collab"}`, p.UUID))
	bad(`{"operation":"perms.request","resource":"permission","permission":"manage:kv-share-write:p_someoneelse:app-tasks:tasks:collab"}`)
	bad(fmt.Sprintf(`{"operation":"perms.request","resource":"permission","permission":"manage:fs-write:%s:app-tasks:tasks:collab"}`, p.UUID))
	bad(fmt.Sprintf(`{"operation":"perms.request","resource":"permission","permission":"manage:kv-share-write:%s:app-tasks"}`, p.UUID))
}

func TestListSharedCursors(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	for i := 0; i < 5; i++ {
		e.now = e.now.Add(time.Second)
		if st, out := alice.share(fmt.Sprintf("p%d:", i), "bob", "read"); st != 200 {
			t.Fatalf("%d %v", st, out)
		}
	}
	var got []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		st, out := bob.do(fmt.Sprintf(`{"operation":"kv.listShared","limit":2,"cursor":%q}`, cursor))
		if st != 200 {
			t.Fatalf("page: %d %v", st, out)
		}
		for _, it := range out["items"].([]any) {
			got = append(got, it.(map[string]any)["prefix"].(string))
		}
		cursor, _ = out["cursor"].(string)
		if cursor == "" {
			break
		}
	}
	if len(got) != 5 || got[0] != "p0:" || got[4] != "p4:" {
		t.Fatalf("pages: %v", got)
	}
	for _, bad := range []string{`"nocolon"`, `"x:y"`, `-1`} {
		body := `{"operation":"kv.listShared","cursor":` + bad + `}`
		if strings.HasPrefix(bad, "-") {
			body = `{"operation":"kv.listShared","limit":` + bad + `}`
		}
		if st, _ := bob.do(body); st != 400 {
			t.Fatalf("bad cursor %s: %d", bad, st)
		}
	}
}

func TestKVHandleListValuesCursor(t *testing.T) {
	e := newShareEnv(t, true)
	alice, bob := e.as(gAlice), e.as(gBob)
	ctx := context.Background()
	_, out := alice.share("big:", "bob", "read")
	h := out["handle"].(string)
	n := ListValuesMax + 20
	for i := 0; i < n; i++ {
		_ = e.store.Put(ctx, gAlice, KindKV, fmt.Sprintf("big:k%04d", i), []byte("v"))
	}
	_ = e.store.Put(ctx, gAlice, KindKV, "bigger:other", []byte("v")) // outside the share
	total, after := 0, ""
	for pages := 0; pages < 5; pages++ {
		st, out := bob.do(fmt.Sprintf(`{"operation":"listvalues","kind":"kv","key":"k","after":%q,"handle":%q}`, after, h))
		if st != 200 {
			t.Fatalf("%d %v", st, out)
		}
		for _, e := range out["entries"].([]any) {
			if !strings.HasPrefix(e.(map[string]any)["key"].(string), "k") {
				t.Fatalf("key not relative: %v", e)
			}
		}
		total += len(out["entries"].([]any))
		after, _ = out["next"].(string)
		if after == "" {
			break
		}
		if strings.HasPrefix(after, "big:") {
			t.Fatalf("cursor leaks the owner's prefix: %q", after)
		}
	}
	if total != n {
		t.Fatalf("read %d of %d", total, n)
	}
}

func TestHandlerRejectsUnknownShareFieldsAndCSRF(t *testing.T) {
	e := newShareEnv(t, true)
	// CSRF and origin are enforced before any share operation.
	h, _ := NewHandler(e.store, fakeIdentity{scope: gAlice}, []string{"https://home.example"})
	for _, op := range []string{"kv.share", "kv.unshare", "kv.listShared", "kv.listSharedByMe", "perms.request"} {
		req := httptest.NewRequest("POST", "https://home.example/b", strings.NewReader(`{"operation":"`+op+`"}`))
		req.Header.Set("Origin", "https://home.example")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("%s without csrf: %d", op, rec.Code)
		}
	}
	if st, _ := e.as(gAlice).do(`{"operation":"kv.share","owner":"s-bob"}`); st != 400 {
		t.Fatalf("unknown field: %d", st)
	}
}

func TestOIDCSessionAge(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	e.signIn(b, "sub-a")
	e.now = e.now.Add(50 * time.Minute)
	req := httptest.NewRequest("POST", testBase+DataPath, nil)
	req.AddCookie(b.cookies[sessionName])
	age, ok := e.o.SessionAge(req)
	if !ok || age != 50*time.Minute {
		t.Fatalf("age %v %v", age, ok)
	}
	if _, ok := e.o.SessionAge(httptest.NewRequest("POST", testBase+DataPath, nil)); ok {
		t.Fatal("age without a session")
	}
}
