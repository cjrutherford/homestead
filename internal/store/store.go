// Package store owns Homestead's SQLite operational state (S013; see ADR-003).
//
// The store opens (or creates) the database, enforces connection pragmas,
// and applies ordered, idempotent schema migrations. Domain read/write APIs
// belong to the owning slices — operations (S060+), identity (S020),
// sessions (S032), onboarding wizards (S036+) — which extend the skeleton
// tables below through new migrations; this package provides the versioned
// handle they build on.
//
// The driver is modernc.org/sqlite (pure Go, no cgo) so the daemon stays a
// self-contained static binary per ADR-001.
package store

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"homestead/internal/layout"

	_ "modernc.org/sqlite"
)

// DBFileName is the operational database file inside LibDir.
const DBFileName = "homestead.db"

// CurrentVersion is the latest schema version. Each new migration bumps it.
const CurrentVersion = 4

// DefaultPath returns the production database path. The parent directory is
// provisioned by S012 (layout.Ensure / provision-layout.sh); Open does not
// create parents, so a missing LibDir fails loudly instead of scattering
// state.
func DefaultPath() string {
	return filepath.Join(layout.LibDir.Path, DBFileName)
}

type migration struct {
	version int
	name    string
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		name:    "base domain tables",
		sql: `
CREATE TABLE IF NOT EXISTS operations (
	id INTEGER PRIMARY KEY,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS identities (
	id INTEGER PRIMARY KEY,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	id INTEGER PRIMARY KEY,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS onboarding (
	id INTEGER PRIMARY KEY,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S019 secure claim: appliance state singleton plus single-use
		// tokens. Plaintext tokens never touch the database — only
		// SHA-256 hashes. First-administrator records belong to S020.
		version: 2,
		name:    "appliance claim state",
		sql: `
CREATE TABLE IF NOT EXISTS appliance (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	state TEXT NOT NULL DEFAULT 'UNCLAIMED'
);
INSERT INTO appliance (id, state) SELECT 1, 'UNCLAIMED'
	WHERE NOT EXISTS (SELECT 1 FROM appliance WHERE id = 1);
CREATE TABLE IF NOT EXISTS claim_tokens (
	id INTEGER PRIMARY KEY,
	token_hash BLOB NOT NULL UNIQUE,
	expires_at TEXT NOT NULL,
	used_at TEXT,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);`,
	},
	{
		// S020 administrator authentication: first-administrator
		// credentials on identities plus durable session tokens.
		// Enforcement on other endpoints arrives in later slices.
		version: 3,
		name:    "administrator auth",
		sql: `
ALTER TABLE identities ADD COLUMN username TEXT;
ALTER TABLE identities ADD COLUMN password_hash BLOB;
CREATE UNIQUE INDEX IF NOT EXISTS identities_username_unique
	ON identities(username) WHERE username IS NOT NULL;
ALTER TABLE sessions ADD COLUMN identity_id INTEGER REFERENCES identities(id);
ALTER TABLE sessions ADD COLUMN token_hash BLOB;
ALTER TABLE sessions ADD COLUMN expires_at TEXT NOT NULL DEFAULT '';`,
	},
	{
		// S036 resumable wizards: per-wizard step plus opaque JSON payload.
		// Payloads hold UI progress only — never secrets (claim tokens and
		// passwords stay out; see S019/S020).
		version: 4,
		name:    "wizard state",
		sql: `
CREATE TABLE IF NOT EXISTS wizard_state (
	wizard TEXT PRIMARY KEY,
	step TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT '{}',
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);`,
	},
}

// Store is an open, migrated operational database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if absent) the database at path and migrates it to
// CurrentVersion. Writes serialize through a single connection; WAL mode
// keeps readers unblocked and busy_timeout absorbs transient locks.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.applyPragmas(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) applyPragmas() error {
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := s.db.Exec(pragma); err != nil {
			return fmt.Errorf("store: pragma %q: %w", pragma, err)
		}
	}
	return nil
}

func (s *Store) migrate() error {
	const initLedger = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);`
	if _, err := s.db.Exec(initLedger); err != nil {
		return fmt.Errorf("store: init migration ledger: %w", err)
	}
	var applied int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&applied); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	for _, m := range migrations {
		if m.version <= applied {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: migration %d begin: %w", m.version, err)
	}
	defer tx.Rollback() // no-op after successful Commit
	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("store: migration %d (%s): %w", m.version, m.name, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
		return fmt.Errorf("store: record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %d commit: %w", m.version, err)
	}
	return nil
}

// SchemaVersion reports the highest applied migration version.
func (s *Store) SchemaVersion() (int, error) {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	return version, nil
}

// DB exposes the underlying handle for domain packages owned by later slices.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close releases the database.
func (s *Store) Close() error {
	return s.db.Close()
}
