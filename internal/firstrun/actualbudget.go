package firstrun

import (
	"context"
	"errors"
	"strings"

	"homestead/internal/catalog"
)

// ActualBudgetVerified is the Actual version actualBudgetScript was
// verified against. The pin test fails when the manifest moves off it:
// re-verify the account database shape first (S447).
const ActualBudgetVerified = "26.10.0"

// actualBudgetScript runs inside the container with node (node:sqlite). It
// makes the appliance owner Actual's server owner before anyone can sign
// in: Actual gives the owner role to whoever signs in through OpenID while
// no named user exists, so the owner's own row is inserted first.
//
// VERIFIED AGAINST actualbudget/actual-server:26.10.0 (chunks/
// 1719409568000-multiuser, account-db, loginWithOpenIdFinalize): table users
// (id, user_name, display_name, role, enabled, owner); the migration's own
// row has user_name ” (the password login); "no named user" is
// count(user_name <> ”) = 0; a sign-in whose preferred_username matches an
// enabled users.user_name signs in as that row without creating one; an
// empty display_name is filled from the provider's name; the first
// sign-in's files move from the ” user to the owner (files.owner).
// argv[1] is the owner's Authentik username (not a secret). Prints one
// word: starting, seeded or present.
const actualBudgetScript = `
const VERIFIED = '` + ActualBudgetVerified + `'
const DB = '/data/server-files/account.sqlite'
const fs = require('node:fs')
const die = (m) => { console.error('actual-budget owner: ' + m); process.exit(1) }
const owner = process.argv[1] || ''
if (!owner) die('no owner name')
let seen
try { seen = JSON.parse(fs.readFileSync('/app/package.json', 'utf8')).version } catch { die('cannot read the Actual version') }
if (seen !== VERIFIED) die('this is Actual ' + seen + ' but the routine was verified against ' + VERIFIED)
if (!fs.existsSync(DB)) { console.log('starting'); process.exit(0) }
const { DatabaseSync } = require('node:sqlite')
const db = new DatabaseSync(DB)
let open = false
try {
  db.exec('PRAGMA busy_timeout = 30000')
  const cols = (t) => db.prepare("SELECT name FROM pragma_table_info('" + t + "')").all().map((c) => c.name)
  const users = cols('users')
  if (users.length === 0) { console.log('starting'); process.exit(0) }
  for (const c of ['id', 'user_name', 'display_name', 'role', 'enabled', 'owner']) if (!users.includes(c)) die('table users lacks column ' + c)
  if (!cols('files').includes('owner')) die('table files lacks column owner')
  db.exec('BEGIN IMMEDIATE')
  open = true
  const named = db.prepare("SELECT count(*) AS n FROM users WHERE user_name <> ''").get().n
  if (named > 0) {
    db.exec('COMMIT'); open = false
    console.log('present'); process.exit(0)
  }
  const id = require('node:crypto').randomUUID()
  db.prepare("INSERT INTO users (id, user_name, display_name, enabled, owner, role) VALUES (?, ?, '', 1, 1, 'ADMIN')").run(id, owner)
  const pw = db.prepare("SELECT id FROM users WHERE user_name = ''").get()
  if (pw) db.prepare('UPDATE files SET owner = ? WHERE owner = ?').run(id, pw.id)
  db.exec('COMMIT'); open = false
  console.log('seeded')
} catch (e) {
  if (open) { try { db.exec('ROLLBACK') } catch {} }
  die('the database access failed: ' + (e && e.code ? e.code : 'error'))
}
`

// actualBudgetFirstRun seeds the appliance owner as Actual's server owner
// so no person can win the first-sign-in race. Without a known owner
// (Authentik not linked yet) nothing is done: the routine runs again when
// sign-in is applied. Idempotent: any named user already present is left
// alone.
func actualBudgetFirstRun(ctx context.Context, m catalog.Manifest, owner string) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "actual-budget" {
		return "", errors.New("Actual Budget first-run configuration is unsupported")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Ready, nil
	}
	container := m.Name + "-" + f.Service
	return waitFor(ctx, m.Title, func() (Result, bool, error) {
		out, err := execIn(ctx, container, "", "node", "-e", actualBudgetScript, owner)
		switch strings.TrimSpace(out) {
		case "seeded":
			return Created, true, nil
		case "present":
			return Ready, true, nil
		case "starting":
			return "", false, errors.New("Actual Budget is starting")
		}
		if err != nil {
			return "", true, errors.New("Actual Budget refused its owner setup; its account database may have changed in an update (" + firstLine(out) + ")")
		}
		return "", false, errors.New("Actual Budget is starting")
	})
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
