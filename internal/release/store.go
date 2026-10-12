package release

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"homestead/internal/store"
)

// EmbeddedKeyLabel names the key compiled into the binary.
const EmbeddedKeyLabel = "HAI release key (PLACEHOLDER: production signing key pending, see docs/DEFERRED.md)"

// EmbeddedPublicKey is the HAI public key shipped in the binary
// (ADR-025). PLACEHOLDER: this is a generated key whose private half
// was discarded, so nothing can be signed for it. The production key
// and signing ceremony are deferred work (docs/DEFERRED.md); owners
// import their own or HAI's published key by configuration meanwhile.
const EmbeddedPublicKey = "untrusted comment: HAI Homestead release public key (PLACEHOLDER)\nRWTnumLxbuC16dpXvXmX4GBEZb6mQJ4iv5KGxMiUwTeAN27NzhuLIJQq\n"

// Source values for TrustKey.Source.
const (
	SourceEmbedded = "embedded"
	SourceOwner    = "owner"
)

// Verification states reported per release.
const (
	StateVerified   = "verified"
	StateUnverified = "unverified"
	StateFailed     = "failed"
)

// Refusal codes in Release.Refusal.
const (
	// RefusalRollback is the only refusal an owner override can lift.
	RefusalRollback = "rollback"
	RefusalCounter  = "counter-conflict"
	RefusalAuth     = "authenticity"
)

var (
	// ErrKeyExists marks a key id already trusted (HTTP 409).
	ErrKeyExists = errors.New("release: trusted key already exists")
	// ErrKeyNotFound marks an unknown trusted key id (HTTP 404).
	ErrKeyNotFound = errors.New("release: trusted key not found")
)

