// Package everything contains foundations for the experimental Everything
// bridge. It does not authenticate callers or expose a network API. A future
// handler must derive Scope from a verified identity and app authorization;
// accepting it from a request would expose other people's data.
package everything

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

var (
	ErrInvalid  = errors.New("everything: invalid data request")
	ErrNotFound = errors.New("everything: data not found")
	appName     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

// MaxValueBytes bounds a single stored KV value or file; this is not a total quota.
const MaxValueBytes = 8 * 1024 * 1024

type Kind int

const (
	KindKV Kind = iota + 1
	KindFile
)

// Scope must only come from a verified identity and authorization, never a client.
type Scope struct {
	App     string
	Subject string
}
type Store struct{ db *sql.DB }

// OpenStore opens a SQLite data file in an owner-selected application storage
// directory. The database is ordinary SQLite and is independent of the daemon.
// The containing directory must be private to the bridge service.
func OpenStore(filename string) (*Store, error) {
	info, err := os.Lstat(filename)
	switch {
	case errors.Is(err, os.ErrNotExist):
		f, e := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, fmt.Errorf("everything: create store: %w", e)
		}
		if e = f.Close(); e != nil {
			return nil, e
		}
	case err != nil:
		return nil, fmt.Errorf("everything: inspect store: %w", err)
	case !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0:
		return nil, errors.New("everything: store must be a private regular file")
	}
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("everything: configure store: %w", err)
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS objects (
        app TEXT NOT NULL, subject TEXT NOT NULL, kind INTEGER NOT NULL,
        object_key TEXT NOT NULL, value BLOB NOT NULL,
        PRIMARY KEY (app, subject, kind, object_key)
    ) WITHOUT ROWID`); err != nil {
		db.Close()
		return nil, fmt.Errorf("everything: initialize store: %w", err)
	}
	st := &Store{db: db}
	if err = st.initSessions(); err != nil {
		db.Close()
		return nil, fmt.Errorf("everything: initialize sessions: %w", err)
	}
	return st, nil
}

func (s *Store) Close() error { return s.db.Close() }

func validText(v string, limit int) bool {
	if v == "" || len(v) > limit || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validate(scope Scope, kind Kind, key string) error {
	if !appName.MatchString(scope.App) || !validText(scope.Subject, 512) || !validText(key, 1024) || (kind != KindKV && kind != KindFile) {
		return ErrInvalid
	}
	if kind == KindFile && (strings.Contains(key, `\`) || strings.HasPrefix(key, "/") || key == ".." || strings.HasPrefix(key, "../") || path.Clean(key) != key || key == ".") {
		return ErrInvalid
	}
	return nil
}

func (s *Store) Put(ctx context.Context, scope Scope, kind Kind, key string, value []byte) error {
	if err := validate(scope, kind, key); err != nil {
		return err
	}
	if len(value) > MaxValueBytes {
		return ErrInvalid
	}
	if value == nil {
		value = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO objects(app,subject,kind,object_key,value) VALUES(?,?,?,?,?)
        ON CONFLICT(app,subject,kind,object_key) DO UPDATE SET value=excluded.value`, scope.App, scope.Subject, kind, key, value)
	return err
}
func (s *Store) Get(ctx context.Context, scope Scope, kind Kind, key string) ([]byte, error) {
	if err := validate(scope, kind, key); err != nil {
		return nil, err
	}
	var value []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM objects WHERE app=? AND subject=? AND kind=? AND object_key=?`, scope.App, scope.Subject, kind, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return value, err
}
func (s *Store) Delete(ctx context.Context, scope Scope, kind Kind, key string) error {
	if err := validate(scope, kind, key); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM objects WHERE app=? AND subject=? AND kind=? AND object_key=?`, scope.App, scope.Subject, kind, key)
	return err
}

// List returns matching names in lexical order within the authenticated scope.
// Prefix matching is literal; no SQL wildcard semantics are exposed.
func (s *Store) List(ctx context.Context, scope Scope, kind Kind, prefix string) ([]string, error) {
	if prefix == "" {
		if err := validate(scope, kind, "_"); err != nil {
			return nil, err
		}
	} else if err := validate(scope, kind, prefix); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT object_key FROM objects WHERE app=? AND subject=? AND kind=? AND substr(object_key,1,length(?))=? ORDER BY object_key LIMIT 1001`, scope.App, scope.Subject, kind, prefix, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
		if len(keys) > 1000 {
			return nil, errors.New("everything: list exceeds 1000 entries")
		}
	}
	return keys, rows.Err()
}

// Entry is one item in an atomic batch. Limits protect the bridge service
// independently of request-size limits at its future HTTP boundary.
type Entry struct {
	Key   string
	Value []byte
}

func (s *Store) PutBatch(ctx context.Context, scope Scope, kind Kind, entries []Entry) error {
	if len(entries) == 0 || len(entries) > 100 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(entries))
	total := 0
	for _, entry := range entries {
		if err := validate(scope, kind, entry.Key); err != nil {
			return err
		}
		if seen[entry.Key] || len(entry.Value) > MaxValueBytes {
			return ErrInvalid
		}
		seen[entry.Key] = true
		total += len(entry.Value)
		if total > 16*1024*1024 {
			return ErrInvalid
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, entry := range entries {
		value := entry.Value
		if value == nil {
			value = []byte{}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO objects(app,subject,kind,object_key,value) VALUES(?,?,?,?,?) ON CONFLICT(app,subject,kind,object_key) DO UPDATE SET value=excluded.value`, scope.App, scope.Subject, kind, entry.Key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
