package catalog

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
)

func indexOf(calls []string, want string, from int) int {
	for i := from; i < len(calls); i++ {
		if calls[i] == want {
			return i
		}
	}
	return -1
}

// A failure after cutover must stop the new stack first and never
// delete what it wrote: the migrated tree (including writes made after
// cutover) is moved aside next to the target.
func TestPostCutoverRollbackMovesDataAside(t *testing.T) {
	f := newNextcloudFixture(t)
	opts, audits := f.options(func(context.Context) error { return nil })
	prior := composeUp
	calls := 0
	composeUp = func(ctx context.Context, dir, project string) error {
		calls++
		if calls > 1 {
			return prior(ctx, dir, project)
		}
		_ = f.engine.call("up")
		f.engine.setState("running", f.engine.newMounts)
		writeTree(t, filepath.Join(f.base, "nextcloud", "data"), map[string]string{"posts/new.txt": "written after cutover"})
		return errors.New("port in use")
	}
	done := f.run(opts)
	if done.Status != operation.StatusFailed || !strings.Contains(done.Error, `step "up"`) {
		t.Fatalf("status = %s err = %s", done.Status, done.Error)
	}
	if strings.Contains(done.Error, "ROLLBACK FAILED") {
		t.Fatalf("rollback failed: %s", done.Error)
	}
	first := indexOf(f.engine.calls, "up", 0)
	if first < 0 || indexOf(f.engine.calls, "stop", first) < 0 {
		t.Fatalf("new stack was not stopped before rollback: %v", f.engine.calls)
	}
	aside, _ := filepath.Glob(filepath.Join(f.base, "nextcloud", "data.rollback-*"))
	if len(aside) != 1 {
		t.Fatalf("moved-aside data = %v", aside)
	}
	for _, name := range []string{"alice/file.txt", "posts/new.txt"} {
		if _, err := os.Stat(filepath.Join(aside[0], name)); err != nil {
			t.Fatalf("moved-aside data lost %s: %v", name, err)
		}
	}
	if !strings.Contains(done.Error, aside[0]) || len(*audits) != 1 || !strings.Contains((*audits)[0], aside[0]) {
		t.Fatalf("moved-aside path not reported: %s / %v", done.Error, *audits)
	}
	if _, err := os.Stat(filepath.Join(f.vol, "data", "alice", "file.txt")); err != nil {
		t.Fatalf("original volume damaged: %v", err)
	}
	if !strings.Contains(f.composeText(), "source: v1") {
		t.Fatalf("stack not pinned to the original volume:\n%s", f.composeText())
	}
}

// Bookkeeping after a healthy migration never rolls it back.
func TestRecordFailureKeepsCommittedMigration(t *testing.T) {
	f := newNextcloudFixture(t)
	oldRecord, oldDelay := recordOldVolumes, recordRetryDelay
	attempts := 0
	recordOldVolumes = func(*store.Store, string, []OldVolume, string, time.Time) error {
		attempts++
		return errors.New("database is locked")
	}
	recordRetryDelay = time.Millisecond
	t.Cleanup(func() { recordOldVolumes, recordRetryDelay = oldRecord, oldDelay })
	opts, audits := f.options(func(context.Context) error { return nil })
	done := f.run(opts)
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %s: %s", done.Status, done.Error)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d", attempts)
	}
	if !hasDeclaredBinds(f.dir, f.manifest) {
		t.Fatal("migrated stack was rolled back")
	}
	if len(*audits) != 1 || !strings.HasPrefix((*audits)[0], "warning:") || !strings.Contains((*audits)[0], "NOT eligible") {
		t.Fatalf("audits = %v", *audits)
	}
	if old, _ := ListOldVolumes(f.st, "nextcloud"); len(old) != 0 {
		t.Fatalf("old = %+v", old)
	}
}

