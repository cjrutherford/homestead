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
const CurrentVersion = 18

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
	{
		// S060 operation domain: typed operations with status/result/error
		// plus ordered steps. Execution (S061), events (S062), and audit
		// (S063) build on these records.
		version: 5,
		name:    "operation records",
		sql: `
ALTER TABLE operations ADD COLUMN type TEXT NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN status TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE operations ADD COLUMN payload TEXT NOT NULL DEFAULT '{}';
ALTER TABLE operations ADD COLUMN result TEXT NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN error TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS operation_steps (
	id INTEGER PRIMARY KEY,
	operation_id INTEGER NOT NULL REFERENCES operations(id),
	position INTEGER NOT NULL,
	name TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	started_at TEXT NOT NULL DEFAULT '',
	finished_at TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS operation_steps_operation
	ON operation_steps(operation_id, position);`,
	},
	{
		// S063 audit trail: append-only actor/action/plan/outcome records
		// including owner overrides. No update or delete path exists.
		version: 6,
		name:    "audit trail",
		sql: `
CREATE TABLE IF NOT EXISTS audit_entries (
	id INTEGER PRIMARY KEY,
	actor TEXT NOT NULL,
	action TEXT NOT NULL,
	plan TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	override INTEGER NOT NULL DEFAULT 0,
	override_reason TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_entries_actor
	ON audit_entries(actor, id);`,
	},
	{
		// S096 secret store: DNS/API secret values live here encrypted
		// (AES-GCM, key in a root-only file — see internal/secret), never
		// in desired YAML, logs, or API responses. The table holds nonce
		// plus ciphertext only; updates replace rows outright.
		version: 7,
		name:    "secret store",
		sql: `
CREATE TABLE IF NOT EXISTS secrets (
	name TEXT PRIMARY KEY,
	nonce BLOB NOT NULL,
	ciphertext BLOB NOT NULL,
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S125 owner overrides: current per-host exposure exceptions
		// with reasons, actors, and management flags. The append-only
		// audit trail (S063) records every grant and revocation; this
		// table holds the live set readers enforce.
		version: 8,
		name:    "exposure overrides",
		sql: `
CREATE TABLE IF NOT EXISTS exposure_overrides (
	host TEXT PRIMARY KEY,
	to_exposure TEXT NOT NULL,
	reason TEXT NOT NULL,
	actor TEXT NOT NULL,
	management INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);`,
	},
	{
		// S141 mount registry: owner-approved storage locations.
		// Registration references discovered mounts by mountpoint; the
		// table holds approval metadata only, never mount facts, so
		// re-discovery never orphans validation logic.
		version: 9,
		name:    "storage registrations",
		sql: `
CREATE TABLE IF NOT EXISTS storage_registrations (
	mountpoint TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	actor TEXT NOT NULL,
	added_at TEXT NOT NULL
);`,
	},
	{
		// S142 storage assignments: one mount per class. Classes share
		// mounts (different subdirectories, provisioned in S143), but
		// each class lives in exactly one place so data never scatters
		// across mounts under one class name.
		version: 10,
		name:    "storage assignments",
		sql: `
CREATE TABLE IF NOT EXISTS storage_assignments (
	class TEXT PRIMARY KEY,
	mountpoint TEXT NOT NULL,
	actor TEXT NOT NULL,
	created_at TEXT NOT NULL
);`,
	},
	{
		// S183 backup schedules: one interval per package with retention
		// counts and enablement. Runs execute through the operation
		// engine (results live there); wall-clock triggering belongs to
		// timers, not this table.
		version: 11,
		name:    "backup schedules",
		sql: `
CREATE TABLE IF NOT EXISTS backup_schedules (
	package TEXT PRIMARY KEY,
	interval_seconds INTEGER NOT NULL,
	retain INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1,
	actor TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S200 login attempts: per-username failure counts with window
		// starts and lockout deadlines. Empty timestamps mean unset;
		// successful logins delete the row outright.
		version: 12,
		name:    "login attempts",
		sql: `
CREATE TABLE IF NOT EXISTS login_attempts (
	username TEXT PRIMARY KEY,
	failures INTEGER NOT NULL DEFAULT 0,
	first_failure_at TEXT NOT NULL DEFAULT '',
	locked_until TEXT NOT NULL DEFAULT ''
);`,
	},
	{
		// S204 SSO integrations: one recorded intent per application
		// with provider, protocol, and acting owner. Client
		// configuration inside apps belongs to actuation; this table
		// records what the wizard reviewed, replaceable per package.
		version: 13,
		name:    "sso integrations",
		sql: `
CREATE TABLE IF NOT EXISTS sso_integrations (
	package TEXT PRIMARY KEY,
	provider TEXT NOT NULL,
	protocol TEXT NOT NULL,
	actor TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S212 generated fingerprints: recorded hashes of managed
		// stack files per stack, re-baselined on every Homestead
		// write so later edits read as drift. Resolution only reads
		// this table; recording owns it.
		version: 14,
		name:    "generated fingerprints",
		sql: `
CREATE TABLE IF NOT EXISTS generated_fingerprints (
	stack TEXT NOT NULL,
	path TEXT NOT NULL,
	sha256 TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (stack, path)
);`,
	},
	{
		// S213 recorded content: viewing and restoring drift needs
		// the managed bytes, not just their hashes. Earlier rows
		// carry empty content until the next Homestead write
		// re-baselines them.
		version: 15,
		name:    "fingerprint content",
		sql:     `ALTER TABLE generated_fingerprints ADD COLUMN content TEXT NOT NULL DEFAULT '';`,
	},
	{
		// S223 connected domain: the single onboarded customer
		// domain per appliance (single row by construction).
		// Bootstrap slices after onboarding read it; the wizard
		// selection stays the input, this table the decision.
		version: 16,
		name:    "domain onboarding",
		sql: `
CREATE TABLE IF NOT EXISTS domain_onboarding (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	provider TEXT NOT NULL,
	zone TEXT NOT NULL,
	base TEXT NOT NULL,
	fqdn TEXT NOT NULL,
	actor TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S224 split-DNS state: the established zone, FQDN, and LAN
		// answer per appliance (single row by construction).
		// Re-establishing replaces; later bootstrap slices read it.
		version: 17,
		name:    "split DNS state",
		sql: `
CREATE TABLE IF NOT EXISTS split_dns_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	zone TEXT NOT NULL,
	fqdn TEXT NOT NULL,
	lan TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S225 overlay state: the reached overlay stage per
		// appliance (single row by construction) — provider plus
		// node addresses as JSON, "none" when local-only skips the
		// stage. Later bootstrap slices read it.
		version: 18,
		name:    "overlay state",
		sql: `
CREATE TABLE IF NOT EXISTS overlay_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	provider TEXT NOT NULL,
	addresses TEXT NOT NULL,
	updated_at TEXT NOT NULL
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
