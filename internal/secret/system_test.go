package secret

import (
	"errors"
	"path/filepath"
	"testing"

	"homestead/internal/store"
)

func TestSystemNamespaceIsWritableOnlyBySetSystem(t *testing.T) {
	st, _ := openSecret(t)
	if err := st.Set(BackupIdentityName, "x"); !errors.Is(err, ErrReservedName) {
		t.Fatalf("owner Set err = %v", err)
	}
	if err := st.SetApp(BackupIdentityName, "x"); err == nil {
		t.Fatal("SetApp wrote a system name")
	}
	if err := st.SetSystem("app_pkg_x", "x"); err == nil {
		t.Fatal("SetSystem wrote an app name")
	}
	if err := st.SetSystem(BackupIdentityName, "AGE-SECRET-KEY-1ABC"); err != nil {
		t.Fatal(err)
	}
}

// An identity stored by the first S259 build under app_backup_identity
// moves to the system namespace on startup, copy before delete, and
// repeated startups change nothing.
func TestLegacyBackupIdentityMigrates(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	keyPath := filepath.Join(dir, KeyFileName)
	st, err := New(s, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	const id = "AGE-SECRET-KEY-1LEGACYIDENTITYVALUE"
	if err := st.SetApp(legacyBackupIdentityName, id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if st, err = New(s, keyPath); err != nil {
			t.Fatal(err)
		}
		if got, err := st.Get(BackupIdentityName); err != nil || got != id {
			t.Fatalf("pass %d: sys identity = %q %v", i, got, err)
		}
		if _, err := st.Get(legacyBackupIdentityName); !errors.Is(err, ErrNotFound) {
			t.Fatalf("pass %d: legacy name remains: %v", i, err)
		}
	}
}

func TestLegacyMigrationNeverDropsADifferentKey(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.Open(filepath.Join(dir, "t.db"))
	defer s.Close()
	keyPath := filepath.Join(dir, KeyFileName)
	st, _ := New(s, keyPath)
	_ = st.SetSystem(BackupIdentityName, "AGE-SECRET-KEY-1NEW")
	_ = st.SetApp(legacyBackupIdentityName, "AGE-SECRET-KEY-1OLD")
	st, err := New(s, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Get(legacyBackupIdentityName); got != "AGE-SECRET-KEY-1OLD" {
		t.Fatalf("old identity lost: %q", got)
	}
	// A catalog secret that merely shares the name is left alone.
	_ = st.Delete(BackupIdentityName)
	_ = st.SetApp(legacyBackupIdentityName, "not-an-age-key")
	st, _ = New(s, keyPath)
	if got, _ := st.Get(legacyBackupIdentityName); got != "not-an-age-key" {
		t.Fatalf("catalog secret moved: %q", got)
	}
}
