package everything

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// S369: the file operations an app with real folders needs (Projects).
func TestStatReaddirAndTreeDeleteAreScopedAndExact(t *testing.T) {
	store := openTest(t)
	ctx := context.Background()
	alice := Scope{App: "projects", Subject: "alice"}
	bob := Scope{App: "projects", Subject: "bob"}
	other := Scope{App: "tasks", Subject: "alice"}
	for k, v := range map[string]string{
		"d/snapshots/p1/2026-01-01.json": "aa",
		"d/snapshots/p1/2026-01-02.json": "bbb",
		"d/snapshots/p10/x.json":         "keep-sibling",
		"d/project-1.json":               "doc",
		"d/attachments/p1/t1/a.txt":      "att",
	} {
		if err := store.Put(ctx, alice, KindFile, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Put(ctx, bob, KindFile, "d/snapshots/p1/bob.json", []byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, other, KindFile, "d/snapshots/p1/other.json", []byte("o")); err != nil {
		t.Fatal(err)
	}

	list, err := store.Readdir(ctx, alice, "d/snapshots/p1")
	if err != nil || len(list) != 2 || list[0].Name != "2026-01-01.json" || list[0].Size != 2 || list[1].Size != 3 || list[0].IsDir || list[0].Modified == 0 {
		t.Fatalf("readdir %+v %v", list, err)
	}
	top, err := store.Readdir(ctx, alice, "d")
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]bool{}
	for _, e := range top {
		dirs[e.Name] = e.IsDir
	}
	if len(top) != 3 || !dirs["snapshots"] || !dirs["attachments"] || dirs["project-1.json"] {
		t.Fatalf("top level %+v", top)
	}
	// Prefix matching is by whole path component, never by substring.
	if _, err := store.Readdir(ctx, alice, "d/snapshots/p"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial name listed: %v", err)
	}
	if _, err := store.Readdir(ctx, alice, "d/nothing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing dir: %v", err)
	}
	// Other people and other apps see none of it.
	if l, err := store.Readdir(ctx, bob, "d/snapshots/p1"); err != nil || len(l) != 1 || l[0].Name != "bob.json" {
		t.Fatalf("bob %+v %v", l, err)
	}
	if _, err := store.Stat(ctx, bob, "d/project-1.json"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob stat: %v", err)
	}

	file, err := store.Stat(ctx, alice, "d/project-1.json")
	if err != nil || file.IsDir || file.Size != 3 || file.Name != "project-1.json" {
		t.Fatalf("stat file %+v %v", file, err)
	}
	dir, err := store.Stat(ctx, alice, "d/snapshots")
	if err != nil || !dir.IsDir || dir.Modified == 0 {
		t.Fatalf("stat dir %+v %v", dir, err)
	}

	// A recursive delete removes the tree and not the lookalike sibling, nor
	// anyone else's tree.
	if n, err := store.DeleteTree(ctx, alice, "d/snapshots/p1"); err != nil || n != 2 {
		t.Fatalf("tree delete %d %v", n, err)
	}
	if _, err := store.Stat(ctx, alice, "d/snapshots/p10/x.json"); err != nil {
		t.Fatalf("sibling removed: %v", err)
	}
	if _, err := store.Stat(ctx, bob, "d/snapshots/p1/bob.json"); err != nil {
		t.Fatalf("other person's tree removed: %v", err)
	}
	if _, err := store.Stat(ctx, other, "d/snapshots/p1/other.json"); err != nil {
		t.Fatalf("other app's tree removed: %v", err)
	}
	if n, err := store.DeleteTree(ctx, alice, "d/snapshots/p1"); err != nil || n != 0 {
		t.Fatalf("second delete %d %v", n, err)
	}
	for _, bad := range []string{"", "/abs", "../x", "a/../b", "a//b", `a\b`, "."} {
		if _, err := store.Stat(ctx, alice, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("stat %q: %v", bad, err)
		}
		if _, err := store.Readdir(ctx, alice, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("readdir %q: %v", bad, err)
		}
		if _, err := store.DeleteTree(ctx, alice, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("deletetree %q: %v", bad, err)
		}
	}
}

func TestModifiedStampStrictlyIncreasesOnEveryRewrite(t *testing.T) {
	store := openTest(t)
	ctx := context.Background()
	s := Scope{App: "projects", Subject: "alice"}
	var last int64
	for i := 0; i < 20; i++ {
		// Same bytes, same millisecond: the stamp must still move.
		if err := store.Put(ctx, s, KindFile, "d/p.json", []byte("same")); err != nil {
			t.Fatal(err)
		}
		fi, err := store.Stat(ctx, s, "d/p.json")
		if err != nil || fi.Modified <= last {
			t.Fatalf("rewrite %d stamp %d after %d (%v)", i, fi.Modified, last, err)
		}
		last = fi.Modified
	}
}

