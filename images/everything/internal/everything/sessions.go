package everything

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Server-side session and login-state storage lives in the bridge's own
// SQLite file. Only SHA-256 hashes of the session id and login state are
// stored, so reading the file does not yield usable cookies.

func (s *Store) initSessions() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS login_states (
        state_hash TEXT PRIMARY KEY, nonce TEXT NOT NULL, verifier TEXT NOT NULL, expires INTEGER NOT NULL
    ) WITHOUT ROWID;
    CREATE TABLE IF NOT EXISTS sessions (
        id_hash TEXT PRIMARY KEY, app TEXT NOT NULL, subject TEXT NOT NULL, csrf TEXT NOT NULL,
        created INTEGER NOT NULL, last_seen INTEGER NOT NULL
    ) WITHOUT ROWID`)
	return err
}

func hashID(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("everything: no randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

type loginState struct{ nonce, verifier string }

func (s *Store) putLoginState(ctx context.Context, state string, ls loginState, expires time.Time, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM login_states WHERE expires < ?`, now.Unix()); err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_states`).Scan(&n); err != nil {
		return err
	}
	if n >= maxStates {
		return errors.New("everything: too many pending sign-ins")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO login_states (state_hash, nonce, verifier, expires) VALUES (?,?,?,?)`,
		hashID(state), ls.nonce, ls.verifier, expires.Unix())
	return err
}

// takeLoginState consumes the state: it is single use.
func (s *Store) takeLoginState(ctx context.Context, state string, now time.Time) (loginState, bool) {
	var ls loginState
	var exp int64
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ls, false
	}
	defer tx.Rollback()
	h := hashID(state)
	if err = tx.QueryRowContext(ctx, `SELECT nonce, verifier, expires FROM login_states WHERE state_hash = ?`, h).Scan(&ls.nonce, &ls.verifier, &exp); err != nil {
		return ls, false
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM login_states WHERE state_hash = ?`, h); err != nil || tx.Commit() != nil {
		return loginState{}, false
	}
	return ls, now.Unix() <= exp
}

type sessionInfo struct{ app, subject, csrf string }

func (s *Store) createSession(ctx context.Context, app, subject string, now time.Time) (id string, info sessionInfo, err error) {
	id = randomToken(32)
	info = sessionInfo{app: app, subject: subject, csrf: randomToken(32)}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions (id_hash, app, subject, csrf, created, last_seen) VALUES (?,?,?,?,?,?)`,
		hashID(id), app, subject, info.csrf, now.Unix(), now.Unix())
	return
}

// lookupSession validates idle and absolute expiry, deleting expired
// sessions, and refreshes last_seen.
func (s *Store) lookupSession(ctx context.Context, id, app string, now time.Time, idle, abs time.Duration) (sessionInfo, bool) {
	var info sessionInfo
	var created, seen int64
	h := hashID(id)
	err := s.db.QueryRowContext(ctx, `SELECT app, subject, csrf, created, last_seen FROM sessions WHERE id_hash = ?`, h).
		Scan(&info.app, &info.subject, &info.csrf, &created, &seen)
	if err != nil {
		return sessionInfo{}, false
	}
	if info.app != app || now.Unix() >= created+int64(abs/time.Second) || now.Unix() >= seen+int64(idle/time.Second) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, h)
		return sessionInfo{}, false
	}
	if now.Unix() > seen {
		_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_seen = ? WHERE id_hash = ?`, now.Unix(), h)
	}
	return info, true
}

func (s *Store) deleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hashID(id))
	return err
}
