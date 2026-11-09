package everything

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"path"
	"strings"
	"time"
)

// Share grants (ADR-058 section 3). A grant lets one person of an app use a
// file, folder or KV prefix of another. Rows are never copied: a recipient's
// operation is rewritten to the owner's (app, subject, kind, key), so the
// owner's quota pays for it and the owner's data stays the single copy.
//
// This file is the store only. No remote surface calls it yet; the SDK
// operations (S428 onward) must prove the caller is the recipient in a valid
// session and apply the 24 hour session recheck before using it.

var (
	// ErrNoRecipient is the one answer for a typed name that is unknown,
	// purged, or not a person of this app. It must not tell them apart.
	ErrNoRecipient = errors.New("everything: recipient does not exist")
	// ErrSelfShare means the owner named themselves.
	ErrSelfShare = errors.New("everything: cannot share with self")
	// ErrGrantLimit means the owner holds the maximum live grants in the app.
	ErrGrantLimit = errors.New("everything: share limit reached")
	// ErrReadOnly means a read grant was used to change data.
	ErrReadOnly = errors.New("everything: share is read only")
)

// MaxLiveGrants is the live grants one owner may hold per app.
const MaxLiveGrants = 200

// Grant kinds and access modes as stored.
const (
	GrantKV     = "kv"
	GrantFS     = "fs"
	AccessRead  = "read"
	AccessWrite = "write"
)

func (s *Store) initGrants() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS grants (
        id TEXT PRIMARY KEY, app TEXT NOT NULL,
        owner_subject TEXT NOT NULL, recipient_subject TEXT NOT NULL,
        kind TEXT NOT NULL, object_key TEXT NOT NULL, is_prefix INTEGER NOT NULL,
        access TEXT NOT NULL, label TEXT NOT NULL DEFAULT '',
        created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, revoked_at INTEGER
    ) WITHOUT ROWID;
    CREATE UNIQUE INDEX IF NOT EXISTS grants_live ON grants (app, owner_subject, recipient_subject, kind, object_key)
        WHERE revoked_at IS NULL;
    CREATE INDEX IF NOT EXISTS grants_recipient ON grants (app, recipient_subject) WHERE revoked_at IS NULL`)
	return err
}

// Grant is one share as the owner sees it. Names are looked up at read time.
type Grant struct {
	ID        string
	App       string
	Owner     string // owner's subject (never sent to a page)
	Recipient string // recipient's subject (never sent to a page)
	OwnerName string
	RecipName string
	Kind      string
	Key       string
	IsPrefix  bool
	Access    string
	Label     string
	CreatedAt int64
	UpdatedAt int64
	RevokedAt int64 // 0 while live
}

// GrantRequest is what an owner asks for. Username is what the owner typed.
type GrantRequest struct {
	Username string
	Kind     string
	Key      string
	IsPrefix bool
	Access   string
	Label    string
}

func newGrantID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func kindOf(g string) (Kind, bool) {
	switch g {
	case GrantKV:
		return KindKV, true
	case GrantFS:
		return KindFile, true
	}
	return 0, false
}

func validGrantKey(owner Scope, g GrantRequest) error {
	k, ok := kindOf(g.Kind)
	if !ok || (g.Access != AccessRead && g.Access != AccessWrite) {
		return ErrInvalid
	}
	if g.Label != "" && !validText(g.Label, 200) {
		return ErrInvalid
	}
	if g.Kind == GrantKV {
		// A KV share names a prefix, written without a trailing "*".
		if !g.IsPrefix || strings.HasSuffix(g.Key, "*") {
			return ErrInvalid
		}
	}
	return validate(owner, k, g.Key)
}

// resolveName returns the subject of the live person a typed name matches.
func resolveName(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, app, username string) (string, error) {
	_, key, ok := normalizeUsername(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if !ok {
		return "", ErrNoRecipient
	}
	var sub string
	err := q.QueryRowContext(ctx, `SELECT subject FROM people WHERE app = ? AND username_key = ? AND purged_at IS NULL`, app, key).Scan(&sub)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoRecipient
	}
	return sub, err
}

func ownerUsable(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, app, subject string) (bool, error) {
	var purged sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT purged_at FROM people WHERE app = ? AND subject = ?`, app, subject).Scan(&purged)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil // no row: the development identity
	}
	return !purged.Valid, err
}

