package everything

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Read links (ADR-058 section 4). A link names one file, not a copy: it serves
// the file's current content until it expires, is revoked, or the file is
// deleted, renamed or tree-deleted (permanently: re-creating the path never
// revives it). The token is 32 random bytes shown once; only its SHA-256 is
// stored.

// Read link limits.
const (
	DefaultReadLinkTTL = 7 * 24 * time.Hour
	MaxReadLinkTTL     = 365 * 24 * time.Hour // the owner may lower it per app
	MinReadLinkTTL     = time.Minute
	// MaxLinksPerPath and MaxLinksPerOwner count live links.
	MaxLinksPerPath  = 10
	MaxLinksPerOwner = 500
	readTokenBytes   = 32
)

// ErrLinkLimit means the owner holds the maximum live links.
var ErrLinkLimit = errors.New("everything: read link limit reached")

func (s *Store) initReadLinks() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS read_links (
        id TEXT PRIMARY KEY, app TEXT NOT NULL,
        owner_subject TEXT NOT NULL, object_key TEXT NOT NULL,
        token_hash TEXT NOT NULL UNIQUE,
        created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, revoked_at INTEGER,
        last_used_at INTEGER, hits INTEGER NOT NULL DEFAULT 0
    ) WITHOUT ROWID;
    CREATE INDEX IF NOT EXISTS read_links_owner ON read_links (app, owner_subject, object_key) WHERE revoked_at IS NULL`)
	return err
}

// SetReadLinkMax lowers the longest lifetime a link may be given (zero or
// less keeps the 365 day default; larger than the default is ignored).
func (s *Store) SetReadLinkMax(d time.Duration) {
	s.linkMax = MaxReadLinkTTL
	if d > 0 && d < MaxReadLinkTTL {
		s.linkMax = d
	}
}

func (s *Store) maxLinkTTL() time.Duration {
	if s.linkMax <= 0 {
		return MaxReadLinkTTL
	}
	return s.linkMax
}

// ReadLink is one link as the owner sees it. The token is never stored.
type ReadLink struct {
	ID         string
	App        string
	Owner      string // owner's subject (never sent to a page)
	OwnerName  string
	Key        string
	CreatedAt  int64
	ExpiresAt  int64
	RevokedAt  int64 // 0 while not revoked
	LastUsedAt int64
	Hits       int64
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateReadLink mints a link to the owner's file. ttl is clamped to the
// per-app maximum; below the one minute minimum it is ErrInvalid. A missing
// file is ErrNotFound and a folder ErrInvalid. The returned token is the only
// time it exists in the clear.
func (s *Store) CreateReadLink(ctx context.Context, owner Scope, key string, ttl time.Duration, now time.Time) (string, ReadLink, error) {
	if err := validate(owner, KindFile, key); err != nil {
		return "", ReadLink{}, err
	}
	if ttl == 0 {
		ttl = DefaultReadLinkTTL
	}
	if ttl < MinReadLinkTTL {
		return "", ReadLink{}, ErrInvalid
	}
	if m := s.maxLinkTTL(); ttl > m {
		ttl = m
	}
	info, err := s.Stat(ctx, owner, key)
	if err != nil {
		return "", ReadLink{}, err
	}
	if info.IsDir {
		return "", ReadLink{}, ErrInvalid
	}
	raw := make([]byte, readTokenBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", ReadLink{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	id, err := newGrantID()
	if err != nil {
		return "", ReadLink{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", ReadLink{}, err
	}
	defer tx.Rollback()
	ok, err := ownerUsable(ctx, tx, owner.App, owner.Subject)
	if err != nil {
		return "", ReadLink{}, err
	}
	if !ok {
		return "", ReadLink{}, ErrNotFound
	}
	var onPath, onOwner int
	live := `revoked_at IS NULL AND expires_at > ? AND app = ? AND owner_subject = ?`
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM read_links WHERE `+live+` AND object_key = ?`,
		now.Unix(), owner.App, owner.Subject, key).Scan(&onPath); err != nil {
		return "", ReadLink{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM read_links WHERE `+live,
		now.Unix(), owner.App, owner.Subject).Scan(&onOwner); err != nil {
		return "", ReadLink{}, err
	}
	if onPath >= MaxLinksPerPath || onOwner >= MaxLinksPerOwner {
		return "", ReadLink{}, ErrLinkLimit
	}
	l := ReadLink{ID: id, App: owner.App, Owner: owner.Subject, Key: key, CreatedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO read_links (id, app, owner_subject, object_key, token_hash, created_at, expires_at)
        VALUES (?,?,?,?,?,?,?)`, l.ID, l.App, l.Owner, l.Key, hashToken(token), l.CreatedAt, l.ExpiresAt); err != nil {
		return "", ReadLink{}, err
	}
	return token, l, tx.Commit()
}

