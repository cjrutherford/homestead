package everything

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Ping reports whether the data file answers; it reads no application data.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// ExportFormat identifies the export manifest layout.
const ExportFormat = "homestead-everything-export/2"

// exportFormatV1 is still read by Import; it has no people or grants.
const exportFormatV1 = "homestead-everything-export/1"

// ExportPerson is a people row without its uuid, which derives from a key
// that exports never carry. Import does not create people from these; they
// are for the owner to read and to match grants against.
type ExportPerson struct {
	App       string `json:"app"`
	Subject   string `json:"subject"`
	Username  string `json:"username,omitempty"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Purged    bool   `json:"purged,omitempty"`
}

// ExportGrant is one share. The id is kept so a restore is the same grant.
type ExportGrant struct {
	ID        string `json:"id"`
	App       string `json:"app"`
	Owner     string `json:"owner"`
	Recipient string `json:"recipient"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	IsPrefix  bool   `json:"is_prefix,omitempty"`
	Access    string `json:"access"`
	Label     string `json:"label,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	RevokedAt int64  `json:"revoked_at,omitempty"`
}

// ExportObject is one KV value or file in the manifest. KV values that are
// valid JSON appear as JSON; anything else is base64. File bodies are written
// under Files/ and referenced by Path.
type ExportObject struct {
	App         string          `json:"app"`
	Subject     string          `json:"subject"`
	Kind        string          `json:"kind"`
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value,omitempty"`
	ValueBase64 string          `json:"value_base64,omitempty"`
	Path        string          `json:"path,omitempty"`
	Bytes       int             `json:"bytes"`
}

// ExportManifest is manifest.json. It never contains sessions, login state,
// tokens, hashes, keys or logs: only the objects, people and grants tables
// are read.
type ExportManifest struct {
	Format  string         `json:"format"`
	Objects []ExportObject `json:"objects"`
	People  []ExportPerson `json:"people,omitempty"`
	Grants  []ExportGrant  `json:"grants,omitempty"`
}