// TrustKey is one trusted public key as exposed over API and CLI.
type TrustKey struct {
	KeyID     string    `json:"key_id"`
	PublicKey string    `json:"public_key"`
	Label     string    `json:"label"`
	Source    string    `json:"source"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
	// Placeholder marks the embedded key whose private half was discarded:
	// nothing can be signed for it, so it verifies no real release.
	Placeholder bool `json:"placeholder"`
}

// Release is the latest verification record for one component.
type Release struct {
	Component  string    `json:"component"`
	Version    string    `json:"version"`
	Counter    int64     `json:"counter"`
	State      string    `json:"state"`
	Refusal    string    `json:"refusal"`
	Detail     string    `json:"detail"`
	KeyID      string    `json:"key_id"`
	SHA256     string    `json:"sha256"`
	Override   bool      `json:"override"`
	Actor      string    `json:"actor"`
	VerifiedAt time.Time `json:"verified_at"`
}

// Store keeps trust keys, installed counters, and verification
// records in the operational database (ADR-003); trust keys render to
// YAML for inspection.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over an open, migrated store.
func NewStore(s *store.Store) *Store { return &Store{db: s.DB()} }

func embeddedKey() TrustKey {
	k, err := ParsePublicKey(EmbeddedPublicKey)
	if err != nil {
		panic("release: embedded public key is invalid: " + err.Error())
	}
	return TrustKey{KeyID: k.KeyID, PublicKey: k.Encoded(), Label: EmbeddedKeyLabel, Source: SourceEmbedded, Actor: "system", Placeholder: true}
}

// Keys lists trusted keys: the embedded key unless the owner removed
// it, plus owner keys, sorted by id.
func (s *Store) Keys() ([]TrustKey, error) { return keysIn(s.db) }

// querier is what *sql.DB and *sql.Tx share, so key reads can run inside
// a transaction.
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func keysIn(q querier) ([]TrustKey, error) {
	var keys []TrustKey
	emb := embeddedKey()
	var removed int
	if err := q.QueryRow(`SELECT COUNT(*) FROM platform_trust_removed WHERE key_id = ?`, emb.KeyID).Scan(&removed); err != nil {
		return nil, fmt.Errorf("release: keys: %w", err)
	}
	if removed == 0 {
		keys = append(keys, emb)
	}
	rows, err := q.Query(`SELECT key_id, public_key, label, actor, created_at FROM platform_trust_keys`)
	if err != nil {
		return nil, fmt.Errorf("release: keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		k := TrustKey{Source: SourceOwner}
		var created string
		if err := rows.Scan(&k.KeyID, &k.PublicKey, &k.Label, &k.Actor, &created); err != nil {
			return nil, fmt.Errorf("release: keys: %w", err)
		}
		k.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("release: keys: %w", err)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].KeyID < keys[j].KeyID })
	return keys, nil
}

// PublicKeys returns the parsed trusted keys for verification.
func (s *Store) PublicKeys() ([]PublicKey, error) {
	keys, err := s.Keys()
	if err != nil {
		return nil, err
	}
	out := make([]PublicKey, 0, len(keys))
	for _, k := range keys {
		p, err := ParsePublicKey(k.PublicKey)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) hasKey(id string) (bool, error) { return hasKeyIn(s.db, id) }

func hasKeyIn(q querier, id string) (bool, error) {
	keys, err := keysIn(q)
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if k.KeyID == id {
			return true, nil
		}
	}
	return false, nil
}

// AddKey trusts a minisign public key. Re-adding the embedded key
// after the owner removed it restores it.
func (s *Store) AddKey(text, label, actor string) (TrustKey, error) {
	return s.change("", text, label, actor)
}

// ReplaceKey atomically swaps trusted key oldID for a new one.
func (s *Store) ReplaceKey(oldID, text, label, actor string) (TrustKey, error) {
	if strings.TrimSpace(oldID) == "" {
		return TrustKey{}, ErrKeyNotFound
	}
	return s.change(oldID, text, label, actor)
}

func (s *Store) change(oldID, text, label, actor string) (TrustKey, error) {
	p, err := ParsePublicKey(text)
	if err != nil {
		return TrustKey{}, err
	}
	if strings.TrimSpace(actor) == "" {
		return TrustKey{}, fmt.Errorf("release: actor must not be empty")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TrustKey{}, fmt.Errorf("release: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Existence checks run inside the transaction, so two concurrent
	// changes cannot both pass them; a lost race on the primary key is
	// reported as ErrKeyExists below.
	if oldID != "" {
		if ok, err := hasKeyIn(tx, oldID); err != nil {
			return TrustKey{}, err
		} else if !ok {
			return TrustKey{}, ErrKeyNotFound
		}
	}
	if ok, err := hasKeyIn(tx, p.KeyID); err != nil {
		return TrustKey{}, err
	} else if ok && p.KeyID != oldID {
		return TrustKey{}, ErrKeyExists
	}
	if oldID != "" {
		if err := removeIn(tx, oldID); err != nil {
			return TrustKey{}, err
		}
	}
	now := time.Now().UTC()
	emb := embeddedKey()
	if p.KeyID == emb.KeyID && p.Encoded() == emb.PublicKey {
		// The embedded key is restored, not duplicated.
		if _, err := tx.Exec(`DELETE FROM platform_trust_removed WHERE key_id = ?`, emb.KeyID); err != nil {
			return TrustKey{}, fmt.Errorf("release: restore embedded key: %w", err)
		}
	} else {
		if _, err := tx.Exec(`INSERT INTO platform_trust_keys (key_id, public_key, label, actor, created_at) VALUES (?, ?, ?, ?, ?)`,
			p.KeyID, p.Encoded(), strings.TrimSpace(label), actor, now.Format(time.RFC3339Nano)); err != nil {
			if strings.Contains(err.Error(), "constraint") {
				return TrustKey{}, ErrKeyExists
			}
			return TrustKey{}, fmt.Errorf("release: add key: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return TrustKey{}, fmt.Errorf("release: commit: %w", err)
	}
	if p.KeyID == emb.KeyID && p.Encoded() == emb.PublicKey {
		return emb, nil
	}
	return TrustKey{KeyID: p.KeyID, PublicKey: p.Encoded(), Label: strings.TrimSpace(label), Source: SourceOwner, Actor: actor, CreatedAt: now}, nil
}

func removeIn(tx *sql.Tx, id string) error {
	res, err := tx.Exec(`DELETE FROM platform_trust_keys WHERE key_id = ?`, id)
	if err != nil {
		return fmt.Errorf("release: remove key: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if id == embeddedKey().KeyID {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO platform_trust_removed (key_id) VALUES (?)`, id); err != nil {
			return fmt.Errorf("release: remove embedded key: %w", err)
		}
		return nil
	}
	return ErrKeyNotFound
}

