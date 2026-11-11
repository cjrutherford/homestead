package backup

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"filippo.io/age"

	"homestead/internal/catalog"
	"homestead/internal/testtools"
)

// The S275 runbook test: every command the runbook documents runs
// literally, with the real age and tar binaries, against a run written
// by the Go backup code. The commands come out of the runbook's fenced
// blocks marked "sh runbook <name>", so the document and the test cannot
// drift. The only things the test supplies are the variables the runbook
// tells the reader to set, and a stand-in for the docker binary.

const runbookPath = "../../docs/architecture/BACKUP_RESTORE_RUNBOOK.md"

var (
	runbookBlock = regexp.MustCompile("(?ms)^```sh runbook ([a-z0-9-]+)\n(.*?)^```$")
	// shellFence matches any fence that opens a shell block, named or not.
	shellFence = regexp.MustCompile("(?m)^```(?:sh|bash|shell|console|zsh)\\b.*$")
)

func runbookBlocks(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatalf("runbook missing: %v", err)
	}
	blocks := map[string]string{}
	named := runbookBlock.FindAllStringSubmatch(string(raw), -1)
	if fences := shellFence.FindAllString(string(raw), -1); len(fences) != len(named) {
		t.Fatalf("the runbook has %d shell fences but %d are named `sh runbook <name>`: every shell block must be named so it is tested (%q)", len(fences), len(named), fences)
	}
	for _, m := range named {
		if _, dup := blocks[m[1]]; dup {
			t.Fatalf("runbook block %q appears twice", m[1])
		}
		blocks[m[1]] = m[2]
	}
	return blocks
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	testtools.SkipUnlessTool(t, name)
}

// dockerShim stands in for the docker binary, which the test cannot
// run: it appends its arguments to $DOCKER_ARGS (one line per call),
// saves what an `exec -i` call reads to $DOCKER_STDIN, and exits with
// $DOCKER_STATUS (default 0), so a block that ignores docker's failure
// is caught.
const dockerShim = "docker() {\n" +
	"\tprintf '%s\\n' \"$*\" >> \"$DOCKER_ARGS\" || return 1\n" +
	"\tcase \"$1\" in exec) cat > \"$DOCKER_STDIN\" || return 1 ;; esac\n" +
	"\treturn \"${DOCKER_STATUS:-0}\"\n" +
	"}\n"

// runbookCmd prepares one documented block under sh -eu with the given
// variables.
func runbookCmd(t *testing.T, blocks map[string]string, used map[string]bool, name string, env map[string]string) *exec.Cmd {
	t.Helper()
	body, ok := blocks[name]
	if !ok {
		t.Fatalf("runbook has no block %q", name)
	}
	used[name] = true
	// A restrictive umask, as root shells often have: modes must come
	// from the archive, not from the shell.
	cmd := exec.Command("sh", "-c", "set -eu\numask 077\n"+dockerShim+body)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}

func runbookShell(t *testing.T, blocks map[string]string, used map[string]bool, name string, env map[string]string) {
	t.Helper()
	if out, err := runbookCmd(t, blocks, used, name, env).CombinedOutput(); err != nil {
		t.Fatalf("runbook block %q failed: %v\n%s\nscript:\n%s", name, err, out, blocks[name])
	}
}

// runbookFails requires a block to exit non-zero.
func runbookFails(t *testing.T, blocks map[string]string, used map[string]bool, name string, env map[string]string) {
	t.Helper()
	if out, err := runbookCmd(t, blocks, used, name, env).CombinedOutput(); err == nil {
		t.Fatalf("runbook block %q succeeded but must fail: %s", name, out)
	}
}

