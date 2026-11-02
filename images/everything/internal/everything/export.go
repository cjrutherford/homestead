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
const ExportFormat = "homestead-everything-export/1"

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

// ExportManifest is manifest.json. It never contains sessions, login state
// or any secret: only the objects table is read.
type ExportManifest struct {
	Format  string         `json:"format"`
	Objects []ExportObject `json:"objects"`
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
	// One object per line keeps the file readable and the KV bytes exact.
	var buf bytes.Buffer
	buf.WriteString("{\"format\": \"" + ExportFormat + "\", \"objects\": [")
	for i, o := range m.Objects {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString("\n  ")
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err = enc.Encode(o); err != nil {
			return 0, err
		}
		buf.Truncate(buf.Len() - 1)
	}
	buf.WriteString("\n]}\n")
	b := buf.Bytes()
	return len(m.Objects), os.WriteFile(filepath.Join(out, "manifest.json"), b, 0o600)
}

// Import loads an export directory into the store, replacing objects with
// the same identity. It ignores the quota (it restores what was held) but
// validates every key like a normal write. Sessions are not part of exports.
func Import(ctx context.Context, store *Store, dir string) (int, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return 0, err
	}
	var m ExportManifest
	if err = json.Unmarshal(raw, &m); err != nil || m.Format != ExportFormat {
		return 0, errors.New("everything: not a Homestead Everything export")
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
					return 0, err
				}
			}
			err = store.Put(ctx, sc, KindKV, o.Key, val)
		case "file":
			p := filepath.Join(dir, filepath.FromSlash(o.Path))
			if !fs.ValidPath(o.Path) || !strings.HasPrefix(o.Path, "files/") {
				return 0, fmt.Errorf("everything: unsafe path in manifest")
			}
			var val []byte
			if val, err = os.ReadFile(p); err != nil {
				return 0, err
			}
			err = store.Put(ctx, sc, KindFile, o.Key, val)
		default:
			err = ErrInvalid
		}
		if err != nil {
			return 0, err
		}
	}
	return len(m.Objects), nil
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
