package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"filippo.io/age"

	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/storage"
	"homestead/internal/testtools"
)

type memKeys struct{ values map[string]string }

func newKeys() *memKeys { return &memKeys{values: map[string]string{}} }

func (m *memKeys) Get(name string) (string, error) {
	v, ok := m.values[name]
	if !ok {
		return "", secret.ErrNotFound
	}
	return v, nil
}

func (m *memKeys) SetSystem(name, value string) error { m.values[name] = value; return nil }

func runDef(t *testing.T, def operation.Definition) {
	t.Helper()
	runner := openJobRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %+v (%v)", done, err)
	}
}

func TestIdentityGeneratedOnceAndStable(t *testing.T) {
	keys := newKeys()
	a, err := ApplianceIdentity(keys)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ApplianceIdentity(keys)
	if err != nil || a.String() != b.String() {
		t.Fatalf("identity changed: %v", err)
	}
	if !strings.HasPrefix(IdentitySecret, secret.SystemSecretPrefix) {
		t.Fatal("identity secret must use the reserved sys_ prefix")
	}
	keys.values[IdentitySecret] = "AGE-SECRET-KEY-garbage"
	if _, err := ApplianceIdentity(keys); err == nil || strings.Contains(err.Error(), "garbage") {
		t.Fatalf("err = %v", err)
	}
}

// Encrypted round trip: tree files, in-tree symlink, modes, the
// database export, and the secret files directory all come back; the
// manifest stays readable and carries no data.
func TestEncryptedRunRoundTrip(t *testing.T) {
	keys := newKeys()
	recipients, err := Recipients(keys)
	if err != nil {
		t.Fatal(err)
	}
	mount, dest := t.TempDir(), t.TempDir()
	job := scheduleJob(t, mount, dest)
	writeFile(t, filepath.Join(mount, "photos", "config", "sub", "note.txt"), "plaintext-marker-7731")
	if err := os.Symlink("sub/note.txt", filepath.Join(mount, "photos", "config", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(mount, "photos", "config", "sub", "note.txt"), 0o640); err != nil {
		t.Fatal(err)
	}
	stacks := t.TempDir()
	secretFile := filepath.Join(stacks, "photos", "secrets", "db_password")
	if err := os.MkdirAll(filepath.Dir(secretFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("s3cret-value-9912"), 0o600); err != nil {
		t.Fatal(err)
	}
	job.Sources = append(job.Sources, Source{Mountpoint: stacks, Path: "photos/secrets", Class: SecretsClass, Sensitive: true})
	job.Recipients = recipients
	stubExec(t, func(_, _ string, _ []string) (string, error) { return "ok", nil })
	def, err := JobDefinition(job, fixedJobTime)
	if err != nil {
		t.Fatal(err)
	}
	runDef(t, def)

	run := filepath.Join(dest, "photos", "20261008T120000Z")
	manifestRaw := readFile(t, filepath.Join(run, "backup.json"))
	for _, leak := range []string{"plaintext-marker-7731", "s3cret-value-9912", "db_password", "AGE-SECRET-KEY"} {
		if strings.Contains(manifestRaw, leak) {
			t.Fatalf("manifest leaks %q: %s", leak, manifestRaw)
		}
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(manifestRaw), &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.Encrypted || manifest.FormatVersion != FormatAge || manifest.Archives["photos/config"] != "photos/config.tar.age" {
		t.Fatalf("manifest = %+v", manifest)
	}
	// Nothing in the run holds plaintext, and no scratch survives.
	assertNoPlaintext(t, dest, "plaintext-marker-7731", "s3cret-value-9912", "SQLite format 3")
	if scratch, _ := filepath.Glob("/dev/shm/homestead-backup-*"); len(scratch) > 0 {
		t.Fatalf("scratch left behind: %v", scratch)
	}

	// Wipe everything, then restore with the appliance identity.
	_ = os.RemoveAll(filepath.Join(mount, "photos", "config"))
	_ = os.RemoveAll(filepath.Join(stacks, "photos"))
	runs, err := ListRuns(dest, "photos")
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns: %v", err)
	}
	dbTarget := filepath.Join(t.TempDir(), "app.db")
	plan, err := PlanRestore(runs[0], map[string]string{"app.db": dbTarget})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := Identities(keys)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := RestoreDefinition(plan, ids...)
	if err != nil {
		t.Fatal(err)
	}
	runDef(t, restore)
	if got := readFile(t, filepath.Join(mount, "photos", "config", "sub", "note.txt")); got != "plaintext-marker-7731" {
		t.Fatalf("restored = %q", got)
	}
	if link, err := os.Readlink(filepath.Join(mount, "photos", "config", "link")); err != nil || link != "sub/note.txt" {
		t.Fatalf("link = %q %v", link, err)
	}
	if info, _ := os.Stat(filepath.Join(mount, "photos", "config", "sub", "note.txt")); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode())
	}
	info, err := os.Stat(secretFile)
	if err != nil || info.Mode().Perm() != 0o600 || readFile(t, secretFile) != "s3cret-value-9912" {
		t.Fatalf("secret file = %v %v", info, err)
	}
	if dirInfo, _ := os.Stat(filepath.Dir(secretFile)); dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("secrets dir mode = %v", dirInfo.Mode())
	}
	db, err := sql.Open("sqlite", "file:"+dbTarget)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='t'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("restored db: %d %v", n, err)
	}
}

