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

func TestMigrationV2SeedsUnclaimedAppliance(t *testing.T) {
	s := openTestStore(t)
	version, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != CurrentVersion || CurrentVersion < 2 {
		t.Fatalf("SchemaVersion = %d, want >= 2", version)
	}
	var state string
	if err := s.DB().QueryRow(`SELECT state FROM appliance WHERE id = 1`).Scan(&state); err != nil {
		t.Fatalf("appliance state: %v", err)
	}
	if state != "UNCLAIMED" {
		t.Fatalf("state = %q, want UNCLAIMED", state)
	}
}

func TestSessionTransportMigration(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, idx := range []string{"sessions_token_hash_unique", "sessions_identity"} {
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s missing (n=%d, err=%v)", idx, n, err)
		}
	}
	for _, col := range []string{"csrf_hash", "last_used_at"} {
		if _, err := s.DB().Exec(`SELECT ` + col + ` FROM sessions LIMIT 1`); err != nil {
			t.Fatalf("sessions.%s missing: %v", col, err)
		}
	}
}

func TestUpgradeFromV23PopulatedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), DBFileName)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind to a populated v23 database: schedules exist, the scheduler
	// table and later columns do not.
	for _, stmt := range []string{
		`INSERT INTO backup_schedules (package, interval_seconds, retain, enabled, actor, updated_at)
		 VALUES ('photos', 86400, 7, 1, 'owner', '2026-10-01T00:00:00Z')`,
		`DROP TABLE scheduler_jobs`,
		`ALTER TABLE secrets DROP COLUMN key_version`,
		`DELETE FROM schema_migrations WHERE version >= 24`,
	} {
		if _, err := s.DB().Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	up, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer up.Close()
	version, err := up.SchemaVersion()
	if err != nil || version != CurrentVersion {
		t.Fatalf("version = %d, %v", version, err)
	}
	var interval int
	if err := up.DB().QueryRow(`SELECT interval_seconds FROM backup_schedules WHERE package = 'photos'`).Scan(&interval); err != nil || interval != 86400 {
		t.Fatalf("schedule lost: %d, %v", interval, err)
	}
	if _, err := up.DB().Exec(`INSERT INTO scheduler_jobs (job_id) VALUES ('backup:photos')`); err != nil {
		t.Fatalf("scheduler_jobs: %v", err)
	}
}
