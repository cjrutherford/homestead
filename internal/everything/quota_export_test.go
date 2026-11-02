package everything

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bridge.db")
	s, err := OpenStore(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p
}

func TestPersonAndAppQuota(t *testing.T) {
	s, _ := newTestStore(t)
	s.SetQuota(1000, 1500)
	ctx := context.Background()
	a := Scope{App: "tasks", Subject: "a"}
	b := Scope{App: "tasks", Subject: "b"}
	c := Scope{App: "tasks", Subject: "c"}
	big := bytes.Repeat([]byte("x"), 900)
	if err := s.Put(ctx, a, KindKV, "k", big); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, a, KindFile, "f", big); !errors.Is(err, ErrQuota) {
		t.Fatalf("person quota: %v", err)
	}
	// Replacing in place and shrinking stay allowed.
	if err := s.Put(ctx, a, KindKV, "k", big[:100]); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, b, KindKV, "k", big); err != nil {
		t.Fatal(err)
	}
	// App total: a(~101)+b(~901)=~1002; c adding 900 -> over 1500.
	if err := s.Put(ctx, c, KindKV, "k", big); !errors.Is(err, ErrQuota) {
		t.Fatalf("app quota: %v", err)
	}
	// A batch that crosses the quota is rejected whole.
	err := s.PutBatch(ctx, a, KindKV, []Entry{{"s1", big[:300]}, {"s2", big[:300]}, {"s3", big[:400]}})
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("batch: %v", err)
	}
	for _, k := range []string{"s1", "s2", "s3"} {
		if _, err := s.Get(ctx, a, KindKV, k); !errors.Is(err, ErrNotFound) {
			t.Fatalf("partial batch %s survived: %v", k, err)
		}
	}
	// Another application is unaffected.
	if err := s.Put(ctx, Scope{App: "notes", Subject: "a"}, KindKV, "k", big); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaRacingWriters(t *testing.T) {
	s, _ := newTestStore(t)
	const limit = 10_000
	s.SetQuota(limit, 1<<30)
	ctx := context.Background()
	sc := Scope{App: "tasks", Subject: "racer"}
	var wg sync.WaitGroup
	var ok, denied int
	var mu sync.Mutex
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.PutBatch(ctx, sc, KindKV, []Entry{{fmt.Sprintf("a%02d", i), make([]byte, 500)}, {fmt.Sprintf("b%02d", i), make([]byte, 500)}})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrQuota):
				denied++
			default:
				t.Errorf("unexpected: %v", err)
			}
		}(i)
	}
	wg.Wait()
	p, _, err := usage(ctx, mustConn(t, s), sc)
	if err != nil {
		t.Fatal(err)
	}
	if p > limit || ok == 0 || denied == 0 || ok+denied != 60 {
		t.Fatalf("usage %d limit %d ok %d denied %d", p, limit, ok, denied)
	}
}

func TestHandlerQuotaIs413AndHealthz(t *testing.T) {
	s, _ := newTestStore(t)
	s.SetQuota(200, 0)
	id := fakeIdentity{scope: Scope{App: "tasks", Subject: "alice"}, csrf: true}
	srv, err := NewServerAt("/apps/tasks", s, id, []string{"https://home.example"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"operation": "put", "kind": "kv", "key": "k", "value": bytes.Repeat([]byte("x"), 400)})
	r := httptest.NewRequest("POST", "/apps/tasks"+DataPath, bytes.NewReader(body))
	r.Header.Set("Origin", "https://home.example")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "quota_exceeded") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	h := httptest.NewRecorder()
	srv.ServeHTTP(h, httptest.NewRequest("GET", "/apps/tasks"+HealthPath, nil))
	if h.Code != 200 || h.Header().Get("Cache-Control") != "no-store" || strings.TrimSpace(h.Body.String()) != "ok" {
		t.Fatalf("healthz %d %q", h.Code, h.Body.String())
	}
	h = httptest.NewRecorder()
	srv.ServeHTTP(h, httptest.NewRequest("POST", "/apps/tasks"+HealthPath, nil))
	if h.Code != 405 {
		t.Fatalf("healthz POST %d", h.Code)
	}
}

