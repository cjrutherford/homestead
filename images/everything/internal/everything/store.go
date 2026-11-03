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
	"net"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
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
	// ErrExists means a rename would replace an existing file (S375).
	ErrExists = errors.New("everything: file already exists")
	appName   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
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
	keyMu                 sync.Mutex
	hmacKey               []byte
	sharing               bool
	linkMax               time.Duration
	trusted               []*net.IPNet
	trustedSet            bool
	publicRead            string
	netOn                 bool
	netFence              *NetFence
}

// SetSharing turns KV sharing on or off (ADR-058: an app whose definition has
// no `sharing` answers kv_handles_disabled). Call it before serving.
func (s *Store) SetSharing(on bool) { s.sharing = on }

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
	if err = ensureModifiedColumn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("everything: upgrade store: %w", err)
	}
	st := &Store{db: db}
	st.SetQuota(0, 0)
	if err = st.initSessions(); err != nil {
		db.Close()
		return nil, fmt.Errorf("everything: initialize sessions: %w", err)
	}
	if err = st.initPeople(); err != nil {
		db.Close()
		return nil, fmt.Errorf("everything: initialize people: %w", err)
	}
	if err = st.initGrants(); err != nil {
		db.Close()
		return nil, fmt.Errorf("everything: initialize grants: %w", err)
	}
	if err = st.initReadLinks(); err != nil {
		db.Close()
		return nil, fmt.Errorf("everything: initialize read links: %w", err)
	}
	if err = st.initNetLog(); err != nil {
		db.Close()
		return nil, fmt.Errorf("everything: initialize net log: %w", err)
	}
	return st, nil
}

