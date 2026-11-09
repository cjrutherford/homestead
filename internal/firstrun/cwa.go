package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"homestead/internal/catalog"
)

// CWASeededAccount is the administrator every Calibre-Web Automated
// database is born with (user name admin, password "admin123"). The
// routine takes that password away the first time the app has created its
// database, and renames the account so an Authentik user called "admin"
// can never sign in as it (S448).
const CWASeededAccount = "admin"

// CWAVerified is the Calibre-Web Automated version cwaScript was verified
// against; the pin test fails when the package moves off it.
const CWAVerified = "4.0.8"

// CWAAdminAccount is the name the seeded administrator is given.
const CWAAdminAccount = "homestead-admin"

// cwaScript runs inside the Calibre-Web Automated container with the
// image's own python. The new password arrives on stdin as JSON, never
// argv or environment. It writes /config/app.db as the app's own user
// (so the file keeps its owner) and prints one JSON line:
// {"state": "waiting"|"created"|"ready"|"failed", "detail": ...}.
//
// It changes the account only while "admin123" still opens it, in one
// compare-and-set UPDATE, so an administrator the owner already changed
// (or removed) is left exactly as it is.
//
// VERIFIED AGAINST Calibre-Web Automated v4.0.8: /config/app.db table
// user(id, name, password, ...) with the seeded row admin / admin123
// (werkzeug scrypt hash), werkzeug in /lsiopy, and an "abc" user. Login
// reads the hash on every attempt, so no restart is needed. The pin test
// TestCWAFirstRunPinnedToVerifiedVersion fails when version changes
// until this script is re-verified.
const cwaScript = `
import json, os, pwd, sqlite3, sys
def out(state, detail=""):
    print(json.dumps({"state": state, "detail": detail}))
    sys.exit(0)
try:
    cfg = json.load(sys.stdin)
    account, new, rename = cfg["account"], cfg["password"], cfg["rename"]
    if not (account and new and rename): raise ValueError
except (ValueError, KeyError):
    out("failed", "the account and password are missing")
from werkzeug.security import check_password_hash, generate_password_hash
try:
    u = pwd.getpwnam("abc")
    os.setgid(u.pw_gid)
    os.setuid(u.pw_uid)
except (KeyError, OSError):
    out("waiting", "the application user is not ready yet")
DB = "/config/app.db"
if not os.path.exists(DB):
    out("waiting", "the application database does not exist yet")
db = sqlite3.connect(DB, timeout=30, isolation_level=None)
try:
    db.execute("PRAGMA busy_timeout=30000")
    have = [r[1] for r in db.execute("PRAGMA table_info(user)")]
    if not have:
        out("waiting", "the user table does not exist yet")
    for c in ("id", "name", "password"):
        if c not in have:
            out("failed", "table user lacks column " + c + " (re-verify this routine against the Calibre-Web Automated schema)")
    if db.execute("SELECT COUNT(*) FROM user").fetchone()[0] < 1:
        out("waiting", "the application has not created its users yet")
    db.execute("BEGIN IMMEDIATE")
    row = db.execute("SELECT id, password FROM user WHERE name = ?", (account,)).fetchone()
    if not row or not row[1] or not check_password_hash(row[1], "admin123"):
        db.execute("ROLLBACK")
        out("ready", "no seeded administrator keeps its default password")
    if db.execute("SELECT 1 FROM user WHERE name = ?", (rename,)).fetchone():
        db.execute("ROLLBACK")
        out("failed", "a user already has the name " + rename)
    n = db.execute("UPDATE user SET password = ?, name = ? WHERE id = ? AND password = ?", (generate_password_hash(new), rename, row[0], row[1])).rowcount
    db.execute("COMMIT")
    if n != 1:
        out("failed", "the seeded password was not replaced")
    row = db.execute("SELECT password FROM user WHERE id = ?", (row[0],)).fetchone()
    if not check_password_hash(row[0], new) or check_password_hash(row[0], "admin123"):
        out("failed", "the seeded password was not replaced")
    out("created")
except sqlite3.Error as e:
    out("failed", "database error: %s" % e)
finally:
    db.close()
`

func cwaStep(ctx context.Context, container, password string) (Result, bool, error) {
	input, _ := json.Marshal(map[string]string{"account": CWASeededAccount, "password": password, "rename": CWAAdminAccount})
	raw, err := execIn(ctx, container, string(input), "/lsiopy/bin/python", "-c", cwaScript)
	raw = strings.ReplaceAll(raw, password, "[redacted]")
	return stepReply("Calibre-Web Automated", raw, password, err)
}

// cwaFirstRun replaces the seeded administrator's well-known password
// with a generated, revealable one, and leaves a changed or removed
// account alone. Until it finishes, admin still opens with "admin123".
func cwaFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "calibre-web-automated" || f.Account != CWASeededAccount {
		return "", errors.New("Calibre-Web Automated first-run configuration is unsupported")
	}
	password, err := Password(m, secrets)
	if err != nil {
		return "", err
	}
	container := m.Name + "-" + f.Service
	res, err := waitFor(ctx, m.Title, func() (Result, bool, error) { return cwaStep(ctx, container, password) })
	if err != nil {
		return "", errors.New(err.Error() + " (until this finishes Calibre-Web Automated's seeded administrator admin still opens with the password \"admin123\": change it in the app under your profile)")
	}
	return res, nil
}
