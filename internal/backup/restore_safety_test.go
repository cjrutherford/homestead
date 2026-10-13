package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"homestead/internal/operation"
	"homestead/internal/storage"
)

// encryptedFixture archives a tree holding one big file and returns the
// archive path plus the identities that open it.
func encryptedFixture(t *testing.T) (string, []age.Identity) {
	t.Helper()
	keys := newKeys()
	recipients, _ := Recipients(keys)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "big.bin"), strings.Repeat("0123456789abcdef", 20000))
	writeFile(t, filepath.Join(src, "small.txt"), "new")
	archive := filepath.Join(t.TempDir(), "x.tar.age")
	if _, err := archiveTree(src, archive, recipients); err != nil {
		t.Fatal(err)
	}
	ids, _ := Identities(keys)
	return archive, ids
}

func liveTarget(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "live")
	writeFile(t, filepath.Join(target, "keep.txt"), "live data")
	return target
}

func assertUntouched(t *testing.T, target string) {
	t.Helper()
	if readFile(t, filepath.Join(target, "keep.txt")) != "live data" {
		t.Fatal("live target changed")
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Fatalf("staging left beside the target: %v", entries)
	}
}

// Damaged streams fail during staging, before the live target is
// touched: truncation at the end or middle, a flipped body byte, and a
// truncated final chunk.
func TestDamagedStreamsLeaveTargetUntouched(t *testing.T) {
	archive, ids := encryptedFixture(t)
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	flipped := append([]byte(nil), raw...)
	flipped[len(flipped)/2] ^= 0xff
	cases := map[string][]byte{
		"truncated-tail":  raw[:len(raw)-40],
		"truncated-half":  raw[:len(raw)/2],
		"flipped-body":    flipped,
		"truncated-chunk": raw[:len(raw)-17],
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			damaged := filepath.Join(t.TempDir(), "d.tar.age")
			if err := os.WriteFile(damaged, data, 0o600); err != nil {
				t.Fatal(err)
			}
			target := liveTarget(t)
			if err := restoreEncrypted(damaged, target, ids, false); err == nil {
				t.Fatal("damaged stream restored")
			}
			assertUntouched(t, target)
		})
	}
}

func TestDamagedFileArtifactLeavesTargetUntouched(t *testing.T) {
	keys := newKeys()
	recipients, _ := Recipients(keys)
	ids, _ := Identities(keys)
	file := filepath.Join(t.TempDir(), "db.sql.age")
	if err := encryptBytes(file, bytes.Repeat([]byte("row\n"), 50000), recipients); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	if err := os.WriteFile(file, raw[:len(raw)-25], 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "db.sql")
	if err := os.WriteFile(target, []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreEncrypted(file, target, ids, false); err == nil {
		t.Fatal("truncated database artifact restored")
	}
	if readFile(t, target) != "live" {
		t.Fatal("live database file changed")
	}
	if entries, _ := os.ReadDir(filepath.Dir(target)); len(entries) != 1 {
		t.Fatalf("staging left: %v", entries)
	}
}

func TestSignatureDetectsTruncation(t *testing.T) {
	archive, ids := encryptedFixture(t)
	raw, _ := os.ReadFile(archive)
	if err := os.WriteFile(archive, raw[:len(raw)-17], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := archiveSignature(archive, ids); err == nil {
		t.Fatal("verify accepted a truncated stream")
	}
}

func TestGoodRestoreSwapsAndRemovesOld(t *testing.T) {
	archive, ids := encryptedFixture(t)
	target := liveTarget(t)
	if err := restoreEncrypted(archive, target, ids, false); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(target, "small.txt")) != "new" {
		t.Fatal("not restored")
	}
	if _, err := os.Stat(filepath.Join(target, "keep.txt")); !os.IsNotExist(err) {
		t.Fatal("mirror semantics lost: old file survived")
	}
	if entries, _ := os.ReadDir(filepath.Dir(target)); len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestRestoreRefusesNewerFormat(t *testing.T) {
	run := Run{Package: "p", Stamp: "20261008T120000Z", Dir: t.TempDir(),
		Manifest: Manifest{Package: "p", Entries: []string{"a"}, Sources: map[string]string{"a": "/x"}, FormatVersion: CurrentFormat + 1}}
	if _, err := PlanRestore(run, nil); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("err = %v", err)
	}
}

func craftedArchive(t *testing.T, build func(tw *tar.Writer)) (string, []age.Identity) {
	t.Helper()
	keys := newKeys()
	recipients, _ := Recipients(keys)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	build(tw)
	_ = tw.Close()
	path := filepath.Join(t.TempDir(), "c.tar.age")
	if err := encryptBytes(path, buf.Bytes(), recipients); err != nil {
		t.Fatal(err)
	}
	ids, _ := Identities(keys)
	return path, ids
}

func TestExtractRefusesLinkedParentsAndEscapes(t *testing.T) {
	archive, ids := craftedArchive(t, func(tw *tar.Writer) {
		_ = tw.WriteHeader(&tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755})
		_ = tw.WriteHeader(&tar.Header{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "d", Mode: 0o777})
		_ = tw.WriteHeader(&tar.Header{Name: "l/f", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
		_, _ = tw.Write([]byte("x"))
	})
	target := liveTarget(t)
	if err := restoreEncrypted(archive, target, ids, false); err == nil {
		t.Fatal("extracted through a symlinked parent")
	}
	assertUntouched(t, target)
	archive, ids = craftedArchive(t, func(tw *tar.Writer) {
		_ = tw.WriteHeader(&tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
		_, _ = tw.Write([]byte("x"))
	})
	if err := restoreEncrypted(archive, target, ids, false); err == nil {
		t.Fatal("extracted a path escape")
	}
	assertUntouched(t, target)
}

func TestNonRootSensitiveOwnershipFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("covers the non-root path")
	}
	archive, ids := craftedArchive(t, func(tw *tar.Writer) {
		_ = tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o700, Uid: 0, Gid: 0})
		_ = tw.WriteHeader(&tar.Header{Name: "key", Typeflag: tar.TypeReg, Mode: 0o600, Size: 1, Uid: 0, Gid: 0})
		_, _ = tw.Write([]byte("x"))
	})
	target := liveTarget(t)
	if err := restoreEncrypted(archive, target, ids, true); err == nil || !strings.Contains(err.Error(), "needs root") {
		t.Fatalf("err = %v", err)
	}
	assertUntouched(t, target)

	var warned []string
	old := warnf
	warnf = func(format string, _ ...any) { warned = append(warned, format) }
	t.Cleanup(func() { warnf = old })
	if err := restoreEncrypted(archive, target, ids, false); err != nil {
		t.Fatal(err)
	}
	if len(warned) != 1 {
		t.Fatalf("warnings = %v", warned)
	}
}

