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
)

// The S275 runbook test: every command the runbook documents runs
// literally, with the real age and tar binaries, against a run written
// by the Go backup code. The commands come out of the runbook's fenced
// blocks marked "sh runbook <name>", so the document and the test cannot
// drift. The only things the test supplies are the variables the runbook
// tells the reader to set, and a stand-in for the docker binary.

const runbookPath = "../../docs/architecture/BACKUP_RESTORE_RUNBOOK.md"

var runbookBlock = regexp.MustCompile("(?ms)^```sh runbook ([a-z0-9-]+)\n(.*?)^```$")

func runbookBlocks(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatalf("runbook missing: %v", err)
	}
	blocks := map[string]string{}
	for _, m := range runbookBlock.FindAllStringSubmatch(string(raw), -1) {
		if _, dup := blocks[m[1]]; dup {
			t.Fatalf("runbook block %q appears twice", m[1])
		}
		blocks[m[1]] = m[2]
	}
	return blocks
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if os.Getenv("HOMESTEAD_REQUIRE_AGE") != "" {
			t.Fatalf("%s not on PATH and HOMESTEAD_REQUIRE_AGE is set", name)
		}
		t.Skipf("%s not installed; skipping the runbook test (acceptance installs it and sets HOMESTEAD_REQUIRE_AGE=1)", name)
	}
}

// runbookShell runs one documented block under sh -eu with the given
// variables. docker is a shell function recording what the block pipes
// into it, because the test has no database container.
func runbookShell(t *testing.T, blocks map[string]string, used map[string]bool, name string, env map[string]string) {
	t.Helper()
	body, ok := blocks[name]
	if !ok {
		t.Fatalf("runbook has no block %q", name)
	}
	used[name] = true
	// A restrictive umask, as root shells often have: modes must come
	// from the archive, not from the shell.
	script := "set -eu\numask 077\n" +
		"docker() { printf '%s\\n' \"$*\" > \"$DOCKER_ARGS\"; cat > \"$DOCKER_STDIN\"; }\n" +
		body
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("runbook block %q failed: %v\n%s\nscript:\n%s", name, err, out, body)
	}
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
	secrets := filepath.Join(stacks, "photos", "secrets")
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
	base := map[string]string{"RUN": run, "IDENTITY": identity, "DOCKER_ARGS": dockerArgs, "DOCKER_STDIN": dockerStdin}
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
	// The restore target is the manifest's source mount plus path,
	// re-rooted under restoreRoot so the test never touches the host.
	targetOf := func(entry string) string {
		return filepath.Join(restoreRoot, m.Sources[entry], m.Paths[entry])
	}

	type tree struct {
		entry, src string
		skip       []string
	}
	trees := []tree{
		{"application/photos", app, []string{"postgres"}},
		{SecretsClass + "/photos/secrets", secrets, nil},
	}
	for _, tr := range trees {
		archive, ok := m.Archives[tr.entry]
		if !ok {
			t.Fatalf("manifest has no archive for %q: %+v", tr.entry, m.Archives)
		}
		env := with(map[string]string{"ARCHIVE": filepath.Join(run, archive), "TARGET": targetOf(tr.entry)})
		runbookShell(t, blocks, used, "list-archive", env)
		// A stale live tree exists: the runbook moves it aside first.
		writeFile(t, filepath.Join(targetOf(tr.entry), "stale.txt"), "old live data")
		runbookShell(t, blocks, used, "move-aside", env)
		if readFile(t, filepath.Join(targetOf(tr.entry)+".before-restore", "stale.txt")) != "old live data" {
			t.Fatalf("%s: stale tree not moved aside", tr.entry)
		}
		runbookShell(t, blocks, used, "extract-tree", env)
		if tr.entry == SecretsClass+"/photos/secrets" {
			runbookShell(t, blocks, used, "secrets-modes", env)
		}
		want := snapshotTree(t, tr.src)
		if len(tr.skip) > 0 {
			// The database directory is kept but its files are not copied.
			want = snapshotTree(t, tr.src, tr.skip...)
			for name := range want {
				if strings.HasPrefix(name, "postgres"+string(filepath.Separator)) {
					delete(want, name)
				}
			}
		}
		compareTrees(t, tr.entry, want, snapshotTree(t, targetOf(tr.entry)))
	}

	// Secret files restore 0600 under a 0700 directory.
	info, err := os.Stat(filepath.Join(targetOf(SecretsClass+"/photos/secrets"), "db_password"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file = %v %v", info, err)
	}

	// Postgres: the documented pipeline decrypts the dump into psql.
	artifact, ok := m.Archives["photos.sql"]
	if !ok {
		t.Fatalf("manifest has no dump archive: %+v", m.Archives)
	}
	runbookShell(t, blocks, used, "postgres-restore", with(map[string]string{
		"ARTIFACT": filepath.Join(run, artifact), "DBCONTAINER": "photos-db", "DBUSER": "photos", "DBNAME": "photos",
	}))
	if got := readFile(t, dockerStdin); got != dump {
		t.Fatalf("psql received %q, want the decrypted dump", got)
	}
	if got := strings.TrimSpace(readFile(t, dockerArgs)); !strings.Contains(got, "exec -i photos-db psql -U photos -d photos") {
		t.Fatalf("docker args = %q", got)
	}

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

	// The wrong key is refused by the documented step, before any
	// live data is touched.
	other, _ := age.GenerateX25519Identity()
	wrong := filepath.Join(work, "wrong.txt")
	_ = os.WriteFile(wrong, []byte(other.String()+"\n"), 0o600)
	cmd := exec.Command("sh", "-c", "set -eu\n"+blocks["list-archive"])
	cmd.Env = append(os.Environ(), "IDENTITY="+wrong, "ARCHIVE="+filepath.Join(run, m.Archives["application/photos"]))
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("a wrong identity listed the archive: %s", out)
	}

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
