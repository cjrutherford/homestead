package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deleteOnWalk removes victim the first time the walk reaches trigger,
// after the directory was already listed: what a live database does when
// it drops a temp file mid-copy.
func deleteOnWalk(t *testing.T, trigger, victim string) {
	t.Helper()
	done := false
	walkHook = func(path string) {
		if path == trigger && !done {
			done = true
			_ = os.Remove(victim)
		}
	}
	t.Cleanup(func() { walkHook = nil })
}

func vanishFixture(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a-first"), "1")
	writeFile(t, filepath.Join(src, "b-vanishes"), "2")
	writeFile(t, filepath.Join(src, "c-last"), "3")
	return src
}

func TestCopyTreeToleratesVanishedFile(t *testing.T) {
	src := vanishFixture(t)
	deleteOnWalk(t, filepath.Join(src, "a-first"), filepath.Join(src, "b-vanishes"))
	dst := filepath.Join(t.TempDir(), "out")
	n, stats, err := copyTreeOpts(src, dst, nil)
	if err != nil {
		t.Fatalf("copy failed on vanished file: %v", err)
	}
	if n != 2 || stats.Vanished != 1 {
		t.Fatalf("files=%d vanished=%d", n, stats.Vanished)
	}
	if _, err := os.Stat(filepath.Join(dst, "b-vanishes")); err == nil {
		t.Fatal("vanished file appeared in the copy")
	}
}

func TestArchiveTreeToleratesVanishedFile(t *testing.T) {
	keys := newKeys()
	recipients, _ := Recipients(keys)
	src := vanishFixture(t)
	deleteOnWalk(t, filepath.Join(src, "a-first"), filepath.Join(src, "b-vanishes"))
	archive := filepath.Join(t.TempDir(), "x.tar.age")
	n, stats, err := archiveTreeOpts(src, archive, recipients, nil)
	if err != nil {
		t.Fatalf("archive failed on vanished file: %v", err)
	}
	if n != 2 || stats.Vanished != 1 {
		t.Fatalf("files=%d vanished=%d", n, stats.Vanished)
	}
	ids, _ := Identities(keys)
	sig, err := archiveSignature(archive, ids)
	if err != nil {
		t.Fatalf("archive unreadable: %v", err)
	}
	if len(sig) != 2 {
		t.Fatalf("signature = %v", sig)
	}
}

func TestVanishedDirectoryContentsTolerated(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a-first"), "1")
	writeFile(t, filepath.Join(src, "b-dir", "inner"), "2")
	deleteOnWalk(t, filepath.Join(src, "b-dir"), filepath.Join(src, "b-dir", "inner"))
	keys := newKeys()
	recipients, _ := Recipients(keys)
	if _, _, err := archiveTreeOpts(src, filepath.Join(t.TempDir(), "x.tar.age"), recipients, nil); err != nil {
		t.Fatalf("archive: %v", err)
	}
}

func TestExcludedDirectoryKeptButEmpty(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "keep", "f"), "k")
	writeFile(t, filepath.Join(src, "data", "pgdata", "base", "1"), "db")
	if err := os.Chmod(filepath.Join(src, "data", "pgdata"), 0o700); err != nil {
		t.Fatal(err)
	}
	exclude := []string{"data/pgdata"}

	dst := filepath.Join(t.TempDir(), "plain")
	if _, _, err := copyTreeOpts(src, dst, exclude); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dst, "data", "pgdata"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("excluded dir not kept with mode: %v %v", info, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dst, "data", "pgdata"))
	if len(entries) != 0 {
		t.Fatalf("excluded dir has contents: %v", entries)
	}
	if readFile(t, filepath.Join(dst, "keep", "f")) != "k" {
		t.Fatal("kept file missing")
	}

	keys := newKeys()
	recipients, _ := Recipients(keys)
	archive := filepath.Join(t.TempDir(), "x.tar.age")
	if _, _, err := archiveTreeOpts(src, archive, recipients, exclude); err != nil {
		t.Fatal(err)
	}
	ids, _ := Identities(keys)
	sig, _ := archiveSignature(archive, ids)
	for name := range sig {
		if strings.HasPrefix(name, "data/pgdata") {
			t.Fatalf("archive holds excluded file %q", name)
		}
	}
	if len(sig) != 1 {
		t.Fatalf("signature = %v", sig)
	}
}

func TestSourceValidateRejectsBadExclude(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../x", "/abs"} {
		s := Source{Mountpoint: "/m", Path: "p", Exclude: []string{bad}}
		if s.Validate() == nil {
			t.Fatalf("exclude %q accepted", bad)
		}
	}
}

func TestJobManifestRecordsVanishedWarning(t *testing.T) {
	mount, dest := t.TempDir(), t.TempDir()
	job := scheduleJob(t, mount, dest)
	cfg := filepath.Join(mount, "photos", "config")
	writeFile(t, filepath.Join(cfg, "a-first"), "1")
	writeFile(t, filepath.Join(cfg, "b-vanishes"), "2")
	deleteOnWalk(t, filepath.Join(cfg, "a-first"), filepath.Join(cfg, "b-vanishes"))
	stubExec(t, func(_, _ string, _ []string) (string, error) { return "ok", nil })
	def, err := JobDefinition(job, fixedJobTime)
	if err != nil {
		t.Fatal(err)
	}
	runDef(t, def)
	raw := readFile(t, filepath.Join(dest, "photos", "20261008T120000Z", "backup.json"))
	if !strings.Contains(raw, "vanished while copying") {
		t.Fatalf("manifest lacks vanished warning:\n%s", raw)
	}
}

func TestDestinationRemovedMidCopyFailsRun(t *testing.T) {
	src := vanishFixture(t)
	dst := filepath.Join(t.TempDir(), "out")
	done := false
	walkHook = func(path string) {
		if path == filepath.Join(src, "b-vanishes") && !done {
			done = true
			_ = os.RemoveAll(dst)
		}
	}
	t.Cleanup(func() { walkHook = nil })
	_, stats, err := copyTreeOpts(src, dst, nil)
	if err == nil {
		t.Fatalf("copy succeeded with the destination removed (vanished=%d)", stats.Vanished)
	}
	if stats.Vanished != 0 {
		t.Fatalf("destination loss counted as vanished: %d", stats.Vanished)
	}
}