// CreateGrant shares the owner's object with a typed username. Repeating the
// same share changes its access and label and keeps its id (isNew false).
func (s *Store) CreateGrant(ctx context.Context, owner Scope, g GrantRequest, now time.Time) (Grant, bool, error) {
	if err := validGrantKey(owner, g); err != nil {
		return Grant{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, false, err
	}
	defer tx.Rollback()
	if ok, err := ownerUsable(ctx, tx, owner.App, owner.Subject); err != nil || !ok {
		if err == nil {
			err = ErrNoRecipient
		}
		return Grant{}, false, err
	}
	rcpt, err := resolveName(ctx, tx, owner.App, g.Username)
	if err != nil {
		return Grant{}, false, err
	}
	if rcpt == owner.Subject {
		return Grant{}, false, ErrSelfShare
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM grants WHERE app = ? AND owner_subject = ? AND recipient_subject = ?
        AND kind = ? AND object_key = ? AND revoked_at IS NULL`, owner.App, owner.Subject, rcpt, g.Kind, g.Key).Scan(&id)
	isNew := errors.Is(err, sql.ErrNoRows)
	if err != nil && !isNew {
		return Grant{}, false, err
	}
	if isNew {
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM grants WHERE app = ? AND owner_subject = ? AND revoked_at IS NULL`,
			owner.App, owner.Subject).Scan(&n); err != nil {
			return Grant{}, false, err
		}
		if n >= MaxLiveGrants {
			return Grant{}, false, ErrGrantLimit
		}
		if id, err = newGrantID(); err != nil {
			return Grant{}, false, err
		}
		pre := 0
		if g.IsPrefix {
			pre = 1
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO grants (id,app,owner_subject,recipient_subject,kind,object_key,is_prefix,access,label,created_at,updated_at)
            VALUES (?,?,?,?,?,?,?,?,?,?,?)`, id, owner.App, owner.Subject, rcpt, g.Kind, g.Key, pre, g.Access, g.Label, now.Unix(), now.Unix())
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE grants SET access = ?, label = ?, updated_at = ? WHERE id = ?`, g.Access, g.Label, now.Unix(), id)
	}
	if err != nil {
		return Grant{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Grant{}, false, err
	}
	got, err := s.grantByID(ctx, id)
	return got, isNew, err
}

const grantCols = `g.id, g.app, g.owner_subject, g.recipient_subject, COALESCE(o.username,''), COALESCE(r.username,''),
    g.kind, g.object_key, g.is_prefix, g.access, g.label, g.created_at, g.updated_at, COALESCE(g.revoked_at,0)`
const grantFrom = ` FROM grants g
    JOIN people o ON o.app = g.app AND o.subject = g.owner_subject
    JOIN people r ON r.app = g.app AND r.subject = g.recipient_subject`

func scanGrants(rows *sql.Rows) ([]Grant, error) {
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		var pre int
		if err := rows.Scan(&g.ID, &g.App, &g.Owner, &g.Recipient, &g.OwnerName, &g.RecipName, &g.Kind, &g.Key, &pre,
			&g.Access, &g.Label, &g.CreatedAt, &g.UpdatedAt, &g.RevokedAt); err != nil {
			return nil, err
		}
		g.IsPrefix = pre == 1
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) grantByID(ctx context.Context, id string) (Grant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+grantCols+grantFrom+` WHERE g.id = ?`, id)
	if err != nil {
		return Grant{}, err
	}
	gs, err := scanGrants(rows)
	if err != nil {
		return Grant{}, err
	}
	if len(gs) == 0 {
		return Grant{}, ErrNotFound
	}
	return gs[0], nil
}

// RevokeGrant revokes one of the owner's own grants by id. Another person's
// id, an unknown id and an already revoked grant all answer ErrNotFound.
func (s *Store) RevokeGrant(ctx context.Context, owner Scope, id string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE grants SET revoked_at = ?, updated_at = ? WHERE id = ? AND app = ? AND owner_subject = ? AND revoked_at IS NULL`,
		now.Unix(), now.Unix(), id, owner.App, owner.Subject)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeShare revokes the owner's live grants of one object to a typed
// username (fs.unshare) and returns how many it revoked. A name that does
// not resolve is ErrNoRecipient; a resolvable name with no grant is zero.
func (s *Store) RevokeShare(ctx context.Context, owner Scope, username, kind, key string, now time.Time) (int, error) {
	if _, ok := kindOf(kind); !ok || !validText(key, 1024) {
		return 0, ErrInvalid
	}
	rcpt, err := resolveName(ctx, s.db, owner.App, username)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE grants SET revoked_at = ?, updated_at = ? WHERE app = ? AND owner_subject = ?
        AND recipient_subject = ? AND kind = ? AND object_key = ? AND revoked_at IS NULL`,
		now.Unix(), now.Unix(), owner.App, owner.Subject, rcpt, kind, key)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GrantsByOwner lists the owner's grants, revoked ones included (listSharedByMe).
func (s *Store) GrantsByOwner(ctx context.Context, owner Scope, kind string) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+grantCols+grantFrom+` WHERE g.app = ? AND g.owner_subject = ? AND g.kind = ?
        ORDER BY g.created_at, g.id`, owner.App, owner.Subject, kind)
	if err != nil {
		return nil, err
	}
	return scanGrants(rows)
}

// GrantsForRecipient lists the grants the person can use now: live, with a
// live owner, and for a live recipient (listShared).
func (s *Store) GrantsForRecipient(ctx context.Context, rcpt Scope, kind string) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+grantCols+grantFrom+` WHERE g.app = ? AND g.recipient_subject = ? AND g.kind = ?
        AND g.revoked_at IS NULL AND (o.purged_at IS NULL) AND (r.purged_at IS NULL)
        ORDER BY g.created_at, g.id`, rcpt.App, rcpt.Subject, kind)
	if err != nil {
		return nil, err
	}
	return scanGrants(rows)
}