func assertNoPlaintext(t *testing.T, root string, needles ...string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() == "backup.json" {
			return nil
		}
		raw, _ := os.ReadFile(path)
		for _, needle := range needles {
			if bytes.Contains(raw, []byte(needle)) {
				t.Fatalf("%s holds plaintext %q", path, needle)
			}
		}
		if !strings.HasSuffix(path, FileSuffix) {
			t.Fatalf("unencrypted artifact %s", path)
		}
		return nil
	})
}

func TestWrongIdentityFailsBeforeClearingTarget(t *testing.T) {
	keys := newKeys()
	recipients, _ := Recipients(keys)
	mount, dest := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(mount, "photos", "a"), "1")
	def, err := Definition(Request{Package: "photos", Destination: dest, Recipients: recipients,
		Sources: []Source{{Mountpoint: mount, Path: "photos", Class: storage.ClassApplication}}}, fixedJobTime)
	if err != nil {
		t.Fatal(err)
	}
	runDef(t, def)
	runs, _ := ListRuns(dest, "photos")
	plan, err := PlanRestore(runs[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := age.GenerateX25519Identity()
	for _, ids := range [][]age.Identity{nil, {other}} {
		restore, _ := RestoreDefinition(plan, ids...)
		runner := openJobRunner(t)
		op, _ := runner.Start(context.Background(), restore, "")
		done, _ := runner.Wait(context.Background(), op.ID)
		if done.Status == operation.StatusSucceeded {
			t.Fatal("restore succeeded without the identity")
		}
		if readFile(t, filepath.Join(mount, "photos", "a")) != "1" {
			t.Fatal("live target was cleared")
		}
	}
}

// Runs written before encryption (no format fields) still restore.
func TestPlaintextRunStillRestores(t *testing.T) {
	mount, dest := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(mount, "photos", "a"), "old")
	def, err := Definition(Request{Package: "photos", Destination: dest,
		Sources: []Source{{Mountpoint: mount, Path: "photos", Class: storage.ClassApplication}}}, fixedJobTime)
	if err != nil {
		t.Fatal(err)
	}
	runDef(t, def)
	runs, _ := ListRuns(dest, "photos")
	if runs[0].Manifest.Encrypted || runs[0].Manifest.Format() != FormatPlaintext {
		t.Fatalf("manifest = %+v", runs[0].Manifest)
	}
	// Drop the new fields entirely, as an older manifest would.
	raw := `{"package":"photos","created_at":"20261008T120000Z","entries":["application/photos"],"files":1,"sources":{"application/photos":"` + mount + `"},"paths":{"application/photos":"photos"}}`
	if err := os.WriteFile(filepath.Join(runs[0].Dir, "backup.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	runs, _ = ListRuns(dest, "photos")
	_ = os.RemoveAll(filepath.Join(mount, "photos"))
	plan, err := PlanRestore(runs[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	restore, _ := RestoreDefinition(plan)
	runDef(t, restore)
	if readFile(t, filepath.Join(mount, "photos", "a")) != "old" {
		t.Fatal("plaintext restore failed")
	}
}

func TestSensitiveSourceRefusesPlaintext(t *testing.T) {
	mount, dest := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(mount, "photos", "secrets", "k"), "v")
	_, err := Definition(Request{Package: "photos", Destination: dest,
		Sources: []Source{{Mountpoint: mount, Path: "photos/secrets", Class: SecretsClass, Sensitive: true}}}, fixedJobTime)
	if err == nil {
		t.Fatal("planned a plaintext backup of secret files")
	}
}

func TestExtractRefusesEscapes(t *testing.T) {
	keys := newKeys()
	recipients, _ := Recipients(keys)
	src := t.TempDir()
	if err := os.Symlink("../../etc", filepath.Join(src, "bad")); err != nil {
		t.Fatal(err)
	}
	if _, err := archiveTree(src, filepath.Join(t.TempDir(), "x.tar.age"), recipients); err == nil {
		t.Fatal("archived an escaping symlink")
	}
}

func TestOwnershipPreserved(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("ownership round trip needs root")
	}
	keys := newKeys()
	recipients, _ := Recipients(keys)
	src, out := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "f"), "x")
	if err := os.Lchown(filepath.Join(src, "f"), 1234, 4321); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(out, "t.tar.age")
	if _, err := archiveTree(src, archive, recipients); err != nil {
		t.Fatal(err)
	}
	ids, _ := Identities(keys)
	target := filepath.Join(out, "restored")
	if err := restoreEncrypted(archive, target, ids, false); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(filepath.Join(target, "f"))
	if st := info.Sys(); st == nil {
		t.Fatal("no stat")
	}
	uid, gid := ownerOf(info)
	if uid != 1234 || gid != 4321 {
		t.Fatalf("owner = %d:%d", uid, gid)
	}
}

