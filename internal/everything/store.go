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
	// ErrQuota means a write would take a person or the whole application
	// past its byte quota. The handler answers 413 "quota_exceeded".
	ErrQuota = errors.New("everything: storage quota exceeded")
	appName  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
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

// Default quotas (bytes of keys plus values). A person may hold 256 MiB in
// one application and the application's total is capped at 4 GiB.
const (
	DefaultPersonQuota int64 = 256 << 20
	DefaultAppQuota    int64 = 4 << 30
)

type Store struct {
	db                    *sql.DB
	personQuota, appQuota int64
}

// SetQuota sets the per-person and per-application byte quotas; a value of
// zero or less keeps the default. Call it before serving.
func (s *Store) SetQuota(person, app int64) {
	s.personQuota, s.appQuota = DefaultPersonQuota, DefaultAppQuota
	if person > 0 {
		s.personQuota = person
	}
	if app > 0 {
		s.appQuota = app
	}
}

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
	st.SetQuota(0, 0)
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
	return s.write(ctx, scope, kind, []Entry{{Key: key, Value: value}})
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
	return s.write(ctx, scope, kind, entries)
}

// usage returns the bytes held by one person and by the whole application.
func usage(ctx context.Context, q *sql.Conn, scope Scope) (person, app int64, err error) {
	err = q.QueryRowContext(ctx, `SELECT
        COALESCE(SUM(CASE WHEN subject=? THEN length(object_key)+length(value) ELSE 0 END),0),
        COALESCE(SUM(length(object_key)+length(value)),0)
        FROM objects WHERE app=?`, scope.Subject, scope.App).Scan(&person, &app)
	return
}

// write applies entries in one transaction and enforces the quotas inside
// it. The store has a single connection and SQLite serialises writers, so
// concurrent writers cannot both pass the check. A write that does not grow
// usage (a delete-like shrink or equal replace) is allowed even when over.
func (s *Store) write(ctx context.Context, scope Scope, kind Kind, entries []Entry) error {
	// BEGIN IMMEDIATE takes the write lock before the usage read, so two
	// bridge processes (or this one) cannot both pass the quota check.
	tx, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer tx.Close()
	if _, err = tx.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = tx.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	p0, a0, err := usage(ctx, tx, scope)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		value := entry.Value
		if value == nil {
			value = []byte{}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO objects(app,subject,kind,object_key,value) VALUES(?,?,?,?,?) ON CONFLICT(app,subject,kind,object_key) DO UPDATE SET value=excluded.value`, scope.App, scope.Subject, kind, entry.Key, value); err != nil {
			return err
		}
	}
	p1, a1, err := usage(ctx, tx, scope)
	if err != nil {
		return err
	}
	if (p1 > p0 && p1 > s.personQuota) || (a1 > a0 && a1 > s.appQuota) {
		return ErrQuota
	}
	if _, err = tx.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	committed = true
	return nil
}