func TestFileWritesStayUnderTheQuota(t *testing.T) {
	store := openTest(t)
	store.SetQuota(1000, 0)
	ctx := context.Background()
	s := Scope{App: "projects", Subject: "alice"}
	if err := store.Put(ctx, s, KindFile, "d/a", bytes.Repeat([]byte("x"), 800)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, s, KindFile, "d/b", bytes.Repeat([]byte("x"), 800)); !errors.Is(err, ErrQuota) {
		t.Fatalf("over quota write: %v", err)
	}
	// Deleting a tree frees the space.
	if _, err := store.DeleteTree(ctx, s, "d"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, s, KindFile, "d/b", bytes.Repeat([]byte("x"), 800)); err != nil {
		t.Fatalf("after delete: %v", err)
	}
}

// A database created before S369 has no modified column; opening upgrades it
// in place and keeps the data.
func TestOpenUpgradesAnOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE objects`,
		`CREATE TABLE objects (app TEXT NOT NULL, subject TEXT NOT NULL, kind INTEGER NOT NULL, object_key TEXT NOT NULL, value BLOB NOT NULL, PRIMARY KEY (app, subject, kind, object_key)) WITHOUT ROWID`,
		`INSERT INTO objects VALUES('projects','alice',2,'d/old.json','legacy')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	for i := 0; i < 2; i++ { // twice: the upgrade is idempotent
		store, err = OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		s := Scope{App: "projects", Subject: "alice"}
		fi, err := store.Stat(context.Background(), s, "d/old.json")
		if err != nil || (i == 0 && (fi.Size != 6 || fi.Modified != 0)) {
			t.Fatalf("legacy row %+v %v", fi, err)
		}
		if err = store.Put(context.Background(), s, KindFile, "d/old.json", []byte("new")); err != nil {
			t.Fatal(err)
		}
		if fi, _ = store.Stat(context.Background(), s, "d/old.json"); fi.Modified == 0 {
			t.Fatal("rewrite of a legacy row has no stamp")
		}
		store.Close()
	}
}

func TestHandlerFileOperations(t *testing.T) {
	store := openTest(t)
	mk := func(sub string) func(string) *httptest.ResponseRecorder {
		h, err := NewHandler(store, fakeIdentity{scope: Scope{App: "projects", Subject: sub}, csrf: true}, []string{"https://home.example"})
		if err != nil {
			t.Fatal(err)
		}
		return func(body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest("POST", "https://home.example/bridge", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "https://home.example")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec
		}
	}
	alice, bob := mk("alice"), mk("bob")
	rec := alice(`{"operation":"put","kind":"file","key":"d/s/1.json","value":"aGk="}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"size":2`) || !strings.Contains(rec.Body.String(), `"modified":`) {
		t.Fatalf("put reports size and stamp: %d %s", rec.Code, rec.Body.String())
	}
	if rec = alice(`{"operation":"put","kind":"kv","key":"k","value":"aGk="}`); strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("kv put %s", rec.Body.String())
	}
	if rec = alice(`{"operation":"readdir","kind":"file","key":"d/s"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"1.json"`) {
		t.Fatalf("readdir %d %s", rec.Code, rec.Body.String())
	}
	if rec = alice(`{"operation":"stat","kind":"file","key":"d/s/1.json"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"is_dir":false`) {
		t.Fatalf("stat %d %s", rec.Code, rec.Body.String())
	}
	for _, body := range []string{
		`{"operation":"stat","kind":"file","key":"d/s/1.json"}`,
		`{"operation":"readdir","kind":"file","key":"d/s"}`,
	} {
		if rec = bob(body); rec.Code != 404 {
			t.Fatalf("cross-person %s: %d", body, rec.Code)
		}
	}
	if rec = bob(`{"operation":"deletetree","kind":"file","key":"d"}`); rec.Code != 200 {
		t.Fatalf("bob delete %d", rec.Code)
	}
	if rec = alice(`{"operation":"stat","kind":"file","key":"d/s/1.json"}`); rec.Code != 200 {
		t.Fatal("bob's tree delete touched alice")
	}
	for _, body := range []string{
		`{"operation":"stat","kind":"kv","key":"k"}`,
		`{"operation":"readdir","kind":"kv","key":"k"}`,
		`{"operation":"deletetree","kind":"kv","key":"k"}`,
		`{"operation":"stat","kind":"file","key":"../x"}`,
		`{"operation":"readdir","kind":"file","key":"/etc"}`,
		`{"operation":"deletetree","kind":"file","key":"a/../../b"}`,
	} {
		if rec = alice(body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	if rec = alice(`{"operation":"deletetree","kind":"file","key":"d"}`); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec = alice(`{"operation":"stat","kind":"file","key":"d/s/1.json"}`); rec.Code != 404 {
		t.Fatalf("after tree delete %d", rec.Code)
	}
}