func TestSweepScratch(t *testing.T) {
	old := scratchBase
	scratchBase = t.TempDir()
	t.Cleanup(func() { scratchBase = old })
	stale := filepath.Join(scratchBase, scratchPrefix+"abc")
	other := filepath.Join(scratchBase, "unrelated")
	for _, d := range []string{stale, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	SweepScratch()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale scratch survived")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("unrelated directory removed")
	}
}

func runFails(t *testing.T, def operation.Definition) {
	t.Helper()
	runner := openJobRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatal(err)
	}
	done, _ := runner.Wait(context.Background(), op.ID)
	if done.Status == operation.StatusSucceeded {
		t.Fatal("run succeeded, wanted failure")
	}
}

func listRuns(t *testing.T, dest string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(dest, "photos"))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// Retention counts complete runs only, and an interrupted backup leaves
// no run behind and never costs the last good one.
func TestInterruptedBackupKeepsLastGoodRun(t *testing.T) {
	keys := newKeys()
	recipients, _ := Recipients(keys)
	mount, dest := t.TempDir(), t.TempDir()
	job := scheduleJob(t, mount, dest)
	job.Retain = 1
	job.Recipients = recipients
	stubExec(t, func(_, _ string, _ []string) (string, error) { return "ok", nil })
	first, _ := JobDefinition(job, fixedJobTime)
	runDef(t, first)

	// The next run fails mid-copy: its second source is missing.
	failing := job
	failing.Sources = append(append([]Source(nil), job.Sources...),
		Source{Mountpoint: mount, Path: "photos/gone", Class: storage.ClassBulk})
	def, _ := JobDefinition(failing, func() time.Time { return fixedJobTime().Add(time.Hour) })
	runFails(t, def)
	if got := listRuns(t, dest); len(got) != 1 || got[0] != "20261008T120000Z" {
		t.Fatalf("runs after interrupted backup = %v", got)
	}
	runs, _ := ListRuns(dest, "photos")
	if len(runs) != 1 {
		t.Fatal("last good run lost")
	}
}

func TestPruneCountsOnlyCompleteRuns(t *testing.T) {
	dest := t.TempDir()
	mk := func(stamp string, complete bool) {
		dir := filepath.Join(dest, "photos", stamp)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if complete {
			writeFile(t, filepath.Join(dir, "backup.json"), `{"package":"photos"}`)
		}
	}
	mk("20261001T000000Z", true)  // old good
	mk("20261002T000000Z", false) // partial, between goods
	mk("20261003T000000Z", true)  // good
	mk("20261004T000000Z", false) // partial newer than... the newest good below
	mk("20261005T000000Z", true)  // current
	mk("20261006T000000Z", false) // newer than every good: may still be writing
	job := Job{Package: "photos", DestRoot: dest, Retain: 2}
	if err := prune(job, "20261005T000000Z"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(listRuns(t, dest), " ")
	want := "20261003T000000Z 20261005T000000Z 20261006T000000Z"
	if got != want {
		t.Fatalf("runs = %q, want %q", got, want)
	}
}

func TestManifestWrittenAtomically(t *testing.T) {
	keys := newKeys()
	recipients, _ := Recipients(keys)
	mount, dest := t.TempDir(), t.TempDir()
	job := scheduleJob(t, mount, dest)
	job.Recipients = recipients
	stubExec(t, func(_, _ string, _ []string) (string, error) { return "ok", nil })
	def, _ := JobDefinition(job, fixedJobTime)
	runDef(t, def)
	entries, _ := os.ReadDir(filepath.Join(dest, "photos", "20261008T120000Z"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".backup-manifest-") {
			t.Fatalf("temp manifest left: %s", e.Name())
		}
	}
}