// truncated copies a file without its last bytes.
func truncated(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), filepath.Base(path))
	if err := os.WriteFile(out, raw[:len(raw)-24], 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

// treeEntry is what restore must reproduce for one path.
type treeEntry struct {
	kind    string
	mode    fs.FileMode
	content string
	uid     int
	gid     int
}

func snapshotTree(t *testing.T, root string, skipBelow ...string) map[string]treeEntry {
	t.Helper()
	out := map[string]treeEntry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		uid, gid := ownerOf(info)
		e := treeEntry{mode: info.Mode().Perm(), uid: uid, gid: gid}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, _ := os.Readlink(path)
			e.kind, e.content = "link", link
			e.mode = 0
		case info.IsDir():
			e.kind = "dir"
		default:
			e.kind, e.content = "file", readFile(t, path)
		}
		out[rel] = e
		for _, skip := range skipBelow {
			if rel == skip {
				return fs.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func compareTrees(t *testing.T, label string, want, got map[string]treeEntry) {
	t.Helper()
	var names []string
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, ok := got[name]
		if !ok {
			t.Errorf("%s: %q missing after restore", label, name)
			continue
		}
		g := want[name]
		if g.kind != w.kind || g.content != w.content || g.mode != w.mode {
			t.Errorf("%s: %q = %+v, want %+v", label, name, w, g)
		}
		if os.Geteuid() == 0 && (g.uid != w.uid || g.gid != w.gid) {
			t.Errorf("%s: %q owner %d:%d, want %d:%d", label, name, w.uid, w.gid, g.uid, g.gid)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: unexpected %q after restore", label, name)
		}
	}
}

func TestRunbookRestoresEncryptedRunWithRealAgeAndTar(t *testing.T) {
	requireTool(t, "age")
	requireTool(t, "tar")
	blocks := runbookBlocks(t)
	used := map[string]bool{}

	// An owner-held key, as the runbook assumes: the appliance identity
	// is not given to the restorer at all.
	keys := newKeys()
	applianceRecipients, err := Recipients(keys)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipients := append(applianceRecipients, owner.Recipient())
	work := t.TempDir()
	identity := filepath.Join(work, "owner-identity.txt")
	if err := os.WriteFile(identity, []byte(owner.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Source: an application tree (with an excluded database directory),
	// a stack secrets tree, and a Postgres export.
	mount, stacks, dest := filepath.Join(work, "mnt"), filepath.Join(work, "stacks"), filepath.Join(work, "backups")
	app := filepath.Join(mount, "photos")
	writeFile(t, filepath.Join(app, "config", "settings.json"), `{"a":1}`)
	writeFile(t, filepath.Join(app, "media", "deep", "pic.bin"), "binary\x00data")
	writeFile(t, filepath.Join(app, "postgres", "base", "1", "pg_internal.init"), "live database file")
	if err := os.Symlink("config/settings.json", filepath.Join(app, "link")); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{
		filepath.Join(app, "config", "settings.json"):  0o640,
		filepath.Join(app, "media", "deep"):            0o750,
		filepath.Join(app, "postgres"):                 0o700,
		filepath.Join(app, "media", "deep", "pic.bin"): 0o664,
	} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	stack := filepath.Join(stacks, "photos")
	writeFile(t, filepath.Join(stack, "compose.yaml"), "services:\n  db:\n    image: postgres\n")
	writeFile(t, filepath.Join(stack, "compose.override.yaml"), "services:\n  db:\n    restart: always\n")
	secrets := filepath.Join(stack, "secrets")
	writeFile(t, filepath.Join(secrets, "db_password"), "s3cret-value-9912")
	if err := os.Chmod(filepath.Join(secrets, "db_password"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		for path, owner := range map[string][2]int{
			filepath.Join(app, "config", "settings.json"):  {1234, 4321},
			filepath.Join(app, "postgres"):                 {70, 70},
			filepath.Join(secrets, "db_password"):          {70, 70},
			filepath.Join(app, "media", "deep", "pic.bin"): {1000, 1000},
			secrets: {0, 0},
		} {
			if err := os.Lchown(path, owner[0], owner[1]); err != nil {
				t.Fatal(err)
			}
		}
	}

	dump := "CREATE TABLE t (id int);\nINSERT INTO t VALUES (1);\n-- runbook-dump-marker\n"
	stubExec(t, func(_, _ string, args []string) (string, error) {
		if len(args) > 0 && args[0] == "sh" {
			return dump, nil
		}
		return "", nil
	})
	manifest := scheduleManifest()
	manifest.Database = &catalog.DatabaseBackup{Service: "db", Kind: catalog.DatabasePostgres, Database: "photos", Username: "photos"}
	job := Job{
		Manifest: manifest, Package: "photos", Container: "photos-db", Password: "pw", SrcRoot: app,
		DestRoot: dest, Retain: 2, Recipients: recipients,
		Sources: []Source{
			{Mountpoint: mount, Path: "photos", Class: "application", Exclude: []string{"postgres"}},
			{Mountpoint: stacks, Path: "photos", Class: StackClass, Exclude: []string{"secrets"}},
			{Mountpoint: stacks, Path: "photos/secrets", Class: SecretsClass, Sensitive: true},
		},
	}
	def, err := JobDefinition(job, fixedJobTime)
	if err != nil {
		t.Fatal(err)
	}
	runDef(t, def)
	run := filepath.Join(dest, "photos", "20261008T120000Z")

	// The runbook's first step: read backup.json (plain JSON).
	var m Manifest
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(run, "backup.json"))), &m); err != nil {
		t.Fatal(err)
	}
	if m.FormatVersion != 2 || !m.Encrypted {
		t.Fatalf("manifest = %+v", m)
	}

	restoreRoot := filepath.Join(work, "restored")
	dockerArgs, dockerStdin := filepath.Join(work, "docker.args"), filepath.Join(work, "docker.stdin")
	// Block temp directories land under work, so a failed block leaves
	// nothing behind in the host's /tmp.
	tmpdir := filepath.Join(work, "tmp")
	if err := os.Mkdir(tmpdir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The restore target is the manifest's source mount plus path,
	// re-rooted under restoreRoot so the test never touches the host.
	targetOf := func(entry string) string {
		return filepath.Join(restoreRoot, m.Sources[entry], m.Paths[entry])
	}
	stackTarget := targetOf(StackClass + "/photos")
	base := map[string]string{
		"RUN": run, "IDENTITY": identity, "DOCKER_ARGS": dockerArgs, "DOCKER_STDIN": dockerStdin,
		"TMPDIR": tmpdir, "PACKAGE": "photos", "STACK": stackTarget, "DBSERVICE": "db",
		"DBCONTAINER": "photos-db", "DBUSER": "photos", "DBNAME": "photos",
	}
	with := func(extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	dockerLog := func() string { return readFile(t, dockerArgs) }
	archiveOf := func(entry string) string {
		archive, ok := m.Archives[entry]
		if !ok {
			t.Fatalf("manifest has no archive for %q: %+v", entry, m.Archives)
		}
		return filepath.Join(run, archive)
	}
	leftovers := func(label string) {
		t.Helper()
		if entries, _ := os.ReadDir(tmpdir); len(entries) != 0 {
			t.Fatalf("%s left temporary plaintext behind: %v", label, entries)
		}
	}

	runbookShell(t, blocks, used, "read-manifest", with(nil))
	runbookShell(t, blocks, used, "stop-stack", with(nil))
	if got := dockerLog(); !strings.Contains(got, "compose --project-directory "+stackTarget+" --project-name photos stop") {
		t.Fatalf("stop-stack ran %q", got)
	}

	// The application data tree: list, move aside, extract.
	appEntry := "application/photos"
	appEnv := with(map[string]string{"ARCHIVE": archiveOf(appEntry), "TARGET": targetOf(appEntry)})
	runbookShell(t, blocks, used, "list-archive", appEnv)
	writeFile(t, filepath.Join(targetOf(appEntry), "stale.txt"), "old live data")
	runbookShell(t, blocks, used, "move-aside", appEnv)
	if readFile(t, filepath.Join(targetOf(appEntry)+".before-restore", "stale.txt")) != "old live data" {
		t.Fatalf("%s: stale tree not moved aside", appEntry)
	}
	runbookShell(t, blocks, used, "extract-tree", appEnv)
	// The database directory is kept but its files are not copied.
	want := snapshotTree(t, app, "postgres")
	for name := range want {
		if strings.HasPrefix(name, "postgres"+string(filepath.Separator)) {
			delete(want, name)
		}
	}
	compareTrees(t, appEntry, want, snapshotTree(t, targetOf(appEntry)))
	leftovers("extract-tree")

	// The stack definition, then its secrets into the directory the
	// stack restore left. A stale live stack is moved aside first.
	stackEnv := with(map[string]string{"TARGET": stackTarget, "STACK_ARCHIVE": archiveOf(StackClass + "/photos")})
	writeFile(t, filepath.Join(stackTarget, "stale.yaml"), "old stack")
	runbookShell(t, blocks, used, "move-aside", stackEnv)
	runbookShell(t, blocks, used, "restore-stack", stackEnv)
	if readFile(t, filepath.Join(stackTarget, "compose.yaml")) != readFile(t, filepath.Join(stack, "compose.yaml")) ||
		readFile(t, filepath.Join(stackTarget, "compose.override.yaml")) != readFile(t, filepath.Join(stack, "compose.override.yaml")) {
		t.Fatal("stack definition differs after restore")
	}
	if entries, err := os.ReadDir(filepath.Join(stackTarget, "secrets")); err != nil || len(entries) != 0 {
		t.Fatalf("the stack entry must carry an empty secrets directory: %v %v", entries, err)
	}
	secretsEntry := SecretsClass + "/photos/secrets"
	if targetOf(secretsEntry) != filepath.Join(stackTarget, "secrets") {
		t.Fatalf("secrets target %q is not under the stack", targetOf(secretsEntry))
	}
	secretsEnv := with(map[string]string{"ARCHIVE": archiveOf(secretsEntry), "TARGET": targetOf(secretsEntry)})
	runbookShell(t, blocks, used, "extract-tree", secretsEnv)
	runbookShell(t, blocks, used, "secrets-modes", secretsEnv)
	compareTrees(t, "stack", snapshotTree(t, stack), snapshotTree(t, stackTarget))
	if info, err := os.Stat(filepath.Join(targetOf(secretsEntry), "db_password")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file = %v %v", info, err)
	}
	// The stack archive never carries secret values.
	ids, _ := Identities(keys)
	sig, err := archiveSignature(archiveOf(StackClass+"/photos"), ids)
	if err != nil {
		t.Fatal(err)
	}
	for name := range sig {
		if strings.Contains(name, "db_password") {
			t.Fatalf("stack archive carries %q", name)
		}
	}
	leftovers("restore-stack")

	// Postgres: start only the database, recreate the database, load
	// the decrypted dump.
	runbookShell(t, blocks, used, "postgres-up", with(nil))
	if got := dockerLog(); !strings.Contains(got, "project-name photos up -d db") {
		t.Fatalf("postgres-up ran %q", got)
	}
	runbookShell(t, blocks, used, "postgres-recreate", with(nil))
	if got := dockerLog(); !strings.Contains(got, `exec -i photos-db psql -U photos -d postgres -v ON_ERROR_STOP=1 -c DROP DATABASE "photos" -c CREATE DATABASE "photos" OWNER "photos"`) {
		t.Fatalf("postgres-recreate ran %q", got)
	}
	artifact := archiveOf("photos.sql")
	runbookShell(t, blocks, used, "postgres-restore", with(map[string]string{"ARTIFACT": artifact}))
	if got := readFile(t, dockerStdin); got != dump {
		t.Fatalf("psql received %q, want the decrypted dump", got)
	}
	if got := dockerLog(); !strings.Contains(got, "exec -i photos-db psql -U photos -d photos") {
		t.Fatalf("docker args = %q", got)
	}
	leftovers("postgres-restore")

	// MariaDB/MySQL: same start, then load with that engine's client.
	// The password comes from the restored secret file and travels in
	// the container environment for the one client call.
	runbookShell(t, blocks, used, "mariadb-up", with(nil))
	if got := dockerLog(); !strings.Contains(got, "project-name photos up -d db") {
		t.Fatalf("mariadb-up ran %q", got)
	}
	runbookShell(t, blocks, used, "mariadb-restore", with(map[string]string{
		"ARTIFACT": artifact, "DBPASS": "s3cret-value-9912", "DBCLIENT": "mariadb",
	}))
	if got := readFile(t, dockerStdin); got != dump {
		t.Fatalf("mariadb-restore sent %q, want the decrypted dump", got)
	}
	if got := dockerLog(); !strings.Contains(got, "exec -i -e MYSQL_PWD=s3cret-value-9912 photos-db mariadb --user=photos --database=photos") {
		t.Fatalf("mariadb-restore ran %q", got)
	}
	leftovers("mariadb-restore")

	// SQLite: an export is one encrypted file named <file>.age.
	sqliteBytes := "SQLite format 3\x00 pretend database"
	sqliteArtifact := filepath.Join(work, "app.db.age")
	if err := encryptBytes(sqliteArtifact, []byte(sqliteBytes), recipients); err != nil {
		t.Fatal(err)
	}
	dbFile := filepath.Join(restoreRoot, "app.db")
	runbookShell(t, blocks, used, "sqlite-restore", with(map[string]string{"ARTIFACT": sqliteArtifact, "DBFILE": dbFile}))
	if readFile(t, dbFile) != sqliteBytes {
		t.Fatal("sqlite database differs after restore")
	}
	if info, _ := os.Stat(dbFile); info.Mode().Perm() != 0o600 {
		t.Fatalf("sqlite mode = %v", info.Mode().Perm())
	}

	// Bring the stack back by hand.
	runbookShell(t, blocks, used, "start-stack", with(nil))
	runbookShell(t, blocks, used, "stack-status", with(nil))
	if got := dockerLog(); !strings.Contains(got, "project-name photos up -d\n") || !strings.HasSuffix(got, "project-name photos ps\n") {
		t.Fatalf("docker log = %q", got)
	}

	// docker's failure status propagates out of every block that runs it.
	failing := with(map[string]string{"DOCKER_STATUS": "3", "ARTIFACT": artifact, "DBCLIENT": "mariadb", "DBPASS": "pw"})
	for _, name := range []string{"stop-stack", "postgres-up", "postgres-recreate", "postgres-restore", "mariadb-up", "mariadb-restore", "start-stack", "stack-status"} {
		runbookFails(t, blocks, used, name, failing)
	}

	// A truncated archive fails the tar, postgres and sqlite blocks,
	// before anything is extracted or loaded.
	bad := truncated(t, archiveOf(appEntry))
	badTarget := filepath.Join(restoreRoot, "truncated-tree")
	runbookFails(t, blocks, used, "extract-tree", with(map[string]string{"ARCHIVE": bad, "TARGET": badTarget}))
	if _, err := os.Stat(badTarget); err == nil {
		t.Fatal("a truncated archive created or extracted into the target")
	}
	runbookFails(t, blocks, used, "list-archive", with(map[string]string{"ARCHIVE": bad}))
	badStack := filepath.Join(restoreRoot, "truncated-stack")
	runbookFails(t, blocks, used, "restore-stack", with(map[string]string{"STACK_ARCHIVE": truncated(t, archiveOf(StackClass+"/photos")), "STACK": badStack}))
	if _, err := os.Stat(badStack); err == nil {
		t.Fatal("a truncated stack archive was extracted")
	}
	before := dockerLog()
	runbookFails(t, blocks, used, "postgres-restore", with(map[string]string{"ARTIFACT": truncated(t, artifact)}))
	if dockerLog() != before {
		t.Fatalf("psql was started for a truncated dump: %q", dockerLog())
	}
	runbookFails(t, blocks, used, "mariadb-restore", with(map[string]string{"ARTIFACT": truncated(t, artifact), "DBCLIENT": "mariadb", "DBPASS": "pw"}))
	if dockerLog() != before {
		t.Fatalf("the MariaDB client was started for a truncated dump: %q", dockerLog())
	}
	badDB := filepath.Join(restoreRoot, "truncated.db")
	runbookFails(t, blocks, used, "sqlite-restore", with(map[string]string{"ARTIFACT": truncated(t, sqliteArtifact), "DBFILE": badDB}))
	if _, err := os.Stat(badDB); err == nil {
		t.Fatal("a truncated sqlite export produced a database file")
	}

	// The wrong key is refused by the documented step, before any
	// live data is touched.
	other, _ := age.GenerateX25519Identity()
	wrong := filepath.Join(work, "wrong.txt")
	_ = os.WriteFile(wrong, []byte(other.String()+"\n"), 0o600)
	runbookFails(t, blocks, used, "list-archive", with(map[string]string{"IDENTITY": wrong, "ARCHIVE": archiveOf(appEntry)}))

	assertAllRunbookBlocksUsed(t, blocks, used, "plaintext-tree")
}

func TestRunbookRestoresPlaintextRun(t *testing.T) {
	requireTool(t, "cp")
	blocks := runbookBlocks(t)
	used := map[string]bool{}
	work := t.TempDir()
	mount, dest := filepath.Join(work, "mnt"), filepath.Join(work, "backups")
	src := filepath.Join(mount, "photos", "config")
	writeFile(t, filepath.Join(src, "settings.json"), "{}")
	writeFile(t, filepath.Join(src, "sub", "note.txt"), "plain note")
	if err := os.Chmod(filepath.Join(src, "sub", "note.txt"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/note.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	def, err := Definition(Request{
		Package: "photos", Destination: dest,
		Sources: []Source{{Mountpoint: mount, Path: "photos/config"}},
	}, fixedJobTime)
	if err != nil {
		t.Fatal(err)
	}
	runDef(t, def)
	run := filepath.Join(dest, "photos", "20261008T120000Z")
	var m Manifest
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(run, "backup.json"))), &m); err != nil {
		t.Fatal(err)
	}
	if m.Format() != 1 || m.Encrypted {
		t.Fatalf("expected a plaintext run: %+v", m)
	}
	target := filepath.Join(work, "restored", m.Sources["photos/config"], "photos/config")
	runbookShell(t, blocks, used, "plaintext-tree", map[string]string{
		"RUN": run, "ENTRY": "photos/config", "TARGET": target,
		"DOCKER_ARGS": filepath.Join(work, "a"), "DOCKER_STDIN": filepath.Join(work, "s"),
	})
	compareTrees(t, "plaintext", snapshotTree(t, src), snapshotTree(t, target))
}

// Every runbook block must be exercised by one of the tests, so a new
// documented command cannot ship untested.
func assertAllRunbookBlocksUsed(t *testing.T, blocks map[string]string, used map[string]bool, exempt ...string) {
	t.Helper()
	skip := map[string]bool{}
	for _, name := range exempt {
		skip[name] = true
	}
	for name := range blocks {
		if !used[name] && !skip[name] {
			t.Errorf("runbook block %q is not exercised by the test", name)
		}
	}
}