func TestVerifyCopyComparesAttributesAndContent(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	dst := filepath.Join(t.TempDir(), "dst")
	writeTree(t, src, map[string]string{"a.txt": "hello", "sub/b.txt": "world"})
	if err := os.Symlink("a.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tarCopy(context.Background(), src, dst, nil); err != nil {
		t.Fatalf("tarCopy: %v", err)
	}
	item := MigrationItem{Source: src, Dest: dst}
	if err := verifyCopy(item); err != nil {
		t.Fatalf("faithful copy rejected: %v", err)
	}
	mutations := map[string]func(t *testing.T){
		"same-size corruption": func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dst, "a.txt"), []byte("hellO"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"mode": func(t *testing.T) {
			if err := os.Chmod(filepath.Join(dst, "sub", "b.txt"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"symlink target": func(t *testing.T) {
			_ = os.Remove(filepath.Join(dst, "link"))
			if err := os.Symlink("sub/b.txt", filepath.Join(dst, "link")); err != nil {
				t.Fatal(err)
			}
		},
		"missing file": func(t *testing.T) {
			if err := os.Remove(filepath.Join(dst, "sub", "b.txt")); err != nil {
				t.Fatal(err)
			}
		},
		"extra file": func(t *testing.T) {
			writeTree(t, dst, map[string]string{"extra": "x"})
		},
	}
	if os.Geteuid() == 0 {
		mutations["owner"] = func(t *testing.T) {
			if err := os.Lchown(filepath.Join(dst, "a.txt"), 4242, 4242); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := os.RemoveAll(dst); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dst, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := tarCopy(context.Background(), src, dst, nil); err != nil {
				t.Fatal(err)
			}
			mutate(t)
			if err := verifyCopy(item); err == nil {
				t.Fatalf("%s went undetected", name)
			}
		})
	}
}

func TestMissingSourceNeverPassesVacuously(t *testing.T) {
	dst := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	if err := tarCopy(context.Background(), missing, dst, nil); err == nil {
		t.Fatal("tarCopy accepted a missing source")
	}
	if err := verifyCopy(MigrationItem{Source: missing, Dest: dst}); err == nil {
		t.Fatal("verifyCopy accepted a missing volume root")
	}
	if err := verifyCopy(MigrationItem{Source: missing, Dest: dst, Nested: true}); err != nil {
		t.Fatalf("an absent nested path is empty, not an error: %v", err)
	}
	// And inside a migration: the volume vanishes after the stop.
	f := newNextcloudFixture(t)
	oldStop := composeStop
	composeStop = func(ctx context.Context, dir, project string) error {
		err := oldStop(ctx, dir, project)
		_ = os.RemoveAll(f.vol)
		return err
	}
	opts, _ := f.options(func(context.Context) error { return nil })
	done := f.run(opts)
	if done.Status != operation.StatusFailed || !strings.Contains(done.Error, `step "copy:nextcloud:/var/www/html"`) {
		t.Fatalf("status = %s err = %s", done.Status, done.Error)
	}
}

func TestWriteStackFailureRestoresOriginal(t *testing.T) {
	f := newNextcloudFixture(t)
	old := writeStackFile
	calls := 0
	writeStackFile = func(name string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 1 {
			// Simulate a torn write: part of the new file lands, then it fails.
			_ = os.WriteFile(name, data[:10], perm)
			return errors.New("no space left on device")
		}
		return old(name, data, perm)
	}
	t.Cleanup(func() { writeStackFile = old })
	opts, _ := f.options(func(context.Context) error { return nil })
	done := f.run(opts)
	if done.Status != operation.StatusFailed || !strings.Contains(done.Error, `step "write-stack"`) {
		t.Fatalf("status = %s err = %s", done.Status, done.Error)
	}
	if f.composeText() != f.original {
		t.Fatalf("original compose.yaml not restored:\n%s", f.composeText())
	}
	containers, _ := projectContainers(context.Background(), "nextcloud")
	if len(containers) != 1 || containers[0].State != "running" {
		t.Fatalf("app not running: %+v", containers)
	}
}

func TestConcurrentMigrationRefused(t *testing.T) {
	f := newNextcloudFixture(t)
	opts, _ := f.options(func(context.Context) error { return nil })
	first, err := MigrateDefinition(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !MigrationActive("nextcloud") || RefuseDuringMigration("nextcloud") == nil {
		t.Fatal("claim not visible to other operations")
	}
	if _, err := MigrateDefinition(opts); !errors.Is(err, ErrMigrationActive) {
		t.Fatalf("second approval err = %v", err)
	}
	if _, err := ConfigureDefinition(ConfigPlan{Manifest: f.manifest}, f.dir, "nextcloud", f.st, nil); err == nil {
		t.Fatal("configure ran during a migration")
	}
	first.Cleanup()
	first.Cleanup() // idempotent
	if MigrationActive("nextcloud") {
		t.Fatal("claim not released")
	}
	again, err := MigrateDefinition(opts)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again.Cleanup()
}

// The container stack is re-checked right before copying.
func TestStackRestartedBeforeCopyIsCaught(t *testing.T) {
	f := newNextcloudFixture(t)
	opts, _ := f.options(func(context.Context) error {
		// A long backup, during which something restarts the stack.
		f.engine.setState("running", nil)
		return nil
	})
	done := f.run(opts)
	if done.Status != operation.StatusFailed || !strings.Contains(done.Error, `step "verify-stopped:before-copy"`) {
		t.Fatalf("status = %s err = %s", done.Status, done.Error)
	}
}

// Real tar over the hard cases: links must not be followed out of the
// volume, hardlinks stay linked, FIFOs survive, sockets are skipped,
// xattrs and ownership carry over where the platform allows.
func TestTarCopySpecialFiles(t *testing.T) {
	if out, err := exec.Command("tar", "--version").Output(); err != nil || !strings.Contains(string(out), "GNU tar") {
		t.Skip("GNU tar not available")
	}
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	outside := filepath.Join(root, "outside")
	writeTree(t, src, map[string]string{"data/file.txt": "payload", "data/hard1": "same inode"})
	writeTree(t, outside, map[string]string{"secret.txt": "must not be copied"})
	for name, target := range map[string]string{"abs-link": outside, "rel-link": "../outside", "dangling": "/nonexistent/x"} {
		if err := os.Symlink(target, filepath.Join(src, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(src, "data", "hard1"), filepath.Join(src, "data", "hard2")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o640); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(src, "s.sock"))
	if err == nil {
		defer listener.Close()
	}
	if err := os.Chmod(filepath.Join(src, "data", "file.txt"), 0o640); err != nil {
		t.Fatal(err)
	}
	xattrs := syscall.Setxattr(filepath.Join(src, "data", "file.txt"), "user.homestead.test", []byte("v1"), 0) == nil
	owned := false
	if os.Geteuid() == 0 {
		owned = os.Lchown(filepath.Join(src, "data", "file.txt"), 1234, 5678) == nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tarCopy(context.Background(), src, dst, nil); err != nil {
		t.Fatalf("tarCopy: %v", err)
	}
	if err := verifyCopy(MigrationItem{Source: src, Dest: dst}); err != nil {
		t.Fatalf("verifyCopy: %v", err)
	}
	// Symlinks are copied as links, never followed.
	for name, target := range map[string]string{"abs-link": outside, "rel-link": "../outside", "dangling": "/nonexistent/x"} {
		got, err := os.Readlink(filepath.Join(dst, name))
		if err != nil || got != target {
			t.Fatalf("%s: target %q err %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "secret.txt")); err == nil {
		t.Fatal("content outside the volume was copied in")
	}
	// Hardlinks remain one inode.
	var a, b syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(dst, "data", "hard1"), &a); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Lstat(filepath.Join(dst, "data", "hard2"), &b); err != nil || a.Ino != b.Ino {
		t.Fatalf("hardlink split: %v %v", a.Ino, b.Ino)
	}
	// FIFO preserved; socket skipped.
	info, err := os.Lstat(filepath.Join(dst, "pipe"))
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("fifo: %v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "s.sock")); err == nil {
		t.Fatal("socket copied")
	}
	if xattrs {
		buf := make([]byte, 8)
		n, err := syscall.Getxattr(filepath.Join(dst, "data", "file.txt"), "user.homestead.test", buf)
		if err != nil || string(buf[:n]) != "v1" {
			t.Fatalf("xattr lost: %q %v", buf[:max(n, 0)], err)
		}
	} else {
		t.Log("xattrs unsupported on the test filesystem; skipped")
	}
	if owned {
		var st syscall.Stat_t
		if err := syscall.Lstat(filepath.Join(dst, "data", "file.txt"), &st); err != nil || st.Uid != 1234 || st.Gid != 5678 {
			t.Fatalf("ownership lost: %+v %v", st, err)
		}
	} else {
		t.Log("not root; ownership check skipped")
	}
}

// Excludes are literal and anchored: a same-named entry deeper in the
// tree and glob characters in the name are untouched.
func TestTarCopyExcludesAreAnchoredAndLiteral(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	writeTree(t, src, map[string]string{"data/x": "skip", "keep/data/y": "keep", "d*": "star-file", "daa": "other"})
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tarCopy(context.Background(), src, dst, []string{"data", "d*"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"data/x": false, "d*": false, "keep/data/y": true, "daa": true} {
		_, err := os.Lstat(filepath.Join(dst, name))
		if (err == nil) != want {
			t.Fatalf("%s present = %v, want %v", name, err == nil, want)
		}
	}
}

// The engine can report a container as running for a moment after
// compose stop returns; verify-stopped re-reads for a bounded time, but
// a container that keeps running still fails the step.
func TestVerifyStoppedWaitsForStateToSettle(t *testing.T) {
	f := newNextcloudFixture(t)
	reads := 0
	projectContainers = func(context.Context, string) ([]docker.Container, error) {
		reads++
		state := "exited"
		if reads < 3 {
			state = "running"
		}
		return []docker.Container{{Names: []string{"nextcloud-nextcloud"}, State: state}}, nil
	}
	run := &migrationRun{projectName: "nextcloud"}
	if err := run.verifyStopped(context.Background()); err != nil {
		t.Fatalf("settled container refused: %v", err)
	}
	if reads != 3 {
		t.Fatalf("reads = %d, want 3", reads)
	}
	projectContainers = func(context.Context, string) ([]docker.Container, error) {
		return []docker.Container{{Names: []string{"nextcloud-nextcloud"}, State: "restarting"}}, nil
	}
	err := run.verifyStopped(context.Background())
	if err == nil || !strings.Contains(err.Error(), "still restarting") {
		t.Fatalf("a container that keeps running passed: %v", err)
	}
	_ = f
}