// The identity never reaches operation records or errors.
func TestIdentityNotInOperationRecords(t *testing.T) {
	keys := newKeys()
	recipients, _ := Recipients(keys)
	mount, dest := t.TempDir(), t.TempDir()
	job := scheduleJob(t, mount, dest)
	job.Recipients = recipients
	stubExec(t, func(_, _ string, _ []string) (string, error) { return "ok", nil })
	def, _ := JobDefinition(job, fixedJobTime)
	runner := openJobRunner(t)
	op, _ := runner.Start(context.Background(), def, "")
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(done)
	if strings.Contains(string(raw), "AGE-SECRET-KEY") || strings.Contains(string(raw), keys.values[IdentitySecret]) {
		t.Fatal("identity leaked into the operation record")
	}
}

// The real age CLI decrypts a produced archive without Homestead
// (library = encryptor, CLI = independent decryptor). S275 adds the
// literal runbook test; HOMESTEAD_REQUIRE_AGE=1 makes absence fail.
func TestRealAgeCLIDecryptsArchive(t *testing.T) {
	bin := testtools.SkipUnlessTool(t, "age")
	keys := newKeys()
	recipients, _ := Recipients(keys)
	src, out := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "dir", "f.txt"), "cli-check")
	archive := filepath.Join(out, "t.tar.age")
	if _, err := archiveTree(src, archive, recipients); err != nil {
		t.Fatal(err)
	}
	idFile := filepath.Join(out, "identity.txt")
	if err := os.WriteFile(idFile, []byte(keys.values[IdentitySecret]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(out, "t.tar")
	if raw, err := exec.Command(bin, "-d", "-i", idFile, "-o", tarPath, archive).CombinedOutput(); err != nil {
		t.Fatalf("age -d: %v: %s", err, raw)
	}
	extract := filepath.Join(out, "x")
	_ = os.MkdirAll(extract, 0o755)
	if raw, err := exec.Command("tar", "-xf", tarPath, "-C", extract).CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, raw)
	}
	if readFile(t, filepath.Join(extract, "dir", "f.txt")) != "cli-check" {
		t.Fatal("contents differ")
	}
}

// ownerOf is a test helper's view of file ownership.
func ownerOf(info fs.FileInfo) (int, int) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid), int(stat.Gid)
	}
	return -1, -1
}
