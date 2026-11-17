package everything

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"errors"
	"strings"
	"time"
)

// People registry (ADR-058 section 1-2). A person becomes known to an app
// only through a verified sign-in. There is no directory: the registry is
// consulted for a person's own name and, in later slices, to resolve a
// username a sharer typed. Names are never listed to other people.

func (s *Store) initPeople() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS bridge_meta (
        name TEXT PRIMARY KEY, value BLOB NOT NULL
    ) WITHOUT ROWID;
    CREATE TABLE IF NOT EXISTS people (
        app TEXT NOT NULL, subject TEXT NOT NULL,
        username TEXT, username_key TEXT, uuid TEXT NOT NULL,
        first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, purged_at INTEGER,
        PRIMARY KEY (app, subject)
    ) WITHOUT ROWID;
    CREATE UNIQUE INDEX IF NOT EXISTS people_live_name ON people (app, username_key)
        WHERE purged_at IS NULL AND username_key IS NOT NULL`)
	return err
}

// idKey returns the per-database HMAC key behind person uuids, creating it
// on first use. It never leaves the database file (exports omit it).
func (s *Store) idKey(ctx context.Context) ([]byte, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.hmacKey != nil {
		return s.hmacKey, nil
	}
	var k []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM bridge_meta WHERE name = 'id_hmac_key'`).Scan(&k)
	if errors.Is(err, sql.ErrNoRows) {
		k = make([]byte, 32)
		if _, err = rand.Read(k); err != nil {
			return nil, err
		}
		if _, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO bridge_meta (name, value) VALUES ('id_hmac_key', ?)`, k); err != nil {
			return nil, err
		}
		err = s.db.QueryRowContext(ctx, `SELECT value FROM bridge_meta WHERE name = 'id_hmac_key'`).Scan(&k)
	}
	if err != nil {
		return nil, err
	}
	s.hmacKey = k
	return k, nil
}

// personUUID is "p_" plus base32 of an HMAC of the app and subject: stable
// for the person in this app, unlinkable across apps, and not the sub.
func (s *Store) personUUID(ctx context.Context, app, subject string) (string, error) {
	k, err := s.idKey(ctx)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, k)
	m.Write([]byte(app + "\x00" + subject))
	return "p_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(m.Sum(nil)[:16])), nil
}

// Person is what a signed-in page may learn about itself.
type Person struct {
	Username string // "" when the sign-in carried no preferred_username
	UUID     string
}

// normalizeUsername returns the display name and its comparison key, or
// ok=false when the claim cannot serve as a recipient name.
func normalizeUsername(raw string) (name, key string, ok bool) {
	name = strings.TrimSpace(raw)
	if !validText(name, 255) || strings.ContainsAny(name, "\x00\r\n\t") {
		return "", "", false
	}
	return name, strings.ToLower(name), true
}

// RecordSignIn upserts the person for (app, subject) from verified claims.
// A purged person who signs in again is live again: Authentik only lets them
// through while the owner allows access. If another live person holds the
// same name (a renamed or reused account) that older row loses the name, so
// a name always resolves to the most recent verified holder.
func (s *Store) RecordSignIn(ctx context.Context, app, subject, username string, now time.Time) error {
	if !appName.MatchString(app) || !validText(subject, 255) {
		return ErrInvalid
	}
	uuid, err := s.personUUID(ctx, app, subject)
	if err != nil {
		return err
	}
	name, key, ok := normalizeUsername(username)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n, nk any
	if ok {
		n, nk = name, key
		if _, err = tx.ExecContext(ctx, `UPDATE people SET username = NULL, username_key = NULL
            WHERE app = ? AND username_key = ? AND subject <> ? AND purged_at IS NULL`, app, key, subject); err != nil {
			return err
		}
	}
	// Clear the old name first so un-purging can never collide with a new holder.
	if _, err = tx.ExecContext(ctx, `INSERT INTO people (app, subject, username, username_key, uuid, first_seen, last_seen)
        VALUES (?,?,NULL,NULL,?,?,?)
        ON CONFLICT(app, subject) DO UPDATE SET last_seen = excluded.last_seen, purged_at = NULL,
        username = NULL, username_key = NULL`,
		app, subject, uuid, now.Unix(), now.Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE people SET username = ?, username_key = ? WHERE app = ? AND subject = ?`, n, nk, app, subject); err != nil {
		return err
	}
	return tx.Commit()
}

// Person returns the caller's own record. A subject with no row (the
// development identity, or a bridge upgraded while a session was open) still
// gets its stable uuid and an empty username.
func (s *Store) Person(ctx context.Context, scope Scope) (Person, error) {
	var name sql.NullString
	var uuid string
	err := s.db.QueryRowContext(ctx, `SELECT username, uuid FROM people WHERE app = ? AND subject = ? AND purged_at IS NULL`,
		scope.App, scope.Subject).Scan(&name, &uuid)
	if errors.Is(err, sql.ErrNoRows) {
		uuid, err = s.personUUID(ctx, scope.App, scope.Subject)
		return Person{UUID: uuid}, err
	}
	return Person{Username: name.String, UUID: uuid}, err
}

// PurgePerson marks a person purged in one app and ends their sessions.
// Their grants (as owner and as recipient) are revoked. Their stored data
// stays and remains exportable. Their read links are revoked too. Exactly one of subject and username is used
// (subject wins). It is idempotent and returns how many live people it
// purged; an unknown or already purged person is zero, not an error.
func (s *Store) PurgePerson(ctx context.Context, app, subject, username string, now time.Time) (int, error) {
	if !appName.MatchString(app) {
		return 0, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var rows *sql.Rows
	switch {
	case subject != "":
		rows, err = tx.QueryContext(ctx, `SELECT subject FROM people WHERE app = ? AND subject = ? AND purged_at IS NULL`, app, subject)
	case username != "":
		_, key, ok := normalizeUsername(strings.TrimPrefix(username, "@"))
		if !ok {
			return 0, ErrInvalid
		}
		rows, err = tx.QueryContext(ctx, `SELECT subject FROM people WHERE app = ? AND username_key = ? AND purged_at IS NULL`, app, key)
	default:
		return 0, ErrInvalid
	}
	if err != nil {
		return 0, err
	}
	var subs []string
	for rows.Next() {
		var sub string
		if err = rows.Scan(&sub); err != nil {
			rows.Close()
			return 0, err
		}
		subs = append(subs, sub)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	for _, sub := range subs {
		if _, err = tx.ExecContext(ctx, `UPDATE people SET purged_at = ? WHERE app = ? AND subject = ?`, now.Unix(), app, sub); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE app = ? AND subject = ?`, app, sub); err != nil {
			return 0, err
		}
		// Their read links end too (the files stay).
		if _, err = tx.ExecContext(ctx, `UPDATE read_links SET revoked_at = ? WHERE app = ? AND owner_subject = ? AND revoked_at IS NULL`,
			now.Unix(), app, sub); err != nil {
			return 0, err
		}
		// Grants as owner and as recipient end; the data they named stays.
		if _, err = tx.ExecContext(ctx, `UPDATE grants SET revoked_at = ?, updated_at = ?
            WHERE app = ? AND revoked_at IS NULL AND (owner_subject = ? OR recipient_subject = ?)`,
			now.Unix(), now.Unix(), app, sub, sub); err != nil {
			return 0, err
		}
	}
	return len(subs), tx.Commit()
}