func seed(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	al, bo := Scope{App: "tasks", Subject: "alice"}, Scope{App: "tasks", Subject: "bob"}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Put(ctx, al, KindKV, "tasks:items:v1", []byte(`{"tasks":[1,2]}`)))
	must(s.Put(ctx, al, KindKV, "raw", []byte{0xff, 0x00, 0x01}))
	must(s.Put(ctx, al, KindFile, "tasks-data/log-2026-10.json", []byte("archive")))
	must(s.Put(ctx, bo, KindKV, "tasks:items:v1", []byte(`{"tasks":[9]}`)))
	must(s.Put(ctx, Scope{App: "notes", Subject: "alice"}, KindKV, "n", []byte(`"x"`)))
}

func TestExportOnePersonAndAllRoundTrip(t *testing.T) {
	s, p := newTestStore(t)
	seed(t, s)
	ctx := context.Background()
	// A live session must never appear in an export.
	if _, _, err := s.createSession(ctx, "tasks", "alice", timeNow()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	out := filepath.Join(t.TempDir(), "one")
	n, err := Export(ctx, p, "alice", "tasks", out)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	if strings.Contains(string(raw), "bob") || strings.Contains(string(raw), "session") {
		t.Fatalf("leak: %s", raw)
	}
	var found bool
	filepath.WalkDir(out, func(path string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && strings.HasSuffix(path, "log-2026-10.json") {
			b, _ := os.ReadFile(path)
			found = string(b) == "archive"
		}
		return nil
	})
	if !found {
		t.Fatal("file body not exported")
	}
	if _, err := Export(ctx, p, "alice", "tasks", out); err == nil {
		t.Fatal("export into a non-empty directory must fail")
	}
	all := filepath.Join(t.TempDir(), "all")
	if n, err = Export(ctx, p, "", "", all); err != nil || n != 5 {
		t.Fatalf("all n=%d err=%v", n, err)
	}
	if _, err := Export(ctx, filepath.Join(t.TempDir(), "missing.db"), "", "", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("missing file must fail, not create one")
	}
	// Import into a fresh store restores everything byte for byte.
	s2, _ := newTestStore(t)
	if n, err = Import(ctx, s2, all); err != nil || n != 5 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	al := Scope{App: "tasks", Subject: "alice"}
	for _, c := range []struct {
		k    Kind
		key  string
		want []byte
	}{{KindKV, "tasks:items:v1", []byte(`{"tasks":[1,2]}`)}, {KindKV, "raw", []byte{0xff, 0, 1}}, {KindFile, "tasks-data/log-2026-10.json", []byte("archive")}} {
		got, err := s2.Get(ctx, al, c.k, c.key)
		if err != nil || !bytes.Equal(got, c.want) {
			t.Fatalf("%s: %q %v", c.key, got, err)
		}
	}
}

func TestBackupRestoreDropsSessionsKeepsData(t *testing.T) {
	s, p := newTestStore(t)
	seed(t, s)
	ctx := context.Background()
	id, _, err := s.createSession(ctx, "tasks", "alice", timeNow())
	if err != nil {
		t.Fatal(err)
	}
	// Online snapshot while the store is open and in use.
	dst := filepath.Join(t.TempDir(), "backup.db")
	if err := Backup(ctx, p, dst); err != nil {
		t.Fatal(err)
	}
	if err := Backup(ctx, p, dst); err == nil {
		t.Fatal("must not overwrite a backup")
	}
	if st, _ := os.Stat(dst); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	// Restore = copy the file over a fresh data path, then open it.
	restored := filepath.Join(t.TempDir(), "restored.db")
	b, _ := os.ReadFile(dst)
	if err := os.WriteFile(restored, b, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := OpenStore(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := r.Get(ctx, Scope{App: "tasks", Subject: "alice"}, KindKV, "tasks:items:v1")
	if err != nil || string(got) != `{"tasks":[1,2]}` {
		t.Fatalf("%q %v", got, err)
	}
	if _, ok := r.lookupSession(ctx, id, "tasks", timeNow(), time.Hour, time.Hour); ok {
		t.Fatal("a session must not survive a restore")
	}
	if _, ok := s.lookupSession(ctx, id, "tasks", timeNow(), time.Hour, time.Hour); !ok {
		t.Fatal("the original keeps its session")
	}
}
func timeNow() time.Time { return time.Now() }

func mustConn(t *testing.T, s *Store) *sql.Conn {
	c, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
