package appsso

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/testtools"
)

// kumaSignInVerified is the Uptime Kuma version the own-login-off script in
// catalog/uptime-kuma.yaml was verified against (S422): db-config.json read
// by server/database.js, the user and setting tables (db/knex_init_db.js),
// the setting value stored JSON-encoded (server/settings.js), disableAuth
// read at socket connect (server.js: autoLogin for the first user row) and
// by basicAuth/apiAuth (server/auth.js), the first-user check at startup
// (needSetup), and bcryptjs hashes in user.password.
const kumaSignInVerified = "2.5.7"

func realKuma(t *testing.T) catalog.Manifest {
	t.Helper()
	m, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	k, err := m.Get("uptime-kuma")
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Validate(); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestKumaOwnLoginOffPinnedToVerifiedVersion(t *testing.T) {
	m := realKuma(t)
	fa := m.SSO.ForwardAuth
	if m.Version != kumaSignInVerified || !strings.HasSuffix(m.Services[0].Image, ":"+kumaSignInVerified) {
		t.Fatalf("uptime-kuma is %s but its own-login-off script was verified against %s. Before bumping: re-check db-config.json (server/database.js), "+
			"the user and setting tables (db/knex_init_db.js), the JSON-encoded setting value (server/settings.js), disableAuth in server/server.js and server/auth.js, "+
			"the /app/node_modules/@louislam/sqlite3 path in the image, and every bypass path for anything disableAuth would make writable; then update the script's VERIFIED and kumaSignInVerified (S422).",
			m.Version, kumaSignInVerified)
	}
	o := fa.OwnLoginOff
	if o == nil || !fa.RequiresHostBlock || fa.Access != catalog.AccessAdminOnly {
		t.Fatalf("forwardauth = %+v", fa)
	}
	for name, argv := range map[string][]string{"prepare": o.Prepare, "account": o.Account, "onapply": o.OnApply, "onoff": o.OnOff} {
		if len(argv) != 4 || argv[0] != "node" || argv[1] != "-e" || !strings.Contains(argv[2], `const VERIFIED = '`+kumaSignInVerified+`'`) || argv[3] != map[string]string{"prepare": "prepare", "account": "account", "onapply": "on", "onoff": "off"}[name] {
			t.Fatalf("%s argv shape: %v", name, describeArgv(argv))
		}
	}
	for _, bad := range []string{"console.log(who", "console.error(who", "execSync", "spawn", "DELETE", "DROP"} {
		if strings.Contains(o.OnApply[2], bad) {
			t.Errorf("script contains %q", bad)
		}
	}
}

func TestKumaOwnLoginOffFailsClosedWithoutTheHostBlock(t *testing.T) {
	in := kumaInputs(t)
	in.HostBlockRequired = true
	in.HostBlockProblem = "nftables is not available on this host."
	if _, err := BuildPlan(in, true); err == nil || !strings.Contains(err.Error(), "turns its own login off") {
		t.Fatalf("plan must refuse without the host block: %v", err)
	}
}

func TestKumaOffPlanTurnsLoginBackOnFirst(t *testing.T) {
	in := kumaInputs(t)
	in.Applied = &Record{Package: "uptime-kuma", Adapter: ForwardAdapter, RedirectURIs: []string{"https://uptime-kuma.lan"}, Issuer: "https://auth.lan"}
	p, err := BuildPlan(in, false)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(p.Changes, "\n")
	first := p.Changes[0]
	if !strings.Contains(first, "own login back on first") || !strings.Contains(all, "npm run reset-password") || !strings.Contains(all, "uptime-kuma-uptime-kuma") {
		t.Fatalf("off plan:\n%s", all)
	}
}

func TestOwnLoginUsernameAndHash(t *testing.T) {
	for in, want := range map[string]string{"chris": "chris", " Chris R ": "ChrisR", "": "owner", "!!!": "owner", "a@b.c": "a@b.c"} {
		if got := ownLoginUsername(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	a, err := unknownPasswordHash()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := unknownPasswordHash()
	if a == b || !strings.HasPrefix(a, "$2a$10$") || len(a) != 60 {
		t.Fatalf("hashes %q %q", a, b)
	}
}

func TestOwnLoginRestartsOnlyWhenAsked(t *testing.T) {
	f := stubDocker(t)
	o := *realKuma(t).SSO.ForwardAuth.OwnLoginOff
	f.fail = func(call) (string, error) { return "uptime-kuma sign-in on (already on)\n", nil }
	if err := ownLoginOff(context.Background(), "c", "owner", o); err != nil {
		t.Fatal(err)
	}
	if err := ownLoginOn(context.Background(), "c", o); err != nil {
		t.Fatal(err)
	}
	if len(f.restarts) != 0 || len(f.calls) != 3 {
		t.Fatalf("restarts %v calls %d", f.restarts, len(f.calls))
	}
	// Prepare refusing (another database type) stops before any account is made.
	f.calls = nil
	f.fail = func(call) (string, error) { return "", errors.New("exit 1") }
	if err := ownLoginOff(context.Background(), "c", "owner", o); err == nil || len(f.calls) != 1 {
		t.Fatalf("err %v calls %d", err, len(f.calls))
	}
}

// shimSqlite is @louislam/sqlite3's callback API over node:sqlite, so the
// shipped script can run against a real SQLite file here.
const shimSqlite = `const { DatabaseSync } = require('node:sqlite')
class Database {
  constructor(file) { this.d = new DatabaseSync(file) }
  run(q, p, cb) { try { this.d.prepare(q).run(...p); cb(null) } catch (e) { cb(e) } }
  all(q, p, cb) { try { cb(null, this.d.prepare(q).all(...p)) } catch (e) { cb(e) } }
  close() { this.d.close() }
}
module.exports = { Database }
`

// TestKumaScriptAgainstARealDatabase runs the shipped script with real node
// and node:sqlite (skipped when this host's node lacks it) against files
// shaped like Uptime Kuma's.
func TestKumaScriptAgainstARealDatabase(t *testing.T) {
	node := testtools.SkipUnlessTool(t, "node")
	if out, err := exec.Command(node, "-e", "require('node:sqlite')").CombinedOutput(); err != nil {
		testtools.SkipOrFail(t, "node", "no node:sqlite: "+string(out))
	}
	o := *realKuma(t).SSO.ForwardAuth.OwnLoginOff
	script := o.OnApply[2]
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, "package.json")
	shim := filepath.Join(dir, "sqlite3.js")
	_ = os.WriteFile(pkg, []byte(`{"version":"`+kumaSignInVerified+`"}`), 0o600)
	_ = os.WriteFile(shim, []byte(shimSqlite), 0o600)
	patched := strings.NewReplacer("'/app/data'", "'"+data+"'", "/app/package.json", pkg, "/app/node_modules/@louislam/sqlite3", shim).Replace(script)
	hash, err := unknownPasswordHash()
	if err != nil {
		t.Fatal(err)
	}
	stdin := `{"username":"chris","hash":"` + hash + `"}` + "\n"
	run := func(mode, in string) (string, error) {
		c := exec.Command(node, "--no-warnings", "-e", patched, mode)
		c.Stdin = strings.NewReader(in)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	sql := func(q string) string {
		out, err := exec.Command(node, "--no-warnings", "-e", `const {DatabaseSync}=require('node:sqlite');const d=new DatabaseSync(process.argv[1]);const q=process.argv[2];
if(/^select/i.test(q)){console.log(JSON.stringify(d.prepare(q).all()))}else{d.exec(q)}`, filepath.Join(data, "kuma.db"), q).Output()
		if err != nil {
			t.Fatalf("sql %q: %v %s", q, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// prepare: writes the SQLite choice once, asks for a restart once.
	if out, err := run("prepare", ""); err != nil || !wantsRestart(out) {
		t.Fatalf("prepare: %v %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "db-config.json")); !strings.Contains(string(b), `"sqlite"`) {
		t.Fatalf("db-config.json = %s", b)
	}
	if out, err := run("prepare", ""); err != nil || wantsRestart(out) {
		t.Fatalf("second prepare: %v %s", err, out)
	}
	// Another database type refuses with guidance.
	_ = os.WriteFile(filepath.Join(data, "db-config.json"), []byte(`{"type":"mariadb"}`), 0o600)
	if out, err := run("prepare", ""); err == nil || !strings.Contains(out, "mariadb") || !strings.Contains(out, "SQLite") {
		t.Fatalf("mariadb: %v %s", err, out)
	}
	_ = os.WriteFile(filepath.Join(data, "db-config.json"), []byte(`{"type":"sqlite"}`), 0o600)
	// No database yet: on retries (fails), off is a no-op.
	if _, err := run("on", stdin); err == nil {
		t.Fatal("on without a database succeeded")
	}
	if out, err := run("off", ""); err != nil || !strings.Contains(out, "no database yet") {
		t.Fatalf("off without a database: %v %s", err, out)
	}
	// Tables not complete yet: on refuses.
	_ = os.WriteFile(filepath.Join(data, "kuma.db"), nil, 0o600)
	sql("CREATE TABLE user (id INTEGER PRIMARY KEY, username TEXT)")
	if out, err := run("on", stdin); err == nil || !strings.Contains(out, "lacks column") {
		t.Fatalf("column guard: %v %s", err, out)
	}
	sql("DROP TABLE user")
	sql("CREATE TABLE user (id INTEGER PRIMARY KEY, username TEXT UNIQUE, password TEXT, active BOOLEAN NOT NULL DEFAULT 1)")
	sql("CREATE TABLE setting (id INTEGER PRIMARY KEY, key TEXT UNIQUE NOT NULL, value TEXT, type TEXT)")
	// Bad stdin never reaches the database.
	for _, bad := range []string{"", "{}", `{"username":"x","hash":"plain-password"}`} {
		if _, err := run("on", bad); err == nil {
			t.Fatalf("stdin %q accepted", bad)
		}
	}
	if sql("SELECT COUNT(*) AS n FROM user") != `[{"n":0}]` {
		t.Fatal("a user was created from bad stdin")
	}
	// account (S451): the install's first run creates the account only, with
	// the login left on and no restart; a second run changes nothing.
	if out, err := run("account", stdin); err != nil || wantsRestart(out) || !strings.Contains(out, "account created") || strings.Contains(out, hash) {
		t.Fatalf("account: %v %s", err, out)
	}
	if sql("SELECT COUNT(*) AS n FROM setting") != `[{"n":0}]` {
		t.Fatal("account touched the login switch")
	}
	if out, err := run("account", stdin); err != nil || !strings.Contains(out, "account exists") {
		t.Fatalf("second account: %v %s", err, out)
	}
	sql("DELETE FROM user")
	out, err := run("on", stdin)
	if err != nil || !wantsRestart(out) || strings.Contains(out, hash) {
		t.Fatalf("on: %v %s", err, out)
	}
	if got := sql("SELECT username, password FROM user"); got != `[{"username":"chris","password":"`+hash+`"}]` {
		t.Fatalf("user = %s", got)
	}
	if got := sql("SELECT key, value FROM setting"); got != `[{"key":"disableAuth","value":"true"},{"key":"trustProxy","value":"true"}]` {
		t.Fatalf("setting = %s", got)
	}
	if out, err := run("on", stdin); err != nil || wantsRestart(out) || !strings.Contains(out, "already on") {
		t.Fatalf("second on: %v %s", err, out)
	}
	// off turns it back on without deleting the account.
	if out, err := run("off", ""); err != nil || !wantsRestart(out) {
		t.Fatalf("off: %v %s", err, out)
	}
	if got := sql("SELECT value FROM setting WHERE key = 'disableAuth'"); got != `[{"value":"false"}]` {
		t.Fatalf("after off = %s", got)
	}
	if sql("SELECT COUNT(*) AS n FROM user") != `[{"n":1}]` {
		t.Fatal("off deleted the account")
	}
	if out, err := run("off", ""); err != nil || wantsRestart(out) || !strings.Contains(out, "already off") {
		t.Fatalf("second off: %v %s", err, out)
	}
	// An owner who already set Kuma up keeps their account and password.
	sql("UPDATE user SET username = 'mine', password = 'theirs'")
	sql("UPDATE setting SET value = 'false'")
	if _, err := run("on", stdin); err != nil {
		t.Fatal(err)
	}
	if got := sql("SELECT username, password FROM user"); got != `[{"username":"mine","password":"theirs"}]` {
		t.Fatalf("existing account changed: %s", got)
	}
	// A different version refuses to turn the login off, but on works.
	_ = os.WriteFile(pkg, []byte(`{"version":"99.0.0"}`), 0o600)
	for _, mode := range []string{"prepare", "on"} {
		if out, err := run(mode, stdin); err == nil || !strings.Contains(out, "verified against") {
			t.Fatalf("version guard %s: %v %s", mode, err, out)
		}
	}
	if _, err := run("off", ""); err != nil {
		t.Fatalf("off must work on any version: %v", err)
	}
}