func openReadOnly(dbPath string) (*sql.DB, error) {
	info, err := os.Stat(dbPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("everything: data file not found")
	}
	u := url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func fileDir(app, subject string) string {
	h := sha256.Sum256([]byte(subject))
	return filepath.Join("files", app, hex.EncodeToString(h[:8]))
}

// Export writes the stored objects of one person (subject) and/or one app
// ("" for all) into out, which must not exist or be empty. It opens the data
// file read-only, so it works on a stopped bridge's file or a restored copy.
// Layout: out/manifest.json plus out/files/<app>/<subject-hash>/<path>.
func Export(ctx context.Context, dbPath, subject, app, out string) (int, error) {
	if ents, err := os.ReadDir(out); err == nil && len(ents) > 0 {
		return 0, errors.New("everything: export directory is not empty")
	}
	db, err := openReadOnly(dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT app,subject,kind,object_key,value FROM objects
        WHERE (?='' OR subject=?) AND (?='' OR app=?) ORDER BY app,subject,kind,object_key`, subject, subject, app, app)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if err = os.MkdirAll(out, 0o700); err != nil {
		return 0, err
	}
	m := ExportManifest{Format: ExportFormat, Objects: []ExportObject{}}
	for rows.Next() {
		var a, sub, key string
		var kind Kind
		var val []byte
		if err = rows.Scan(&a, &sub, &kind, &key, &val); err != nil {
			return 0, err
		}
		o := ExportObject{App: a, Subject: sub, Key: key, Bytes: len(val)}
		switch kind {
		case KindKV:
			o.Kind = "kv"
			var compact bytes.Buffer
			// Only already-compact JSON is inlined, so import restores the
			// exact bytes; anything else is base64.
			if json.Compact(&compact, val) == nil && bytes.Equal(compact.Bytes(), val) {
				o.Value = json.RawMessage(val)
			} else {
				o.ValueBase64 = base64.StdEncoding.EncodeToString(val)
			}
		case KindFile:
			o.Kind = "file"
			rel := filepath.Join(fileDir(a, sub), filepath.FromSlash(key))
			if !fs.ValidPath(filepath.ToSlash(rel)) {
				return 0, errors.New("everything: refusing unsafe file path in data")
			}
			dst := filepath.Join(out, rel)
			if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return 0, err
			}
			if err = os.WriteFile(dst, val, 0o600); err != nil {
				return 0, err
			}
			o.Path = filepath.ToSlash(rel)
		default:
			continue
		}
		m.Objects = append(m.Objects, o)
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	if m.Grants, m.People, err = exportShares(ctx, db, subject, app); err != nil {
		return 0, err
	}
	// One entry per line keeps the file readable and the KV bytes exact.
	var buf bytes.Buffer
	buf.WriteString("{\"format\": \"" + ExportFormat + "\", \"objects\": [")
	if err = writeLines(&buf, m.Objects); err != nil {
		return 0, err
	}
	buf.WriteString("\n], \"people\": [")
	if err = writeLines(&buf, m.People); err != nil {
		return 0, err
	}
	buf.WriteString("\n], \"grants\": [")
	if err = writeLines(&buf, m.Grants); err != nil {
		return 0, err
	}
	buf.WriteString("\n]}\n")
	b := buf.Bytes()
	return len(m.Objects), os.WriteFile(filepath.Join(out, "manifest.json"), b, 0o600)
}

func writeLines[T any](buf *bytes.Buffer, items []T) error {
	for i, o := range items {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString("\n  ")
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(o); err != nil {
			return err
		}
		buf.Truncate(buf.Len() - 1)
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) bool {
	var n int
	return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n) == nil && n > 0
}

// exportShares reads the grants matching the filter and the people they
// involve plus the people the filter selects. A data file from before the
// tables existed has none.
func exportShares(ctx context.Context, db *sql.DB, subject, app string) ([]ExportGrant, []ExportPerson, error) {
	grants, people := []ExportGrant{}, []ExportPerson{}
	if tableExists(ctx, db, "grants") {
		rows, err := db.QueryContext(ctx, `SELECT id,app,owner_subject,recipient_subject,kind,object_key,is_prefix,access,label,
            created_at,updated_at,COALESCE(revoked_at,0) FROM grants
            WHERE (?='' OR app=?) AND (?='' OR owner_subject=? OR recipient_subject=?) ORDER BY app,created_at,id`,
			app, app, subject, subject, subject)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var g ExportGrant
			var pre int
			if err = rows.Scan(&g.ID, &g.App, &g.Owner, &g.Recipient, &g.Kind, &g.Key, &pre, &g.Access, &g.Label, &g.CreatedAt, &g.UpdatedAt, &g.RevokedAt); err != nil {
				rows.Close()
				return nil, nil, err
			}
			g.IsPrefix = pre == 1
			grants = append(grants, g)
		}
		if err = rows.Close(); err != nil {
			return nil, nil, err
		}
	}
	if tableExists(ctx, db, "people") {
		want := map[[2]string]bool{}
		for _, g := range grants {
			want[[2]string{g.App, g.Owner}] = true
			want[[2]string{g.App, g.Recipient}] = true
		}
		rows, err := db.QueryContext(ctx, `SELECT app,subject,COALESCE(username,''),first_seen,last_seen,purged_at IS NOT NULL FROM people
            WHERE (?='' OR app=?) ORDER BY app,subject`, app, app)
		if err != nil {
			return nil, nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var p ExportPerson
			if err = rows.Scan(&p.App, &p.Subject, &p.Username, &p.FirstSeen, &p.LastSeen, &p.Purged); err != nil {
				return nil, nil, err
			}
			if subject == "" || p.Subject == subject || want[[2]string{p.App, p.Subject}] {
				people = append(people, p)
			}
		}
		if err = rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	return grants, people, nil
}

// ImportResult counts what Import restored.
type ImportResult struct {
	Objects int
	Grants  int // restored grants (format /2 only)
	Skipped int // grants whose people are not in this store
}

// Import loads an export directory into the store, replacing objects with
// the same identity. See ImportAll for grants.
func Import(ctx context.Context, store *Store, dir string) (int, error) {
	r, err := ImportAll(ctx, store, dir)
	return r.Objects, err
}

// ImportAll is Import and also restores the grants of a /2 export. A grant
// comes back only when both its owner and its recipient already exist as
// people of that app in this store (people are made by sign-in, never by
// import) and are not purged; otherwise it is counted as skipped. An id that
// already exists is left alone. Objects replace those with the same
// identity. It ignores the quota (it restores what was held) but
// validates every key like a normal write. Sessions are not part of exports.
func ImportAll(ctx context.Context, store *Store, dir string) (ImportResult, error) {
	var res ImportResult
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return res, err
	}
	var m ExportManifest
	if err = json.Unmarshal(raw, &m); err != nil || (m.Format != ExportFormat && m.Format != exportFormatV1) {
		return res, errors.New("everything: not a Homestead Everything export")
	}
	pq, aq := store.personQuota, store.appQuota
	store.personQuota, store.appQuota = 1<<62, 1<<62
	defer func() { store.personQuota, store.appQuota = pq, aq }()
	for _, o := range m.Objects {
		sc := Scope{App: o.App, Subject: o.Subject}
		switch o.Kind {
		case "kv":
			val := []byte(o.Value)
			if o.ValueBase64 != "" {
				if val, err = base64.StdEncoding.DecodeString(o.ValueBase64); err != nil {
					return res, err
				}
			}
			err = store.Put(ctx, sc, KindKV, o.Key, val)
		case "file":
			p := filepath.Join(dir, filepath.FromSlash(o.Path))
			if !fs.ValidPath(o.Path) || !strings.HasPrefix(o.Path, "files/") {
				return res, fmt.Errorf("everything: unsafe path in manifest")
			}
			var val []byte
			if val, err = os.ReadFile(p); err != nil {
				return res, err
			}
			err = store.Put(ctx, sc, KindFile, o.Key, val)
		default:
			err = ErrInvalid
		}
		if err != nil {
			return res, err
		}
	}
	res.Objects = len(m.Objects)
	if m.Format == ExportFormat {
		if err = importGrants(ctx, store, m.Grants, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

func importGrants(ctx context.Context, store *Store, grants []ExportGrant, res *ImportResult) error {
	for _, g := range grants {
		if _, ok := kindOf(g.Kind); !ok || !validText(g.ID, 64) || validGrantKey(Scope{App: g.App, Subject: g.Owner},
			GrantRequest{Kind: g.Kind, Key: g.Key, IsPrefix: g.IsPrefix, Access: g.Access, Label: g.Label}) != nil {
			return ErrInvalid
		}
		var n int
		err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM people WHERE app = ? AND subject IN (?,?) AND purged_at IS NULL`,
			g.App, g.Owner, g.Recipient).Scan(&n)
		if err != nil {
			return err
		}
		if g.Owner == g.Recipient || n != 2 {
			res.Skipped++
			continue
		}
		pre := 0
		if g.IsPrefix {
			pre = 1
		}
		var revoked any
		if g.RevokedAt != 0 {
			revoked = g.RevokedAt
		}
		r, err := store.db.ExecContext(ctx, `INSERT OR IGNORE INTO grants (id,app,owner_subject,recipient_subject,kind,object_key,is_prefix,access,label,created_at,updated_at,revoked_at)
            VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, g.ID, g.App, g.Owner, g.Recipient, g.Kind, g.Key, pre, g.Access, g.Label, g.CreatedAt, g.UpdatedAt, revoked)
		if err != nil {
			return err
		}
		if k, _ := r.RowsAffected(); k > 0 {
			res.Grants++
		}
	}
	return nil
}

// Backup writes a consistent snapshot of the data file to dst (VACUUM INTO,
// safe while the bridge runs) and removes sign-in sessions and login state
// from the copy, so a backup holds no live credentials. Restoring is
// copying dst over the data file while the bridge is stopped; people then
// sign in again.
func Backup(ctx context.Context, dbPath, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return errors.New("everything: backup target already exists")
	}
	db, err := openReadOnly(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return err
	}
	if err = os.Chmod(dst, 0o600); err != nil {
		return err
	}
	cp, err := sql.Open("sqlite", dst)
	if err != nil {
		return err
	}
	defer cp.Close()
	for _, q := range []string{`DELETE FROM sessions`, `DELETE FROM login_states`, `VACUUM`} {
		if _, err = cp.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