// AllGrants lists an app's grants for the appliance owner's tool.
func (s *Store) AllGrants(ctx context.Context, app string, includeRevoked bool) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+grantCols+grantFrom+` WHERE (?='' OR g.app = ?) AND (? OR g.revoked_at IS NULL)
        ORDER BY g.app, g.created_at, g.id`, app, app, includeRevoked)
	if err != nil {
		return nil, err
	}
	return scanGrants(rows)
}

// RevokeGrantByID revokes any grant of an app (the appliance owner's tool).
func (s *Store) RevokeGrantByID(ctx context.Context, app, id string, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE grants SET revoked_at = ?, updated_at = ? WHERE app = ? AND id = ? AND revoked_at IS NULL`,
		now.Unix(), now.Unix(), app, id)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Resolved is a recipient's operation rewritten to the owner's scope.
type Resolved struct {
	Owner  Scope
	Kind   Kind
	Key    string // the owner's full key
	Prefix bool   // the grant covers a folder or KV prefix
	Access string
}

// ResolveGrant checks that rcpt may use grant id for kind with the relative
// key rel and returns the owner's scope and key. Every failure that could
// reveal another person's data (unknown id, someone else's grant, revoked,
// inert, wrong kind or app, key outside the grant) is ErrNotFound. When write
// is set and the grant is read-only the answer is ErrReadOnly, which only the
// proven recipient can reach.
//
// For a prefix grant rel is relative to the prefix: for fs it is a clean
// relative path ("" is the folder itself); for kv it is appended directly. A
// single-file grant takes only rel "".
func (s *Store) ResolveGrant(ctx context.Context, rcpt Scope, id, kind, rel string, write bool) (Resolved, error) {
	k, ok := kindOf(kind)
	if !ok || !validText(id, 64) {
		return Resolved{}, ErrNotFound
	}
	var owner, key, access string
	var pre int
	err := s.db.QueryRowContext(ctx, `SELECT g.owner_subject, g.object_key, g.is_prefix, g.access FROM grants g
        JOIN people o ON o.app = g.app AND o.subject = g.owner_subject
        JOIN people r ON r.app = g.app AND r.subject = g.recipient_subject
        WHERE g.id = ? AND g.app = ? AND g.recipient_subject = ? AND g.kind = ? AND g.revoked_at IS NULL
        AND o.purged_at IS NULL AND r.purged_at IS NULL`, id, rcpt.App, rcpt.Subject, kind).Scan(&owner, &key, &pre, &access)
	if errors.Is(err, sql.ErrNoRows) {
		return Resolved{}, ErrNotFound
	}
	if err != nil {
		return Resolved{}, err
	}
	full := key
	switch {
	case pre == 0:
		if rel != "" {
			return Resolved{}, ErrNotFound
		}
	case k == KindKV:
		full = key + rel
	case rel != "":
		if strings.Contains(rel, `\`) || strings.HasPrefix(rel, "/") || path.Clean(rel) != rel {
			return Resolved{}, ErrNotFound
		}
		full = key + "/" + rel
	}
	own := Scope{App: rcpt.App, Subject: owner}
	if validate(own, k, full) != nil {
		return Resolved{}, ErrNotFound
	}
	if write && access != AccessWrite {
		return Resolved{}, ErrReadOnly
	}
	return Resolved{Owner: own, Kind: k, Key: full, Prefix: pre == 1, Access: access}, nil
}

// GrantGet reads through a grant.
func (s *Store) GrantGet(ctx context.Context, rcpt Scope, id, kind, rel string) ([]byte, error) {
	r, err := s.ResolveGrant(ctx, rcpt, id, kind, rel, false)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, r.Owner, r.Kind, r.Key)
}

// GrantPut writes through a write grant. The bytes count against the
// owner's person and app quotas, since they live in the owner's scope.
func (s *Store) GrantPut(ctx context.Context, rcpt Scope, id, kind, rel string, value []byte) error {
	r, err := s.ResolveGrant(ctx, rcpt, id, kind, rel, true)
	if err != nil {
		return err
	}
	if r.Prefix && rel == "" {
		return ErrInvalid // a folder or prefix itself holds no value
	}
	return s.Put(ctx, r.Owner, r.Kind, r.Key, value)
}

// GrantDelete deletes through a write grant. A single-file grant never
// allows delete (ADR-058); only a folder or prefix grant does.
func (s *Store) GrantDelete(ctx context.Context, rcpt Scope, id, kind, rel string) error {
	r, err := s.ResolveGrant(ctx, rcpt, id, kind, rel, true)
	if err != nil {
		return err
	}
	if !r.Prefix || rel == "" {
		return ErrInvalid
	}
	return s.Delete(ctx, r.Owner, r.Kind, r.Key)
}
