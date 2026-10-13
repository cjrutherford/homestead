package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"homestead/internal/layout"
	"homestead/internal/store"
)

// ErrNotFound reports reads of names holding no secret.
var ErrNotFound = errors.New("secret: not found")

// KeyFileName is the legacy (pre-S258) key file inside LibDir. Since
// S258 keys live in versioned files KeyFileName.v<N> beside it (ADR-021);
// the legacy file is only read once, to migrate it to version 1, and is
// left in place and ignored afterward (binary rollback works only before the first re-key; ADR-021).
// Key material never leaves these root-only files; the database holds
// ciphertext alone.
const KeyFileName = "secret.key"

// DefaultKeyPath returns the production legacy key path; versioned key
// files are derived from it. The parent directory is provisioned by S012;
// loading does not create parents, so a missing LibDir fails loudly
// instead of scattering key material.
func DefaultKeyPath() string {
	return filepath.Join(layout.LibDir.Path, KeyFileName)
}

// AppSecretPrefix marks app-owned secrets (S266): container variables
// the catalog generates. Configuration export leaves them out; they are
// recovered from encrypted backups (S259), not from the export document.
const AppSecretPrefix = "app_"

// IsAppSecret reports whether name is an app-owned secret.
func IsAppSecret(name string) bool {
	return len(name) > len(AppSecretPrefix) && name[:len(AppSecretPrefix)] == AppSecretPrefix
}

// SystemSecretPrefix marks appliance-held system secrets (S259): the
// backup identity. Neither the owner routes (Set) nor the catalog
// (SetApp, app_ only) can write these names, so no package or owner
// secret can collide with or overwrite them. Configuration export
// leaves them out.
const SystemSecretPrefix = "sys_"

// BackupIdentityName holds the appliance backup identity (ADR-022).
const BackupIdentityName = SystemSecretPrefix + "backup_identity"

// legacyBackupIdentityName is where the first S259 build stored the
// identity, inside the catalog's namespace; it migrates on startup.
const legacyBackupIdentityName = AppSecretPrefix + "backup_identity"

// IsSystemSecret reports whether name is a system secret.
func IsSystemSecret(name string) bool {
	return len(name) > len(SystemSecretPrefix) && name[:len(SystemSecretPrefix)] == SystemSecretPrefix
}

// IsReserved reports names configuration export and import never carry.
func IsReserved(name string) bool { return IsAppSecret(name) || IsSystemSecret(name) }

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// maxValueBytes caps stored values: tokens are short, and a bound keeps
// one fat row from bloating the operational database.
const maxValueBytes = 8192

// Store keeps DNS/API secret values encrypted at rest: AES-GCM rows in
// the operational database (migration v7), keys in root-only versioned
// files. Each row records the key version that sealed it and is opened
// with that key, so mixed-version states stay readable (ADR-021). Values
// enter through Set and leave only through Get for provider calls — List
// reports names and timestamps, never values, and errors never carry
// value or key material.
type Store struct {
	db      *sql.DB
	keyPath string

	// mu guards keys/current and serializes row writes against the
	// re-encrypt transaction.
	mu      sync.RWMutex
	keys    map[int][32]byte
	current int
	// rekeying is set while a re-key operation owns the store.
	rekeying bool
	// hook is a test seam: called at named re-key steps; an error aborts
	// the step as if the process had failed there.
	hook func(point string) error
}

// New opens a Store over an open store. keyPath is the legacy key path
// (see KeyFileName); versioned keys are keyPath.v<N>. On first start
// after S258 an existing legacy key becomes version 1 and unversioned rows
// are stamped 1 without changing any ciphertext; a fresh install
// generates version 1 directly. Any key file readable beyond its owner is
// refused: the daemon repairs permissions, never operates exposed. When
// every row is already on the newest key version and verifies, older key
// files are removed (completing an interrupted re-key).
func New(s *store.Store, keyPath string) (*Store, error) {
	st := &Store{db: s.DB(), keyPath: keyPath}
	if err := st.loadKeys(); err != nil {
		return nil, err
	}
	if _, err := st.db.Exec(`UPDATE secrets SET key_version = 1 WHERE key_version = 0`); err != nil {
		return nil, fmt.Errorf("secret: stamp unversioned rows: %w", err)
	}
	if err := st.cleanupOlder(); err != nil {
		return nil, err
	}
	if err := st.migrateLegacyBackupIdentity(); err != nil {
		return nil, fmt.Errorf("secret: migrate backup identity: %w", err)
	}
	return st, nil
}

// seal encrypts one value under a fresh random nonce with the key of the
// given version. Callers hold mu.
func (s *Store) seal(version int, value string) (nonce, ciphertext []byte, err error) {
	key, ok := s.keys[version]
	if !ok {
		return nil, nil, fmt.Errorf("secret: key version %d unavailable", version)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("secret: nonce: %w", err)
	}
	return nonce, gcm.Seal(nil, nonce, []byte(value), nil), nil
}