// ensureModifiedColumn adds the per-object modification time (Unix
// milliseconds) that file stat and directory listings report. Rows written
// before the column existed read as 0. The change is additive and older
// bridge builds ignore the column.
func ensureModifiedColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(objects)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err = rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "modified" {
			has = true
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE objects ADD COLUMN modified INTEGER NOT NULL DEFAULT 0`)
	return err
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
	if kind == KindFile && (key == SharedRoot || strings.HasPrefix(key, SharedRoot+"/")) {
		return ErrInvalid // reserved for paths through a share (ADR-058)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM objects WHERE app=? AND subject=? AND kind=? AND object_key=?`, scope.App, scope.Subject, kind, key); err != nil {
		return err
	}
	if kind == KindFile {
		if err = revokeFileGrants(ctx, tx, scope, key, false); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// revokeFileGrants permanently revokes the owner's fs grants on a deleted or
// renamed file (key) and, with tree, on everything at or under a deleted
// folder, so re-creating a file at that path never revives a share (ADR-058).
func revokeFileGrants(ctx context.Context, tx *sql.Tx, scope Scope, key string, tree bool) error {
	now := time.Now().Unix()
	q := `UPDATE grants SET revoked_at = ?, updated_at = ? WHERE app = ? AND owner_subject = ? AND kind = 'fs' AND revoked_at IS NULL AND (object_key = ?`
	args := []any{now, now, scope.App, scope.Subject, key}
	if tree {
		q += ` OR substr(object_key,1,length(?)) = ?`
		args = append(args, key+"/", key+"/")
	}
	if _, err := tx.ExecContext(ctx, q+`)`, args...); err != nil {
		return err
	}
	return revokeFileLinks(ctx, tx, scope, key, tree)
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

// ListPageBytes bounds the values returned by one ListValues page, so that a
// large address book is read in several bounded responses (S372).
const ListPageBytes = 4 * 1024 * 1024

// ListValuesMax bounds the number of entries in one ListValues page.
const ListValuesMax = 500

// ListValues returns the key-value pairs whose key starts with prefix and sorts
// after the cursor (empty for the first page), in lexical order, within the
// authenticated scope. A page holds at most ListValuesMax entries and about
// ListPageBytes of values (always at least one entry). next is the cursor for
// the following page, or empty when there is none. It reads only; it changes
// no quota. KV only.
func (s *Store) ListValues(ctx context.Context, scope Scope, kind Kind, prefix, after string) (entries []Entry, next string, err error) {
	if kind != KindKV {
		return nil, "", ErrInvalid
	}
	if prefix == "" {
		err = validate(scope, kind, "_")
	} else {
		err = validate(scope, kind, prefix)
	}
	if err == nil && after != "" {
		err = validate(scope, kind, after)
	}
	if err != nil {
		return nil, "", err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT object_key, value FROM objects WHERE app=? AND subject=? AND kind=? AND substr(object_key,1,length(?))=? AND object_key>? ORDER BY object_key LIMIT ?`,
		scope.App, scope.Subject, kind, prefix, prefix, after, ListValuesMax+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Key, &e.Value); err != nil {
			return nil, "", err
		}
		if len(entries) >= ListValuesMax || (len(entries) > 0 && size+len(e.Value) > ListPageBytes) {
			next = entries[len(entries)-1].Key
			break
		}
		size += len(e.Value)
		entries = append(entries, e)
	}
	return entries, next, rows.Err()
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
	now := time.Now().UnixMilli()
	for _, entry := range entries {
		value := entry.Value
		if value == nil {
			value = []byte{}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO objects(app,subject,kind,object_key,value,modified) VALUES(?,?,?,?,?,?) ON CONFLICT(app,subject,kind,object_key) DO UPDATE SET value=excluded.value, modified=MAX(excluded.modified, objects.modified+1)`, scope.App, scope.Subject, kind, entry.Key, value, now); err != nil {
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

// FileInfo describes a stored file or a directory implied by file keys.
// Modified is Unix milliseconds and strictly increases with each rewrite of
// the same file, so a changed file never keeps its stamp.
type FileInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	IsDir    bool   `json:"is_dir"`
}

// MaxDirEntries bounds how many stored files one directory listing considers.
const MaxDirEntries = 20000

// Stat reports a file (exact key) or, when no file has that key, a directory
// that other file keys sit under. Directories are not stored: they exist
// while a file does beneath them, as in a flat object store.
func (s *Store) Stat(ctx context.Context, scope Scope, key string) (FileInfo, error) {
	if err := validate(scope, KindFile, key); err != nil {
		return FileInfo{}, err
	}
	name := path.Base(key)
	var size, modified int64
	err := s.db.QueryRowContext(ctx, `SELECT length(value), modified FROM objects WHERE app=? AND subject=? AND kind=? AND object_key=?`, scope.App, scope.Subject, KindFile, key).Scan(&size, &modified)
	if err == nil {
		return FileInfo{Name: name, Size: size, Modified: modified}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return FileInfo{}, err
	}
	var n int64
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(modified),0) FROM objects WHERE app=? AND subject=? AND kind=? AND substr(object_key,1,length(?))=?`, scope.App, scope.Subject, KindFile, key+"/", key+"/").Scan(&n, &modified)
	if err != nil {
		return FileInfo{}, err
	}
	if n == 0 {
		return FileInfo{}, ErrNotFound
	}
	return FileInfo{Name: name, Modified: modified, IsDir: true}, nil
}

// Readdir lists the direct children of a directory in name order. A
// directory with no files beneath it does not exist (ErrNotFound).
func (s *Store) Readdir(ctx context.Context, scope Scope, dir string) ([]FileInfo, error) {
	if err := validate(scope, KindFile, dir); err != nil {
		return nil, err
	}
	prefix := dir + "/"
	rows, err := s.db.QueryContext(ctx, `SELECT object_key, length(value), modified FROM objects WHERE app=? AND subject=? AND kind=? AND substr(object_key,1,length(?))=? ORDER BY object_key LIMIT ?`, scope.App, scope.Subject, KindFile, prefix, prefix, MaxDirEntries+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]*FileInfo{}
	var order []string
	seen := 0
	for rows.Next() {
		var key string
		var size, modified int64
		if err := rows.Scan(&key, &size, &modified); err != nil {
			return nil, err
		}
		if seen++; seen > MaxDirEntries {
			return nil, errors.New("everything: directory holds too many files to list")
		}
		rest := key[len(prefix):]
		name, _, nested := strings.Cut(rest, "/")
		fi := byName[name]
		if fi == nil {
			fi = &FileInfo{Name: name}
			byName[name] = fi
			order = append(order, name)
		}
		if nested {
			fi.IsDir = true
		} else {
			fi.Size = size
		}
		if modified > fi.Modified {
			fi.Modified = modified
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, ErrNotFound
	}
	out := make([]FileInfo, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

// DeleteTree removes the file with this key and every file under it, within
// the authenticated scope. Missing is not an error. It returns how many files
// were removed.
func (s *Store) DeleteTree(ctx context.Context, scope Scope, key string) (int64, error) {
	if err := validate(scope, KindFile, key); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM objects WHERE app=? AND subject=? AND kind=? AND (object_key=? OR substr(object_key,1,length(?))=?)`, scope.App, scope.Subject, KindFile, key, key+"/", key+"/")
	if err != nil {
		return 0, err
	}
	if err = revokeFileGrants(ctx, tx, scope, key, true); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Rename gives the file at from the name to within the same folder (S375).
// Both are file keys in the authenticated scope. It fails with ErrNotFound if
// from is not a file and ErrExists if to is already taken, so a rename never
// replaces data. The value moves unchanged; the longer or shorter key is
// counted against the quota like any other write.
func (s *Store) Rename(ctx context.Context, scope Scope, from, to string) error {
	if err := validate(scope, KindFile, from); err != nil {
		return err
	}
	if err := validate(scope, KindFile, to); err != nil {
		return err
	}
	if from == to || path.Dir(from) != path.Dir(to) {
		return ErrInvalid
	}
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
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM objects WHERE app=? AND subject=? AND kind=? AND object_key=?`, scope.App, scope.Subject, KindFile, to).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrExists
	}
	p0, a0, err := usage(ctx, tx, scope)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE objects SET object_key=?, modified=MAX(?, modified+1) WHERE app=? AND subject=? AND kind=? AND object_key=?`, to, time.Now().UnixMilli(), scope.App, scope.Subject, KindFile, from)
	if err != nil {
		return err
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	// A renamed file leaves its shares behind: revoked, not moved.
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, `UPDATE grants SET revoked_at = ?, updated_at = ? WHERE app=? AND owner_subject=? AND kind='fs' AND object_key=? AND revoked_at IS NULL`, now, now, scope.App, scope.Subject, from); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE read_links SET revoked_at = ? WHERE app=? AND owner_subject=? AND object_key=? AND revoked_at IS NULL`, now, scope.App, scope.Subject, from); err != nil {
		return err
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
