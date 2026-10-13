package backup

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"gopkg.in/yaml.v3"

	"homestead/internal/store"
)

// Owner recipients (S271, ADR-022). The owner adds age X25519 public
// keys whose private halves they hold; every new backup run encrypts to
// the appliance identity AND each of them. Existing runs are never
// re-encrypted: a recipient added later cannot read older runs, and a
// removed recipient keeps access to runs written while it was present.
//
// Only native age X25519 recipients (age1...) are accepted. SSH and
// plugin recipients are not (ADR-022 names X25519 only).

// MaxOwnerRecipients bounds the list so a run never fans out unbounded.
const MaxOwnerRecipients = 16

var (
	// ErrInvalidRecipient marks a malformed or unsupported recipient (422).
	ErrInvalidRecipient = errors.New("backup: invalid recipient")
	// ErrRecipientExists marks a recipient already configured (409).
	ErrRecipientExists = errors.New("backup: recipient already configured")
	// ErrRecipientNotFound marks an unknown fingerprint (404).
	ErrRecipientNotFound = errors.New("backup: recipient not found")
	// ErrApplianceRecipient marks an attempt to remove the appliance
	// identity, which always remains a recipient (409).
	ErrApplianceRecipient = errors.New("backup: the appliance identity is always a recipient and cannot be removed")
	// ErrExportRateLimited marks too many recovery exports in an hour (429).
	ErrExportRateLimited = errors.New("backup: too many recovery identity exports this hour; try again later")
	// ErrNothingToAcknowledge marks an acknowledgement with no pending
	// export (409).
	ErrNothingToAcknowledge = errors.New("backup: no unacknowledged recovery identity export")
)

// MaxExportsPerHour bounds recovery identity exports.
const MaxExportsPerHour = 5

// Recipient is one configured owner recipient. Public data only.
type Recipient struct {
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"public_key"`
	Label       string    `json:"label"`
	Kind        string    `json:"kind"` // "owner" or "appliance"
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"created_at"`
}

// IdentityExport records that the recovery identity was exported and,
// separately, that the owner confirmed storing it offline. It carries no
// key material. Only an acknowledged export satisfies the doctor check:
// an export alone does not prove the owner holds the key.
type IdentityExport struct {
	ID             int64     `json:"-"`
	Fingerprint    string    `json:"fingerprint"`
	Actor          string    `json:"actor"`
	ExportedAt     time.Time `json:"exported_at"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

// Acknowledged reports whether the owner confirmed storing the key.
func (e IdentityExport) Acknowledged() bool { return !e.AcknowledgedAt.IsZero() }

// Fingerprint names a recipient by the SHA-256 of its public key string
// (first 16 bytes, hex). It is not secret.
func Fingerprint(publicKey string) string {
	sum := sha256.Sum256([]byte(publicKey))
	return hex.EncodeToString(sum[:16])
}

// ParseRecipient validates a pasted recipient strictly: one native
// age X25519 public key, canonical lowercase, nothing else.
func ParseRecipient(raw string) (*age.X25519Recipient, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return nil, fmt.Errorf("%w: expected one age1... public key", ErrInvalidRecipient)
	}
	r, err := age.ParseX25519Recipient(raw)
	if err != nil || r.String() != raw {
		return nil, fmt.Errorf("%w: not a valid age X25519 public key (age1...); SSH and plugin recipients are not supported", ErrInvalidRecipient)
	}
	return r, nil
}

// RecipientStore persists owner recipients and export records.
type RecipientStore struct {
	db  *sql.DB
	now func() time.Time
}

// NewRecipientStore wraps the operational database.
func NewRecipientStore(st *store.Store) *RecipientStore {
	return &RecipientStore{db: st.DB(), now: time.Now}
}

// Owner lists owner recipients in creation order.
func (s *RecipientStore) Owner() ([]Recipient, error) {
	rows, err := s.db.Query(`SELECT fingerprint, public_key, label, actor, created_at FROM backup_recipients ORDER BY created_at, fingerprint`)
	if err != nil {
		return nil, fmt.Errorf("backup: list recipients: %w", err)
	}
	defer rows.Close()
	out := []Recipient{}
	for rows.Next() {
		var r Recipient
		var created string
		if err := rows.Scan(&r.Fingerprint, &r.PublicKey, &r.Label, &r.Actor, &created); err != nil {
			return nil, err
		}
		r.Kind = "owner"
		r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Add validates and stores one owner recipient. appliance is the
// appliance identity's public key, refused as a duplicate.
func (s *RecipientStore) Add(publicKey, label, actor, appliance string) (Recipient, error) {
	r, err := ParseRecipient(publicKey)
	if err != nil {
		return Recipient{}, err
	}
	key := r.String()
	label = strings.TrimSpace(label)
	if len(label) > 120 || strings.ContainsAny(label, "\r\n") {
		return Recipient{}, fmt.Errorf("%w: label must be a single line of at most 120 characters", ErrInvalidRecipient)
	}
	if strings.TrimSpace(actor) == "" {
		return Recipient{}, fmt.Errorf("%w: an acting owner is required", ErrInvalidRecipient)
	}
	if key == appliance {
		return Recipient{}, fmt.Errorf("%w: that is the appliance identity's own recipient; add a key you hold elsewhere", ErrInvalidRecipient)
	}
	rec := Recipient{Fingerprint: Fingerprint(key), PublicKey: key, Label: label, Kind: "owner", Actor: actor, CreatedAt: s.now().UTC()}
	// Count and insert share one transaction so concurrent adds cannot
	// jointly exceed the cap.
	tx, err := s.db.Begin()
	if err != nil {
		return Recipient{}, fmt.Errorf("backup: add recipient: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM backup_recipients`).Scan(&count); err != nil {
		return Recipient{}, fmt.Errorf("backup: add recipient: %w", err)
	}
	if count >= MaxOwnerRecipients {
		return Recipient{}, fmt.Errorf("%w: at most %d owner recipients", ErrInvalidRecipient, MaxOwnerRecipients)
	}
	_, err = tx.Exec(`INSERT INTO backup_recipients (fingerprint, public_key, label, actor, created_at) VALUES (?, ?, ?, ?, ?)`,
		rec.Fingerprint, rec.PublicKey, rec.Label, rec.Actor, rec.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Recipient{}, ErrRecipientExists
		}
		return Recipient{}, fmt.Errorf("backup: add recipient: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Recipient{}, fmt.Errorf("backup: add recipient: %w", err)
	}
	return rec, nil
}

