package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/layout"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	return s
}

func TestOpenCreatesAndMigrates(t *testing.T) {
	s := openTestStore(t)
	version, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != CurrentVersion {
		t.Fatalf("SchemaVersion = %d, want %d", version, CurrentVersion)
	}
}

func TestDomainTablesExist(t *testing.T) {
	s := openTestStore(t)
	rows, err := s.DB().Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	for _, table := range []string{"schema_migrations", "operations", "identities", "sessions", "onboarding"} {
		if !found[table] {
			t.Fatalf("table %q missing", table)
		}
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()
	version, err := second.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != CurrentVersion {
		t.Fatalf("SchemaVersion after reopen = %d, want %d", version, CurrentVersion)
	}
	var count int
	if err := second.DB().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != len(migrations) {
		t.Fatalf("migration records = %d, want %d", count, len(migrations))
	}
}

func TestTablesAreWritable(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB().Exec(`INSERT INTO operations (created_at, updated_at) VALUES (?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	var created string
	if err := s.DB().QueryRow(`SELECT created_at FROM operations WHERE id = ?`, id).Scan(&created); err != nil {
		t.Fatalf("select: %v", err)
	}
	if created != now {
		t.Fatalf("created_at = %q, want %q", created, now)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	s := openTestStore(t)
	var enabled bool
	if err := s.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if !enabled {
		t.Fatal("foreign_keys pragma is off")
	}
}

func TestJournalModeIsWAL(t *testing.T) {
	s := openTestStore(t)
	var mode string
	if err := s.DB().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestDefaultPathLivesUnderLibDir(t *testing.T) {
	path := DefaultPath()
	if filepath.Dir(path) != layout.LibDir.Path {
		t.Fatalf("DefaultPath dir = %q, want %q", filepath.Dir(path), layout.LibDir.Path)
	}
	if filepath.Base(path) != DBFileName {
		t.Fatalf("DefaultPath base = %q, want %q", filepath.Base(path), DBFileName)
	}
}

func TestOpenMissingParentFailsLoudly(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "test.db")
	if _, err := Open(missing); err == nil {
		t.Fatal("Open with missing parent dir succeeded, want error")
	}
}