// OpenReadLink resolves a token to the file's current content and counts the
// hit. Every reason a token does not work (unknown, expired, revoked, owner
// purged, file gone) is the same ErrNotFound.
func (s *Store) OpenReadLink(ctx context.Context, token string, now time.Time) (ReadLink, []byte, error) {
	var l ReadLink
	var revoked sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id, app, owner_subject, object_key, created_at, expires_at, revoked_at
        FROM read_links WHERE token_hash = ?`, hashToken(token)).Scan(&l.ID, &l.App, &l.Owner, &l.Key, &l.CreatedAt, &l.ExpiresAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return ReadLink{}, nil, ErrNotFound
	}
	if err != nil {
		return ReadLink{}, nil, err
	}
	if revoked.Valid || l.ExpiresAt <= now.Unix() {
		return ReadLink{}, nil, ErrNotFound
	}
	ok, err := ownerUsable(ctx, s.db, l.App, l.Owner)
	if err != nil {
		return ReadLink{}, nil, err
	}
	if !ok {
		return ReadLink{}, nil, ErrNotFound
	}
	value, err := s.Get(ctx, Scope{App: l.App, Subject: l.Owner}, KindFile, l.Key)
	if err != nil {
		return ReadLink{}, nil, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE read_links SET hits = hits + 1, last_used_at = ? WHERE id = ?`, now.Unix(), l.ID)
	return l, value, nil
}

// ReadLinks lists an app's links (never tokens). Without all, only live ones.
func (s *Store) ReadLinks(ctx context.Context, app string, all bool, now time.Time) ([]ReadLink, error) {
	q := `SELECT l.id, l.app, l.owner_subject, COALESCE(p.username, ''), l.object_key, l.created_at, l.expires_at,
        COALESCE(l.revoked_at, 0), COALESCE(l.last_used_at, 0), l.hits
        FROM read_links l LEFT JOIN people p ON p.app = l.app AND p.subject = l.owner_subject
        WHERE l.app = ?`
	args := []any{app}
	if !all {
		q += ` AND l.revoked_at IS NULL AND l.expires_at > ?`
		args = append(args, now.Unix())
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY l.created_at, l.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadLink
	for rows.Next() {
		var l ReadLink
		if err = rows.Scan(&l.ID, &l.App, &l.Owner, &l.OwnerName, &l.Key, &l.CreatedAt, &l.ExpiresAt, &l.RevokedAt, &l.LastUsedAt, &l.Hits); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RevokeReadLink revokes one link by id and reports how many it revoked.
func (s *Store) RevokeReadLink(ctx context.Context, app, id string, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE read_links SET revoked_at = ? WHERE app = ? AND id = ? AND revoked_at IS NULL`, now.Unix(), app, id)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// revokeFileLinks permanently revokes the owner's links to a deleted or
// renamed file (key) and, with tree, to every file under a deleted folder.
func revokeFileLinks(ctx context.Context, tx *sql.Tx, scope Scope, key string, tree bool) error {
	q := `UPDATE read_links SET revoked_at = ? WHERE app = ? AND owner_subject = ? AND revoked_at IS NULL AND (object_key = ?`
	args := []any{time.Now().Unix(), scope.App, scope.Subject, key}
	if tree {
		q += ` OR substr(object_key,1,length(?)) = ?`
		args = append(args, key+"/", key+"/")
	}
	_, err := tx.ExecContext(ctx, q+`)`, args...)
	return err
}
