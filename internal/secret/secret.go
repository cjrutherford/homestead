package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"homestead/internal/layout"
	"homestead/internal/store"
)

// ErrNotFound reports reads of names holding no secret.
var ErrNotFound = errors.New("secret: not found")

// KeyFileName is the encryption-key file inside LibDir. The key never
// leaves this root-only file; the database holds ciphertext alone.
const KeyFileName = "secret.key"

// DefaultKeyPath returns the production key path. The parent directory
// is provisioned by S012; loading does not create parents, so a missing
// LibDir fails loudly instead of scattering key material.
func DefaultKeyPath() string {
	return filepath.Join(layout.LibDir.Path, KeyFileName)
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// maxValueBytes caps stored values: tokens are short, and a bound keeps
// one fat row from bloating the operational database.
const maxValueBytes = 8192

// Store keeps DNS/API secret values encrypted at rest: AES-GCM rows in
// the operational database (migration v7), key in a root-only file.
// Values enter through Set and leave only through Get for provider
// calls — List reports names and timestamps, never values, and errors
// never carry value material.
type Store struct {
	db  *sql.DB
	key [32]byte
}

// New opens a Store over an open store, loading the key file at keyPath
// or generating it (0600) on first use. An existing key file readable
// beyond its owner is refused: the daemon repairs permissions, never
// operates exposed.
func New(s *store.Store, keyPath string) (*Store, error) {
	key, err := loadOrGenerateKey(keyPath)
	if err != nil {
		return nil, err
	}
	return &Store{db: s.DB(), key: key}, nil
}

// loadOrGenerateKey reads the 32-byte key file or creates it. Parent
// directories are never created here.
func loadOrGenerateKey(path string) ([32]byte, error) {
	var key [32]byte
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) != 32 {
			return key, fmt.Errorf("secret: key file %q must hold 32 bytes", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return key, fmt.Errorf("secret: stat key: %w", err)
		}
		if info.Mode().Perm()&0077 != 0 {
			return key, fmt.Errorf("secret: key file %q must not be group/other readable", path)
		}
		copy(key[:], raw)
		return key, nil
	}
	if !os.IsNotExist(err) {
		return key, fmt.Errorf("secret: read key: %w", err)
	}
	if _, err := rand.Read(key[:]); err != nil {
		return key, fmt.Errorf("secret: generate key: %w", err)
	}
	if err := os.WriteFile(path, key[:], 0o600); err != nil {
		return key, fmt.Errorf("secret: write key: %w", err)
	}
	return key, nil
}

// seal encrypts one value under a fresh random nonce.
func (s *Store) seal(value string) (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, nil, fmt.Errorf("secret: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("secret: gcm: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("secret: nonce: %w", err)
	}
	return nonce, gcm.Seal(nil, nonce, []byte(value), nil), nil
}

// open decrypts one row, failing closed on tampering or key mismatch.
func (s *Store) open(nonce, ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return "", fmt.Errorf("secret: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secret: gcm: %w", err)
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

// Set stores (or replaces) one secret value.
func (s *Store) Set(name, value string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if len(value) == 0 || len(value) > maxValueBytes {
		return fmt.Errorf("secret: value must be 1-%d bytes", maxValueBytes)
	}
	nonce, ciphertext, err := s.seal(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO secrets (name, nonce, ciphertext, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET nonce = excluded.nonce,
		 ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
		name, nonce, ciphertext, now,
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
	var nonce, ciphertext []byte
	err := s.db.QueryRow(
		`SELECT nonce, ciphertext FROM secrets WHERE name = ?`, name,
	).Scan(&nonce, &ciphertext)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("secret: get %q: %w", name, err)
	}
	return s.open(nonce, ciphertext)
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