// Remove deletes one owner recipient by fingerprint and returns it. The
// appliance identity's fingerprint is refused: the appliance is the
// last recipient that always remains, so a run is never left with none.
func (s *RecipientStore) Remove(fingerprint, appliance string) (Recipient, error) {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if appliance != "" && fingerprint == Fingerprint(appliance) {
		return Recipient{}, ErrApplianceRecipient
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Recipient{}, fmt.Errorf("backup: remove recipient: %w", err)
	}
	defer tx.Rollback()
	var r Recipient
	var created string
	err = tx.QueryRow(`SELECT fingerprint, public_key, label, actor, created_at FROM backup_recipients WHERE fingerprint = ?`, fingerprint).
		Scan(&r.Fingerprint, &r.PublicKey, &r.Label, &r.Actor, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Recipient{}, ErrRecipientNotFound
	}
	if err != nil {
		return Recipient{}, fmt.Errorf("backup: remove recipient: %w", err)
	}
	r.Kind = "owner"
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	res, err := tx.Exec(`DELETE FROM backup_recipients WHERE fingerprint = ?`, fingerprint)
	if err != nil {
		return Recipient{}, fmt.Errorf("backup: remove recipient: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Recipient{}, ErrRecipientNotFound
	}
	if err := tx.Commit(); err != nil {
		return Recipient{}, fmt.Errorf("backup: remove recipient: %w", err)
	}
	return r, nil
}

// RecordExport notes that the recovery identity was exported, refusing
// more than MaxExportsPerHour. Only the recipient fingerprint, actor, and
// time are kept. Call it before the key is returned.
func (s *RecipientStore) RecordExport(fingerprint, actor string) (IdentityExport, error) {
	at := s.now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return IdentityExport{}, fmt.Errorf("backup: record export: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT exported_at FROM backup_identity_exports ORDER BY id DESC LIMIT ?`, MaxExportsPerHour)
	if err != nil {
		return IdentityExport{}, fmt.Errorf("backup: record export: %w", err)
	}
	recent := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return IdentityExport{}, err
		}
		if t, perr := time.Parse(time.RFC3339Nano, raw); perr == nil && at.Sub(t) < time.Hour {
			recent++
		}
	}
	rows.Close()
	if recent >= MaxExportsPerHour {
		return IdentityExport{}, ErrExportRateLimited
	}
	res, err := tx.Exec(`INSERT INTO backup_identity_exports (fingerprint, actor, exported_at) VALUES (?, ?, ?)`,
		fingerprint, actor, at.Format(time.RFC3339Nano))
	if err != nil {
		return IdentityExport{}, fmt.Errorf("backup: record export: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return IdentityExport{}, fmt.Errorf("backup: record export: %w", err)
	}
	id, _ := res.LastInsertId()
	return IdentityExport{ID: id, Fingerprint: fingerprint, Actor: actor, ExportedAt: at}, nil
}

const exportColumns = `SELECT id, fingerprint, actor, exported_at, acknowledged_at FROM backup_identity_exports`

func scanExport(row *sql.Row) (IdentityExport, bool, error) {
	var e IdentityExport
	var at, ack string
	err := row.Scan(&e.ID, &e.Fingerprint, &e.Actor, &at, &ack)
	if errors.Is(err, sql.ErrNoRows) {
		return IdentityExport{}, false, nil
	}
	if err != nil {
		return IdentityExport{}, false, fmt.Errorf("backup: read export: %w", err)
	}
	e.ExportedAt, _ = time.Parse(time.RFC3339Nano, at)
	if ack != "" {
		e.AcknowledgedAt, _ = time.Parse(time.RFC3339Nano, ack)
	}
	return e, true, nil
}

// LastExport returns the latest export record, if any.
func (s *RecipientStore) LastExport() (IdentityExport, bool, error) {
	return scanExport(s.db.QueryRow(exportColumns + ` ORDER BY id DESC LIMIT 1`))
}

// LastAcknowledged returns the latest export the owner acknowledged.
func (s *RecipientStore) LastAcknowledged() (IdentityExport, bool, error) {
	return scanExport(s.db.QueryRow(exportColumns + ` WHERE acknowledged_at != '' ORDER BY id DESC LIMIT 1`))
}

// Acknowledge records that the owner stored the most recent export
// offline. It refuses when there is no export or it is already
// acknowledged.
func (s *RecipientStore) Acknowledge() (IdentityExport, error) {
	e, ok, err := s.LastExport()
	if err != nil {
		return IdentityExport{}, err
	}
	if !ok || e.Acknowledged() {
		return IdentityExport{}, ErrNothingToAcknowledge
	}
	at := s.now().UTC()
	res, err := s.db.Exec(`UPDATE backup_identity_exports SET acknowledged_at = ? WHERE id = ? AND acknowledged_at = ''`,
		at.Format(time.RFC3339Nano), e.ID)
	if err != nil {
		return IdentityExport{}, fmt.Errorf("backup: acknowledge export: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return IdentityExport{}, ErrNothingToAcknowledge
	}
	e.AcknowledgedAt = at
	return e, nil
}

// OwnerProtected reports whether the doctor owner-recipient check is
// satisfied: an owner recipient exists or an export was acknowledged.
func (s *RecipientStore) OwnerProtected() (bool, error) {
	owners, err := s.Owner()
	if err != nil {
		return false, err
	}
	if len(owners) > 0 {
		return true, nil
	}
	_, acked, err := s.LastAcknowledged()
	return acked, err
}

type recipientsYAML struct {
	Version    int             `yaml:"version"`
	Recipients []recipientYAML `yaml:"recipients"`
	Export     *exportYAML     `yaml:"identity_export,omitempty"`
}

type recipientYAML struct {
	Fingerprint string `yaml:"fingerprint"`
	PublicKey   string `yaml:"public_key"`
	Label       string `yaml:"label,omitempty"`
	Actor       string `yaml:"actor"`
}

type exportYAML struct {
	Actor          string `yaml:"actor"`
	ExportedAt     string `yaml:"exported_at"`
	AcknowledgedAt string `yaml:"acknowledged_at,omitempty"`
}

// RenderYAML writes the inspectable recipient configuration (ADR-003)
// atomically. Public keys and the export timestamp only.
func (s *RecipientStore) RenderYAML(path string) error {
	owners, err := s.Owner()
	if err != nil {
		return err
	}
	doc := recipientsYAML{Version: 1, Recipients: []recipientYAML{}}
	sort.SliceStable(owners, func(i, j int) bool { return owners[i].CreatedAt.Before(owners[j].CreatedAt) })
	for _, r := range owners {
		doc.Recipients = append(doc.Recipients, recipientYAML{Fingerprint: r.Fingerprint, PublicKey: r.PublicKey, Label: r.Label, Actor: r.Actor})
	}
	if e, ok, err := s.LastExport(); err != nil {
		return err
	} else if ok {
		doc.Export = &exportYAML{Actor: e.Actor, ExportedAt: e.ExportedAt.Format(time.RFC3339)}
		if e.Acknowledged() {
			doc.Export.AcknowledgedAt = e.AcknowledgedAt.Format(time.RFC3339)
		}
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("backup: render recipients yaml: %w", err)
	}
	header := []byte("# Generated by homestead-server from the operational database (ADR-022).\n# Manage with `homesteadctl backup recipients`; edits here are overwritten.\n# Public keys only: no private key is ever written here.\n")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".backup-recipients-*")
	if err != nil {
		return fmt.Errorf("backup: render recipients yaml: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(header, data...)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("backup: render recipients yaml: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RecipientsWithOwner returns the appliance recipient plus every owner
// recipient, deduplicated. It never returns an empty list.
func RecipientsWithOwner(keys KeyStore, owner *RecipientStore) ([]age.Recipient, error) {
	out, err := Recipients(keys)
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return out, nil
	}
	owners, err := owner.Owner()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{out[0].(*age.X25519Recipient).String(): true}
	for _, o := range owners {
		r, err := ParseRecipient(o.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("backup: stored recipient %s is unreadable", o.Fingerprint)
		}
		if !seen[r.String()] {
			seen[r.String()] = true
			out = append(out, r)
		}
	}
	return out, nil
}