// RemoveKey stops trusting a key. The embedded key is removable; the
// owner may re-add it later.
func (s *Store) RemoveKey(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("release: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if ok, err := hasKeyIn(tx, id); err != nil {
		return err
	} else if !ok {
		return ErrKeyNotFound
	}
	if err := removeIn(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Installed returns the recorded installed release for a component
// (zero value when none is recorded).
func (s *Store) Installed(component string) (Installed, error) {
	var in Installed
	err := s.db.QueryRow(`SELECT version, counter FROM platform_installed WHERE component = ?`, component).Scan(&in.Version, &in.Counter)
	if errors.Is(err, sql.ErrNoRows) {
		return Installed{}, nil
	}
	if err != nil {
		return Installed{}, fmt.Errorf("release: installed: %w", err)
	}
	return in, nil
}

// RecordInstalled stores the installed version and counter. Update
// actuation (S265) calls it after a successful replacement.
func (s *Store) RecordInstalled(component, version string, counter int64) error {
	_, err := s.db.Exec(`INSERT INTO platform_installed (component, version, counter, recorded_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(component) DO UPDATE SET version = excluded.version, counter = excluded.counter, recorded_at = excluded.recorded_at`,
		component, version, counter, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("release: record installed: %w", err)
	}
	return nil
}

// SeedInstalled records the installed release only when none is recorded,
// reporting whether it did. It never overwrites: S265's RecordInstalled
// owns later changes. The daemon seeds the baseline when a release
// matching the running version verifies, so rollback protection works
// before S265 lands.
func (s *Store) SeedInstalled(component, version string, counter int64) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO platform_installed (component, version, counter, recorded_at) VALUES (?, ?, ?, ?)`,
		component, version, counter, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("release: seed installed: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SaveRelease upserts the latest verification record for a component.
func (s *Store) SaveRelease(r Release) error {
	at := r.VerifiedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO platform_releases (component, version, counter, state, refusal, detail, key_id, sha256, override, actor, verified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(component) DO UPDATE SET version = excluded.version, counter = excluded.counter, state = excluded.state,
			refusal = excluded.refusal, detail = excluded.detail, key_id = excluded.key_id, sha256 = excluded.sha256,
			override = excluded.override, actor = excluded.actor, verified_at = excluded.verified_at`,
		r.Component, r.Version, r.Counter, r.State, r.Refusal, r.Detail, r.KeyID, r.SHA256, r.Override, r.Actor, at.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("release: save release: %w", err)
	}
	return nil
}

// Releases lists the latest verification record per component.
func (s *Store) Releases() ([]Release, error) {
	rows, err := s.db.Query(`SELECT component, version, counter, state, refusal, detail, key_id, sha256, override, actor, verified_at
		FROM platform_releases ORDER BY component`)
	if err != nil {
		return nil, fmt.Errorf("release: releases: %w", err)
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		var r Release
		var at string
		if err := rows.Scan(&r.Component, &r.Version, &r.Counter, &r.State, &r.Refusal, &r.Detail, &r.KeyID, &r.SHA256, &r.Override, &r.Actor, &at); err != nil {
			return nil, fmt.Errorf("release: releases: %w", err)
		}
		r.VerifiedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

type trustYAML struct {
	Version int            `yaml:"version"`
	Keys    []trustKeyYAML `yaml:"trusted_keys"`
}

type trustKeyYAML struct {
	KeyID     string `yaml:"key_id"`
	PublicKey string `yaml:"public_key"`
	Label     string `yaml:"label,omitempty"`
	Source    string `yaml:"source"`
	// Placeholder is true for the embedded key until the production
	// signing key replaces it.
	Placeholder bool `yaml:"placeholder,omitempty"`
}

// RenderYAML writes the inspectable trust configuration (ADR-003)
// atomically. SQLite stays the source of truth; the file is generated.
func (s *Store) RenderYAML(path string) error {
	keys, err := s.Keys()
	if err != nil {
		return err
	}
	doc := trustYAML{Version: 1, Keys: []trustKeyYAML{}}
	for _, k := range keys {
		doc.Keys = append(doc.Keys, trustKeyYAML{KeyID: k.KeyID, PublicKey: k.PublicKey, Label: k.Label, Source: k.Source, Placeholder: k.Placeholder})
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("release: render trust yaml: %w", err)
	}
	header := []byte("# Generated by homestead-server from the operational database (ADR-025).\n# Manage with `homesteadctl updates trust`; edits here are overwritten.\n")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".platform-trust-*")
	if err != nil {
		return fmt.Errorf("release: render trust yaml: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(header, data...)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("release: render trust yaml: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("release: render trust yaml: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("release: render trust yaml: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("release: render trust yaml: %w", err)
	}
	return nil
}
