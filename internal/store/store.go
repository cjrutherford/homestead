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
const CurrentVersion = 41

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
	{
		// S226 TLS readiness: the decided certificate strategy per
		// appliance (single row by construction). Reaching readiness
		// decides; issuance belongs to later slices.
		version: 19,
		name:    "TLS readiness",
		sql: `
CREATE TABLE IF NOT EXISTS tls_readiness (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	fqdn TEXT NOT NULL,
	strategy TEXT NOT NULL,
	challenge TEXT NOT NULL DEFAULT '',
	dns_provider TEXT NOT NULL DEFAULT '',
	needs_ingress INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S227 exposure approvals: the recorded ownership decision
		// per route host (plan JSON plus actor). Firewall
		// composition expands every approval, so approving never
		// drops earlier routes.
		version: 20,
		name:    "exposure approvals",
		sql: `
CREATE TABLE IF NOT EXISTS exposure_approvals (
	host TEXT PRIMARY KEY,
	plan TEXT NOT NULL,
	actor TEXT NOT NULL,
	networks TEXT NOT NULL DEFAULT '[]',
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S228 public validation: the last verdict per route host
		// (pass plus failed check names). The dashboard shows
		// PUBLIC / HEALTHY only from a passing record; later runs
		// replace.
		version: 21,
		name:    "public validation",
		sql: `
CREATE TABLE IF NOT EXISTS public_validation (
	host TEXT PRIMARY KEY,
	passed INTEGER NOT NULL DEFAULT 0,
	failed TEXT NOT NULL DEFAULT '[]',
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S246 tunnel routes: one served hostname per row (tunnel
		// plus local origin service). Re-setup replaces; teardown
		// deletes. Secrets never land here — only the sealed
		// store holds the API token.
		version: 22,
		name:    "tunnel routes",
		sql: `
CREATE TABLE IF NOT EXISTS tunnel_routes (
	hostname TEXT PRIMARY KEY,
	tunnel_id TEXT NOT NULL,
	tunnel_name TEXT NOT NULL DEFAULT '',
	service TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S256 session transport: an indexed token-hash lookup replaces
		// the per-request full-table scan in auth.Verify (the S257 boundary
		// runs Verify on every request), plus the per-session CSRF hash and
		// the idle-timeout clock. Legacy rows start their idle clock at
		// creation.
		version: 23,
		name:    "session transport",
		sql: `
ALTER TABLE sessions ADD COLUMN csrf_hash BLOB;
ALTER TABLE sessions ADD COLUMN last_used_at TEXT NOT NULL DEFAULT '';
UPDATE sessions SET last_used_at = created_at;
CREATE UNIQUE INDEX IF NOT EXISTS sessions_token_hash_unique
	ON sessions(token_hash) WHERE token_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS sessions_identity ON sessions(identity_id);`,
	},
	{
		// S260 daemon scheduler: last-run and last-skip state per job, so
		// missed windows run once at startup and never burst (ADR-023).
		version: 24,
		name:    "scheduler state",
		sql: `
CREATE TABLE IF NOT EXISTS scheduler_jobs (
	job_id TEXT PRIMARY KEY,
	last_run_at TEXT NOT NULL DEFAULT '',
	last_operation_id INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	last_skip_at TEXT NOT NULL DEFAULT '',
	last_skip_due TEXT NOT NULL DEFAULT '',
	last_skip_blocking INTEGER NOT NULL DEFAULT 0
);`,
	},
	{
		// S258 secret re-key (ADR-021): each row records the key version
		// that sealed it. 0 means unversioned (pre-S258) and is always
		// read as version 1; internal/secret stamps those rows at start.
		version: 25,
		name:    "secret key version",
		sql:     `ALTER TABLE secrets ADD COLUMN key_version INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		// S261 periodic drift scan: the single owner-set cadence row and
		// the latest scan result (both singletons, id = 1). Scan history
		// lives in operations and audit; these hold only current state.
		version: 26,
		name:    "drift scan",
		sql: `
CREATE TABLE IF NOT EXISTS drift_scan_schedule (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	interval_seconds INTEGER NOT NULL,
	enabled INTEGER NOT NULL,
	actor TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS drift_scan_latest (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	scanned_at TEXT NOT NULL,
	result TEXT NOT NULL
);`,
	},
	{
		// S263 certificate renewal check (ADR-024): the approved-issuance
		// fingerprint and public chain recorded with each certificate
		// issued through DNS01.Issue. Keys never land here.
		version: 27,
		name:    "certificate issuances",
		sql: `
CREATE TABLE IF NOT EXISTS cert_issuances (
	name TEXT PRIMARY KEY,
	fingerprint TEXT NOT NULL,
	chain_pem TEXT NOT NULL,
	issued_at TEXT NOT NULL
);`,
	},
	{
		// S264 release artifact verification (ADR-025): owner-managed
		// trusted keys (the embedded HAI key is tombstoned when removed),
		// installed counters for rollback protection, and the latest
		// verification record per platform component.
		version: 28,
		name:    "platform release verification",
		sql: `
CREATE TABLE IF NOT EXISTS platform_trust_keys (
	key_id TEXT PRIMARY KEY,
	public_key TEXT NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS platform_trust_removed (
	key_id TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS platform_installed (
	component TEXT PRIMARY KEY,
	version TEXT NOT NULL,
	counter INTEGER NOT NULL,
	recorded_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS platform_releases (
	component TEXT PRIMARY KEY,
	version TEXT NOT NULL DEFAULT '',
	counter INTEGER NOT NULL DEFAULT 0,
	state TEXT NOT NULL,
	refusal TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	key_id TEXT NOT NULL DEFAULT '',
	sha256 TEXT NOT NULL DEFAULT '',
	override INTEGER NOT NULL DEFAULT 0,
	actor TEXT NOT NULL DEFAULT '',
	verified_at TEXT NOT NULL
);`,
	},
	{
		// S277 app data migration: Docker volumes left behind after a
		// migration onto S276 bind mounts. Removal is a separate owner
		// action, so each volume is tracked until removed.
		version: 29,
		name:    "app data migration old volumes",
		sql: `
CREATE TABLE IF NOT EXISTS app_data_old_volumes (
	volume TEXT PRIMARY KEY,
	package TEXT NOT NULL,
	service TEXT NOT NULL,
	target TEXT NOT NULL,
	size_bytes INTEGER NOT NULL DEFAULT 0,
	state TEXT NOT NULL,
	actor TEXT NOT NULL DEFAULT '',
	migrated_at TEXT NOT NULL,
	removed_at TEXT NOT NULL DEFAULT ''
);`,
	},
	{
		// S271 backup recipients (ADR-022): owner-held age public keys
		// that every backup also encrypts to, and a record that the
		// recovery identity was exported (timestamp and actor only; the
		// exported value is never stored here).
		version: 30,
		name:    "backup recipients",
		sql: `
CREATE TABLE IF NOT EXISTS backup_recipients (
	fingerprint TEXT PRIMARY KEY,
	public_key TEXT NOT NULL UNIQUE,
	label TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS backup_identity_exports (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	fingerprint TEXT NOT NULL,
	actor TEXT NOT NULL,
	exported_at TEXT NOT NULL
);`,
	},
	{
		// S271 review: an export alone does not prove the owner holds the
		// key. The owner acknowledges storing it offline; only that
		// clears the doctor warning. Exports recorded before this
		// migration stay unacknowledged.
		version: 31,
		name:    "backup identity export acknowledgement",
		sql:     `ALTER TABLE backup_identity_exports ADD COLUMN acknowledged_at TEXT NOT NULL DEFAULT '';`,
	},
	{
		// S270 review: the edge reconciler records the table it last
		// wrote (one row). It owns exactly those hosts: only they are
		// removed as stale, and the dashboard and CLI read it back.
		version: 32,
		name:    "edge reconcile route table",
		sql: `
CREATE TABLE IF NOT EXISTS edge_routes (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	routes TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`,
	},
	{
		// S265: platform update apply. The single-row marker describes an
		// apply in flight (written before the binary is replaced, read
		// and cleared by the next daemon start); the previous-binary
		// record names what manual restore (and S273 rollback) restores.
		version: 33,
		name:    "platform update apply marker and previous binary",
		sql: `
CREATE TABLE IF NOT EXISTS platform_apply_marker (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	op_id INTEGER NOT NULL,
	component TEXT NOT NULL,
	from_version TEXT NOT NULL,
	from_counter INTEGER NOT NULL,
	to_version TEXT NOT NULL,
	to_counter INTEGER NOT NULL,
	sha256 TEXT NOT NULL,
	binary_path TEXT NOT NULL,
	previous_path TEXT NOT NULL,
	previous_sha256 TEXT NOT NULL,
	override INTEGER NOT NULL DEFAULT 0,
	override_reason TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS platform_previous (
	component TEXT PRIMARY KEY,
	version TEXT NOT NULL,
	counter INTEGER NOT NULL,
	sha256 TEXT NOT NULL,
	path TEXT NOT NULL,
	recorded_at TEXT NOT NULL
);`,
	},
	{
		// ADR-027: the daemon stages and hands off to a privileged apply
		// unit, so the marker names the staged files, the listeners the
		// unit health-checks, and whether the owner allowed an older
		// release.
		version: 34,
		name:    "platform apply marker staged files for the apply unit",
		sql: `
-- The marker is transient (pending applies only), so the table is
-- rebuilt rather than altered: idempotent for rewound upgrade tests.
DROP TABLE IF EXISTS platform_apply_marker;
CREATE TABLE platform_apply_marker (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	op_id INTEGER NOT NULL,
	component TEXT NOT NULL,
	from_version TEXT NOT NULL,
	from_counter INTEGER NOT NULL,
	to_version TEXT NOT NULL,
	to_counter INTEGER NOT NULL,
	sha256 TEXT NOT NULL,
	binary_path TEXT NOT NULL,
	previous_path TEXT NOT NULL,
	previous_sha256 TEXT NOT NULL,
	override INTEGER NOT NULL DEFAULT 0,
	override_reason TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL,
	created_at TEXT NOT NULL,
	staged_artifact TEXT NOT NULL DEFAULT '',
	staged_checksums TEXT NOT NULL DEFAULT '',
	staged_signature TEXT NOT NULL DEFAULT '',
	allow_older INTEGER NOT NULL DEFAULT 0,
	listen TEXT NOT NULL DEFAULT '',
	socket_path TEXT NOT NULL DEFAULT ''
);`,
	},
	{
		// S273: the same apply unit performs manual rollback, so the
		// marker says which kind of run it carries.
		version: 35,
		name:    "platform apply marker kind for rollback",
		sql: `
DROP TABLE IF EXISTS platform_apply_marker;
CREATE TABLE platform_apply_marker (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	op_id INTEGER NOT NULL,
	component TEXT NOT NULL,
	from_version TEXT NOT NULL,
	from_counter INTEGER NOT NULL,
	to_version TEXT NOT NULL,
	to_counter INTEGER NOT NULL,
	sha256 TEXT NOT NULL,
	binary_path TEXT NOT NULL,
	previous_path TEXT NOT NULL,
	previous_sha256 TEXT NOT NULL,
	override INTEGER NOT NULL DEFAULT 0,
	override_reason TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL,
	created_at TEXT NOT NULL,
	staged_artifact TEXT NOT NULL DEFAULT '',
	staged_checksums TEXT NOT NULL DEFAULT '',
	staged_signature TEXT NOT NULL DEFAULT '',
	allow_older INTEGER NOT NULL DEFAULT 0,
	listen TEXT NOT NULL DEFAULT '',
	socket_path TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT 'apply'
);`,
	},
	{
		// S278 first certificate issuance (ADR-006, ADR-010): the
		// owner's approval of one previewed issuance plan per
		// certificate. Plans hold public facts only; keys never land here.
		version: 36,
		name:    "certificate issuance approvals",
		sql: `
CREATE TABLE IF NOT EXISTS cert_issuance_approvals (
	certificate TEXT PRIMARY KEY,
	digest TEXT NOT NULL,
	plan TEXT NOT NULL,
	actor TEXT NOT NULL,
	agreed_terms INTEGER NOT NULL,
	confirmed_production INTEGER NOT NULL,
	approved_at TEXT NOT NULL
);`,
	},
	{
		// S272 unattended certificate re-issue (ADR-024): the persisted
		// retry state of each certificate's renewal, so the consecutive
		// failure counter and next-attempt time survive restarts.
		version: 37,
		name:    "certificate renewal state",
		sql: `
CREATE TABLE IF NOT EXISTS cert_renewal_state (
	certificate TEXT PRIMARY KEY,
	attempts INTEGER NOT NULL DEFAULT 0,
	next_attempt_at TEXT NOT NULL DEFAULT '',
	last_attempt_at TEXT NOT NULL DEFAULT '',
	last_success_at TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT '',
	pending_reload INTEGER NOT NULL DEFAULT 0,
	account_recovery INTEGER NOT NULL DEFAULT 0
);`,
	},
	{
		// S272 review: the approval records the owner's renewal mode
		// (existing approvals default to auto, ADR-024) and each recorded
		// issuance the CA directory it came from.
		version: 38,
		name:    "certificate renewal mode and issuance directory",
		sql: `
ALTER TABLE cert_issuance_approvals ADD COLUMN renewal_mode TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE cert_issuances ADD COLUMN directory TEXT NOT NULL DEFAULT '';`,
	},
	{
		// S279 (ADR-028): craft signing keys (a separate class from the
		// platform release keys), per-app counter baselines, the latest
		// verification verdict per app, and the locally built image
		// records the drift and install checks compare against.
		version: 39,
		name:    "craft build pipeline",
		sql: `
CREATE TABLE IF NOT EXISTS craft_trust_keys (
	key_id TEXT PRIMARY KEY,
	public_key TEXT NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS craft_trust_removed (
	key_id TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS craft_baseline (
	app TEXT PRIMARY KEY,
	version TEXT NOT NULL,
	counter INTEGER NOT NULL,
	recorded_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS craft_verdicts (
	app TEXT PRIMARY KEY,
	version TEXT NOT NULL DEFAULT '',
	counter INTEGER NOT NULL DEFAULT 0,
	state TEXT NOT NULL,
	refusal TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	key_id TEXT NOT NULL DEFAULT '',
	override INTEGER NOT NULL DEFAULT 0,
	actor TEXT NOT NULL DEFAULT '',
	verified_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS craft_images (
	app TEXT NOT NULL,
	version TEXT NOT NULL,
	tag TEXT NOT NULL,
	image_id TEXT NOT NULL,
	counter INTEGER NOT NULL,
	key_id TEXT NOT NULL,
	artifact_sha256 TEXT NOT NULL,
	context_digest TEXT NOT NULL,
	manifest_sha256 TEXT NOT NULL,
	built_at TEXT NOT NULL,
	PRIMARY KEY (app, version)
);`,
	},
	{
		// S279 review: the baseline remembers the digests of the build
		// that set it, so an equal counter and version with different
		// digests can be refused (ADR-025 consistency).
		version: 40,
		name:    "craft baseline digests",
		sql: `
ALTER TABLE craft_baseline ADD COLUMN artifact_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE craft_baseline ADD COLUMN context_digest TEXT NOT NULL DEFAULT '';`,
	},
	{
		// S280 (ADR-030): the persisted internal CA. Only PUBLIC material
		// lives here; the private key is a sys_ secret (ADR-021). One row:
		// the CA is minted once and never regenerated silently.
		version: 41,
		name:    "internal ca and local leaves",
		sql: `
CREATE TABLE IF NOT EXISTS internal_ca (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	cert_pem TEXT NOT NULL,
	fingerprint_sha256 TEXT NOT NULL,
	serial TEXT NOT NULL,
	subject TEXT NOT NULL,
	permitted_dns TEXT NOT NULL,
	not_before TEXT NOT NULL,
	not_after TEXT NOT NULL,
	generation INTEGER NOT NULL,
	previous_fingerprint TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS local_leaves (
	host TEXT PRIMARY KEY,
	cert_name TEXT NOT NULL,
	serial TEXT NOT NULL,
	fingerprint_sha256 TEXT NOT NULL,
	ca_fingerprint TEXT NOT NULL,
	not_before TEXT NOT NULL,
	not_after TEXT NOT NULL,
	issued_at TEXT NOT NULL
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
	// busy_timeout goes first: switching journal_mode needs a lock, and
	// without a timeout a writer still finishing (a previous process or
	// connection) fails the open with SQLITE_BUSY instead of waiting.
	for _, pragma := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
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
