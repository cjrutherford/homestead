package secret

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/store"
)

func openSecret(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	keyPath := filepath.Join(dir, KeyFileName)
	st, err := New(s, keyPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return st, dir
}

func TestRoundTrip(t *testing.T) {
	st, dir := openSecret(t)
	if err := st.Set("api_token", "secret-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := st.Get("api_token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "secret-value" {
		t.Fatalf("Get = %q", got)
	}
	// Overwrite replaces.
	if err := st.Set("api_token", "rotated"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err = st.Get("api_token")
	if err != nil || got != "rotated" {
		t.Fatalf("Get = %q %v", got, err)
	}
	// Key file is root-only.
	info, err := os.Stat(filepath.Join(dir, KeyFileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o", info.Mode().Perm())
	}
	// Ciphertext at rest, never the value.
	var count int
	db := storeFor(t, dir)
	err = db.DB().QueryRow(
		`SELECT COUNT(*) FROM secrets WHERE CAST(ciphertext AS TEXT) LIKE '%secret-value%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 0 {
		t.Fatal("value stored in cleartext")
	}
}

func storeFor(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNotFoundAndDelete(t *testing.T) {
	st, _ := openSecret(t)
	if _, err := st.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	// Delete converges on absent names.
	if err := st.Delete("missing"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := st.Set("temp", "x"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := st.Delete("temp"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Get("temp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestListRedacts(t *testing.T) {
	st, _ := openSecret(t)
	if err := st.Set("one", "1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := st.Set("two", "2"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	infos, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 || infos[0].Name != "one" || infos[1].Name != "two" {
		t.Fatalf("List = %+v", infos)
	}
	if infos[0].UpdatedAt.IsZero() {
		t.Fatal("missing timestamp")
	}
}

func TestValidation(t *testing.T) {
	st, _ := openSecret(t)
	for _, name := range []string{"", "Bad", "has space", "UPPER", strings.Repeat("a", 65)} {
		if err := st.Set(name, "v"); err == nil {
			t.Fatalf("accepted name %q", name)
		}
		if _, err := st.Get(name); err == nil {
			t.Fatalf("got name %q", name)
		}
		if err := st.Delete(name); err == nil {
			t.Fatalf("deleted name %q", name)
		}
	}
	if err := st.Set("ok", ""); err == nil {
		t.Fatal("accepted empty value")
	}
	if err := st.Set("ok", strings.Repeat("x", maxValueBytes+1)); err == nil {
		t.Fatal("accepted oversize value")
	}
}

func TestReopenPersistence(t *testing.T) {
	st, dir := openSecret(t)
	if err := st.Set("api_token", "persisted"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	second, err := New(storeFor(t, dir), filepath.Join(dir, KeyFileName))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := second.Get("api_token")
	if err != nil || got != "persisted" {
		t.Fatalf("Get = %q %v", got, err)
	}
}

func TestWrongKeyFailsClosed(t *testing.T) {
	st, dir := openSecret(t)
	if err := st.Set("api_token", "secret-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	other, err := New(storeFor(t, dir), filepath.Join(t.TempDir(), KeyFileName))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := other.Get("api_token"); err == nil {
		t.Fatal("decrypted with wrong key")
	}
}

func TestExposedKeyRefused(t *testing.T) {
	_, dir := openSecret(t)
	keyPath := filepath.Join(dir, KeyFileName)
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if _, err := New(storeFor(t, dir), keyPath); err == nil ||
		!strings.Contains(err.Error(), "must not be group/other readable") {
		t.Fatalf("err = %v", err)
	}
}

func TestShortKeyRefused(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, KeyFileName)
	if err := os.WriteFile(keyPath, []byte("short"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := New(storeFor(t, dir), keyPath); err == nil ||
		!strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("err = %v", err)
	}
}