func newGCM(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("secret: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: gcm: %w", err)
	}
	return gcm, nil
}

// open decrypts one row with the key version it records, failing closed
// on tampering or key mismatch. Callers hold mu.
func (s *Store) open(version int, nonce, ciphertext []byte) (string, error) {
	key, ok := s.keys[version]
	if !ok {
		return "", fmt.Errorf("secret: key version %d unavailable", version)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("secret: decrypt failed")
	}
	return string(plain), nil
}

// validateName checks the secret-name shape, shared with DNS credential
// field names so provider requirements map one-to-one.
func validateName(name string) error {
	if len(name) > 64 || !validName.MatchString(name) {
		return fmt.Errorf("secret: name must match [a-z][a-z0-9_]* (max 64)")
	}
	return nil
}

// ErrReservedName reports owner-facing writes to the app-owned prefix.
var ErrReservedName = errors.New("secret: the app_ prefix is reserved for catalog-managed secrets")

// Set stores (or replaces) one owner-supplied secret value. The app_
// prefix is reserved: owner routes (credentials, tunnel tokens, config
// import) cannot overwrite catalog-managed secrets (S266); the catalog
// uses SetApp.
func (s *Store) Set(name, value string) error {
	if IsReserved(name) {
		return ErrReservedName
	}
	return s.set(name, value)
}

// SetSystem stores an appliance-held system secret; only sys_ names.
func (s *Store) SetSystem(name, value string) error {
	if !IsSystemSecret(name) {
		return fmt.Errorf("secret: system secrets must start with %s", SystemSecretPrefix)
	}
	return s.set(name, value)
}

// migrateLegacyBackupIdentity moves an identity stored by the first S259
// build under app_backup_identity to sys_backup_identity: copy first,
// delete only after the copy reads back identical, idempotent. Only a
// value shaped like an age identity moves, so a catalog secret that
// legitimately has this name is left alone.
func (s *Store) migrateLegacyBackupIdentity() error {
	legacy, err := s.Get(legacyBackupIdentityName)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.HasPrefix(legacy, "AGE-SECRET-KEY-") {
		return nil
	}
	current, err := s.Get(BackupIdentityName)
	if errors.Is(err, ErrNotFound) {
		if err := s.set(BackupIdentityName, legacy); err != nil {
			return err
		}
		current = legacy
	} else if err != nil {
		return err
	}
	if current != legacy {
		// A newer identity already exists; never delete the old one: it
		// may be the only key to backups written before this migration.
		return nil
	}
	return s.Delete(legacyBackupIdentityName)
}

// SetApp stores a catalog-managed secret; only app_ names are accepted.
func (s *Store) SetApp(name, value string) error {
	if !IsAppSecret(name) {
		return fmt.Errorf("secret: app secrets must start with %s", AppSecretPrefix)
	}
	return s.set(name, value)
}

func (s *Store) set(name, value string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if len(value) == 0 || len(value) > maxValueBytes {
		return fmt.Errorf("secret: value must be 1-%d bytes", maxValueBytes)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	version := s.current
	nonce, ciphertext, err := s.seal(version, value)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO secrets (name, nonce, ciphertext, updated_at, key_version)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET nonce = excluded.nonce,
		 ciphertext = excluded.ciphertext, updated_at = excluded.updated_at,
		 key_version = excluded.key_version`,
		name, nonce, ciphertext, now, version,
	); err != nil {
		return fmt.Errorf("secret: set %q: %w", name, err)
	}
	return nil
}

// Get returns one secret value for provider calls.
func (s *Store) Get(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var nonce, ciphertext []byte
	var version int
	err := s.db.QueryRow(
		`SELECT nonce, ciphertext, `+versionExpr+` FROM secrets WHERE name = ?`, name,
	).Scan(&nonce, &ciphertext, &version)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("secret: get %q: %w", name, err)
	}
	return s.open(version, nonce, ciphertext)
}

// Delete removes one secret, idempotent: absent names converge to nil so
// rotation and reconcile flows never trip on ordering.
func (s *Store) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM secrets WHERE name = ?`, name); err != nil {
		return fmt.Errorf("secret: delete %q: %w", name, err)
	}
	return nil
}

// Info describes one stored secret without its value.
type Info struct {
	Name      string
	UpdatedAt time.Time
}

// List returns every stored secret's metadata, never values.
func (s *Store) List() ([]Info, error) {
	rows, err := s.db.Query(`SELECT name, updated_at FROM secrets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("secret: list: %w", err)
	}
	defer rows.Close()
	var out []Info
	for rows.Next() {
		var info Info
		var updated string
		if err := rows.Scan(&info.Name, &updated); err != nil {
			return nil, fmt.Errorf("secret: list: %w", err)
		}
		info.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, fmt.Errorf("secret: list: %w", err)
		}
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("secret: list: %w", err)
	}
	return out, nil
}
