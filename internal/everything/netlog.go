package everything

import (
	"context"
	"time"
)

// net_log (ADR-058 section 5): one persistent row per net.fetch request.
// It records who (the per-app person pseudonym, never the sign-in subject),
// what (method, scheme, host, port, path without the query string, resolved
// address) and how it went (status, bytes, duration, error class). Never
// headers, bodies, query strings or user information. Rows are kept for 30
// days or the newest 10,000 rows, whichever is fewer.
const (
	NetLogRetention = 30 * 24 * time.Hour
	NetLogMaxRows   = 10000
)

// NetLogEntry is one logged request.
type NetLogEntry struct {
	ID         int64
	Time       int64 // Unix seconds
	App        string
	Person     string // per-app pseudonym ("p_...")
	Method     string
	Scheme     string
	Host       string
	Port       int
	Path       string // no query string
	Addr       string // resolved address actually used, "" when none
	Status     int    // upstream status, 0 when none
	BytesIn    int64  // response bytes received
	BytesOut   int64  // request body bytes sent
	DurationMS int64
	Class      string // "ok" or a refusal/failure class
}

func (s *Store) initNetLog() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS net_log (
        id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL,
        app TEXT NOT NULL, person TEXT NOT NULL, method TEXT NOT NULL,
        scheme TEXT NOT NULL, host TEXT NOT NULL, port INTEGER NOT NULL,
        path TEXT NOT NULL, addr TEXT NOT NULL, status INTEGER NOT NULL,
        bytes_in INTEGER NOT NULL, bytes_out INTEGER NOT NULL,
        duration_ms INTEGER NOT NULL, class TEXT NOT NULL
    );
    CREATE INDEX IF NOT EXISTS net_log_app ON net_log (app, id)`)
	return err
}

// SetNet turns the net.fetch operation on or off (off = net_disabled). Call
// it before serving. fence is the egress fence every request goes through; nil
// means the default fence (system resolver, checked dialer).
func (s *Store) SetNet(on bool, fence *NetFence) {
	s.netOn = on
	if fence == nil {
		fence = &NetFence{}
	}
	s.netFence = fence
}

// LogNet appends a row and applies the retention rules.
func (s *Store) LogNet(ctx context.Context, e NetLogEntry) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO net_log (ts, app, person, method, scheme, host, port, path, addr, status, bytes_in, bytes_out, duration_ms, class)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, e.Time, e.App, e.Person, e.Method, e.Scheme, e.Host, e.Port, e.Path, e.Addr,
		e.Status, e.BytesIn, e.BytesOut, e.DurationMS, e.Class)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	_, err = s.db.ExecContext(ctx, `DELETE FROM net_log WHERE ts < ? OR id <= ?`, time.Unix(e.Time, 0).Add(-NetLogRetention).Unix(), id-NetLogMaxRows)
	return err
}

// NetLog lists an app's most recent rows, newest first (limit <= 0: 100).
func (s *Store) NetLog(ctx context.Context, app string, limit int) ([]NetLogEntry, error) {
	if limit <= 0 || limit > NetLogMaxRows {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, app, person, method, scheme, host, port, path, addr, status, bytes_in, bytes_out, duration_ms, class
        FROM net_log WHERE app = ? ORDER BY id DESC LIMIT ?`, app, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NetLogEntry
	for rows.Next() {
		var e NetLogEntry
		if err = rows.Scan(&e.ID, &e.Time, &e.App, &e.Person, &e.Method, &e.Scheme, &e.Host, &e.Port, &e.Path, &e.Addr,
			&e.Status, &e.BytesIn, &e.BytesOut, &e.DurationMS, &e.Class); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
