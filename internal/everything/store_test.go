package everything

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPrivateDataPersistsAndIsIsolated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	alice := Scope{App: "tasks", Subject: "issuer|alice"}
	bob := Scope{App: "tasks", Subject: "issuer|bob"}
	other := Scope{App: "notes", Subject: "issuer|alice"}
	if err := store.Put(ctx, alice, KindKV, "tasks:items:v1", []byte(`{"tasks":[1]}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, alice, KindFile, "tasks-data/log-2026-10.json", []byte("archive")); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []Scope{bob, other} {
		if _, err := store.Get(ctx, scope, KindKV, "tasks:items:v1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("scope %+v: %v", scope, err)
		}
		if _, err := store.Get(ctx, scope, KindFile, "tasks-data/log-2026-10.json"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("scope %+v: %v", scope, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.Get(ctx, alice, KindKV, "tasks:items:v1")
	if err != nil || string(got) != `{"tasks":[1]}` {
		t.Fatalf("reopen: %q %v", got, err)
	}
	if err := store.Delete(ctx, alice, KindKV, "tasks:items:v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, alice, KindKV, "tasks:items:v1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

func TestRejectsInvalidScopePathsAndOversize(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := Scope{App: "tasks", Subject: "alice"}
	for _, c := range []struct {
		scope Scope
		kind  Kind
		key   string
		data  []byte
	}{
		{Scope{App: "", Subject: "alice"}, KindKV, "key", nil},
		{Scope{App: "tasks", Subject: ""}, KindKV, "key", nil},
		{scope, KindFile, "../other", nil}, {scope, KindFile, "/absolute", nil},
		{scope, KindFile, "a/../b", nil}, {scope, KindFile, "a\\b", nil},
		{scope, Kind(99), "key", nil}, {scope, KindKV, "key", make([]byte, MaxValueBytes+1)},
	} {
		if err := store.Put(context.Background(), c.scope, c.kind, c.key, c.data); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v: %v", c, err)
		}
	}
}

func TestListIsScopedAndTreatsPrefixLiterally(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	a := Scope{App: "tasks", Subject: "alice"}
	b := Scope{App: "tasks", Subject: "bob"}
	for _, c := range []struct {
		scope Scope
		key   string
	}{{a, "list:%:1"}, {a, "list:x:2"}, {a, "list:%:3"}, {b, "list:%:4"}} {
		if err := store.Put(ctx, c.scope, KindKV, c.key, []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := store.List(ctx, a, KindKV, "list:%:")
	if err != nil || len(keys) != 2 || keys[0] != "list:%:1" || keys[1] != "list:%:3" {
		t.Fatalf("keys %v: %v", keys, err)
	}
}

func TestStoreRefusesSymlinkAndInsecureExistingFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "data.db")
	if err := os.WriteFile(file, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(file); err == nil {
		t.Fatal("accepted world-readable store")
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(link); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestBatchIsAtomicOnInvalidInput(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	scope := Scope{App: "tasks", Subject: "alice"}
	if err := store.Put(ctx, scope, KindKV, "one", []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBatch(ctx, scope, KindKV, []Entry{{Key: "one", Value: []byte("new")}, {Key: "", Value: []byte("bad")}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("batch: %v", err)
	}
	got, err := store.Get(ctx, scope, KindKV, "one")
	if err != nil || string(got) != "old" {
		t.Fatalf("partial batch %q %v", got, err)
	}
	if err := store.PutBatch(ctx, scope, KindKV, []Entry{{Key: "one", Value: []byte("new")}, {Key: "two", Value: []byte("ok")}}); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, scope, KindKV, "two")
	if err != nil || string(got) != "ok" {
		t.Fatalf("batch %q %v", got, err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	scope := Scope{App: "tasks", Subject: "alice"}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key:%d", i)
			if err := store.Put(ctx, scope, KindKV, key, []byte(key)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	keys, err := store.List(ctx, scope, KindKV, "key:")
	if err != nil || len(keys) != 20 {
		t.Fatalf("keys %d %v", len(keys), err)
	}
}

func TestSQLiteSnapshotRestoresWithoutDaemon(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	scope := Scope{App: "tasks", Subject: "alice"}
	if err := store.Put(ctx, scope, KindFile, "tasks-data/archive.json", []byte("saved")); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "snapshot.db")
	if _, err := store.db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.Get(ctx, scope, KindFile, "tasks-data/archive.json")
	if err != nil || string(got) != "saved" {
		t.Fatalf("restored %q %v", got, err)
	}
}

func TestConcurrentStoresShareDatabase(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "shared.db")
	a, err := OpenStore(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenStore(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	scope := Scope{App: "tasks", Subject: "alice"}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := a
			if i%2 == 1 {
				target = b
			}
			if err := target.Put(ctx, scope, KindKV, fmt.Sprintf("shared:%d", i), []byte("value")); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	keys, err := a.List(ctx, scope, KindKV, "shared:")
	if err != nil || len(keys) != 30 {
		t.Fatalf("keys %d: %v", len(keys), err)
	}
}

// S372: reading a whole address book (Contacts lists "contact:*" with values)
// must work past the 1000-key list cap, in bounded pages, scoped to the person.
func TestListValuesPagesPastTheKeyListCapAndStaysScoped(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	a := Scope{App: "contacts", Subject: "alice"}
	b := Scope{App: "contacts", Subject: "bob"}
	var batch []Entry
	flush := func() {
		if len(batch) > 0 {
			if err := store.PutBatch(ctx, a, KindKV, batch); err != nil {
				t.Fatal(err)
			}
			batch = nil
		}
	}
	for i := 0; i < 1250; i++ {
		batch = append(batch, Entry{Key: fmt.Sprintf("contact:%05d", i), Value: []byte("v")})
		if len(batch) == 100 {
			flush()
		}
	}
	flush()
	_ = store.Put(ctx, a, KindKV, "other:1", []byte("x"))
	_ = store.Put(ctx, a, KindKV, "contact%", []byte("x"))
	_ = store.Put(ctx, b, KindKV, "contact:bob", []byte("secret"))
	if _, err := store.List(ctx, a, KindKV, "contact:"); err == nil {
		t.Fatal("the key list should still refuse more than 1000 entries")
	}
	var got []Entry
	after, pages := "", 0
	for {
		page, next, err := store.ListValues(ctx, a, KindKV, "contact:", after)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 || len(page) > ListValuesMax {
			t.Fatalf("page %d has %d entries", pages, len(page))
		}
		got = append(got, page...)
		pages++
		if next == "" {
			break
		}
		after = next
	}
	if len(got) != 1250 || pages != 3 {
		t.Fatalf("got %d entries in %d pages", len(got), pages)
	}
	for i, e := range got {
		if e.Key != fmt.Sprintf("contact:%05d", i) || string(e.Value) != "v" {
			t.Fatalf("entry %d = %q", i, e.Key)
		}
	}
	// Bob sees only his own; a file scope and an invalid cursor are refused.
	if page, next, err := store.ListValues(ctx, b, KindKV, "contact:", ""); err != nil || next != "" || len(page) != 1 || string(page[0].Value) != "secret" {
		t.Fatalf("bob: %v %q %v", page, next, err)
	}
	if _, _, err := store.ListValues(ctx, a, KindFile, "contact:", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("file kind: %v", err)
	}
	if _, _, err := store.ListValues(ctx, a, KindKV, "contact:", strings.Repeat("x", 2000)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long cursor: %v", err)
	}
}

func TestListValuesPagesByBytes(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	s := Scope{App: "contacts", Subject: "alice"}
	big := make([]byte, 3*1024*1024)
	for i := 0; i < 3; i++ {
		if err := store.Put(ctx, s, KindKV, fmt.Sprintf("k%d", i), big); err != nil {
			t.Fatal(err)
		}
	}
	page, next, err := store.ListValues(ctx, s, KindKV, "k", "")
	if err != nil || len(page) != 1 || next != "k0" {
		t.Fatalf("first page: %d entries, next %q, %v", len(page), next, err)
	}
	page, next, err = store.ListValues(ctx, s, KindKV, "k", next)
	if err != nil || len(page) != 1 || page[0].Key != "k1" || next != "k1" {
		t.Fatalf("second page: %v %q %v", page, next, err)
	}
}
