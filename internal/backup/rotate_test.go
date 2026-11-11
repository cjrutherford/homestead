package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"homestead/internal/secret"
)

type rotKeys map[string]string

func (k rotKeys) Get(n string) (string, error) {
	if v, ok := k[n]; ok {
		return v, nil
	}
	return "", secret.ErrNotFound
}
func (k rotKeys) SetSystem(n, v string) error { k[n] = v; return nil }

func writeAge(t *testing.T, path string, data []byte, recipients ...age.Recipient) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := encryptBytes(path, data, recipients); err != nil {
		t.Fatal(err)
	}
}

func readAge(t *testing.T, path string, id age.Identity) ([]byte, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := age.Decrypt(f, id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// runRewrap executes a re-wrap definition's steps in order, as the
// runner would, and returns the summary it reported and the first error.
func runRewrap(t *testing.T, keys KeyStore, root string) (RewrapSummary, error) {
	t.Helper()
	var sum RewrapSummary
	def, err := RewrapDefinition(keys, root, func(s RewrapSummary) { sum = s })
	if err != nil {
		return sum, err
	}
	defer def.Cleanup()
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			return sum, err
		}
	}
	return sum, nil
}

func TestRotateMintsWithoutTouchingFilesAndRewrapConvertsThem(t *testing.T) {
	keys := rotKeys{}
	old, _ := ApplianceIdentity(keys)
	owner, _ := age.GenerateX25519Identity()
	root := t.TempDir()
	big := bytes.Repeat([]byte("homestead payload "), 20000) // multiple age chunks
	a := filepath.Join(root, "pkg", "run1", "data.tar.age")
	b := filepath.Join(root, "pkg", "run1", "db.age")
	writeAge(t, a, big, old.Recipient(), owner.Recipient())
	writeAge(t, b, []byte("small"), old.Recipient())
	if err := os.WriteFile(filepath.Join(root, "pkg", "run1", "backup.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(a)
	beforeStat, _ := os.Stat(a)

	res, err := RotateIdentity(keys)
	if err != nil {
		t.Fatal(err)
	}
	if res.OldFingerprint != Fingerprint(old.Recipient().String()) {
		t.Fatalf("result = %+v", res)
	}
	if after, _ := os.ReadFile(a); !bytes.Equal(before, after) {
		t.Fatal("minting touched a backup file")
	}
	cur, ok, _ := PeekApplianceIdentity(keys)
	if !ok || cur.String() != res.New.String() || cur.String() == old.String() {
		t.Fatal("the stored identity is not the new one")
	}
	if _, err := readAge(t, a, res.New); err == nil {
		t.Fatal("the new key opens a run before the re-wrap")
	}

	sum, err := runRewrap(t, keys, root)
	if err != nil || sum.Rewrapped != 2 || sum.FailedCount != 0 || !sum.Complete || sum.Fingerprint != Fingerprint(res.New.Recipient().String()) {
		t.Fatalf("rewrap = %+v %v", sum, err)
	}
	for _, id := range []age.Identity{res.New, old, owner} {
		got, err := readAge(t, a, id)
		if err != nil || !bytes.Equal(got, big) {
			t.Fatalf("data.tar.age with %T: %v", id, err)
		}
	}
	if got, err := readAge(t, b, res.New); err != nil || string(got) != "small" {
		t.Fatalf("db.age: %v", err)
	}
	if after, _ := os.Stat(a); after.Mode().Perm() != beforeStat.Mode().Perm() {
		t.Fatal("mode changed")
	}
	recipients, _ := Recipients(keys)
	c := filepath.Join(root, "pkg", "run2", "data.age")
	writeAge(t, c, []byte("fresh"), recipients...)
	if _, err := readAge(t, c, old); err == nil {
		t.Fatal("old key reads a run made after rotation")
	}
	if left, _ := filepath.Glob(filepath.Join(root, "pkg", "run1", ".rewrap-*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	ids, _ := Identities(keys)
	if len(ids) != 2 {
		t.Fatalf("identities = %d", len(ids))
	}
	// A second rotation chains retirement; the re-wrap converts all three.
	res2, err := RotateIdentity(keys)
	if err != nil {
		t.Fatal(err)
	}
	if sum, err = runRewrap(t, keys, root); err != nil || sum.Rewrapped != 3 {
		t.Fatalf("second rewrap %+v %v", sum, err)
	}
	if got, err := readAge(t, a, res2.New); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("after two rotations: %v", err)
	}
	if ids, _ = Identities(keys); len(ids) != 3 {
		t.Fatalf("identities = %d", len(ids))
	}
}

func TestRewrapIsIdempotentAndNamesUnreadableFiles(t *testing.T) {
	keys := rotKeys{}
	old, _ := ApplianceIdentity(keys)
	stranger, _ := age.GenerateX25519Identity()
	root := t.TempDir()
	good := filepath.Join(root, "a.age")
	bad := filepath.Join(root, "b.age")
	writeAge(t, good, []byte("x"), old.Recipient())
	writeAge(t, bad, []byte("y"), stranger.Recipient())
	if _, err := RotateIdentity(keys); err != nil {
		t.Fatal(err)
	}
	sum, err := runRewrap(t, keys, root)
	if err == nil || !strings.Contains(err.Error(), "1 files could not be re-wrapped") {
		t.Fatalf("err = %v", err)
	}
	if sum.Rewrapped != 1 || sum.FailedCount != 1 || len(sum.Failed) != 1 || sum.Failed[0] != "b.age" || sum.Complete {
		t.Fatalf("summary = %+v", sum)
	}
	if got, err := readAge(t, bad, stranger); err != nil || string(got) != "y" {
		t.Fatalf("bad file changed: %v", err)
	}
	if sum, _ = runRewrap(t, keys, root); sum.Rewrapped != 0 || sum.AlreadyCurrent != 1 {
		t.Fatalf("again = %+v", sum)
	}
}

func TestRewrapCountsAnUnreadableDirectoryAsFailed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	keys := rotKeys{}
	id, _ := ApplianceIdentity(keys)
	root := t.TempDir()
	writeAge(t, filepath.Join(root, "ok.age"), []byte("x"), id.Recipient())
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	sum, err := runRewrap(t, keys, root)
	if err == nil || sum.FailedCount != 1 || sum.Failed[0] != "locked" || sum.Complete {
		t.Fatalf("summary = %+v err = %v", sum, err)
	}
}

func TestRotateWithoutAnIdentityCreatesOne(t *testing.T) {
	keys := rotKeys{}
	res, err := RotateIdentity(keys)
	if err != nil || res.OldFingerprint != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, ok := keys[RetiredIdentitySecret]; ok {
		t.Fatal("retired entry written with nothing to retire")
	}
	if _, err := RotateIdentity(nil); err == nil {
		t.Fatal("nil store accepted")
	}
}

func TestRotateIsRefusedWhileARunHoldsTheGate(t *testing.T) {
	keys := rotKeys{}
	if _, err := ApplianceIdentity(keys); err != nil {
		t.Fatal(err)
	}
	_, release, err := AcquireRecipients(keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := keys[IdentitySecret]
	if _, err := RotateIdentity(keys); !errors.Is(err, ErrBackupRunning) {
		t.Fatalf("rotate during a run = %v", err)
	}
	if keys[IdentitySecret] != before {
		t.Fatal("a refused rotation changed the key")
	}
	release()
	release() // idempotent
	if _, err := RotateIdentity(keys); err != nil {
		t.Fatalf("rotate after release = %v", err)
	}
}

// A run that resolved its recipients before a rotation finishes encrypted
// to the old key; the gate makes the rotation wait for it rather than
// leave the run unreadable by the new key.
func TestRunStartedBeforeRotationStaysOpenableAfterRewrap(t *testing.T) {
	keys := rotKeys{}
	root := t.TempDir()
	recipients, release, err := AcquireRecipients(keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RotateIdentity(keys); !errors.Is(err, ErrBackupRunning) {
		t.Fatalf("rotate = %v", err)
	}
	run := filepath.Join(root, "pkg", "run1", "data.age")
	writeAge(t, run, []byte("during the run"), recipients...)
	release()
	res, err := RotateIdentity(keys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runRewrap(t, keys, root); err != nil {
		t.Fatal(err)
	}
	if got, err := readAge(t, run, res.New); err != nil || string(got) != "during the run" {
		t.Fatalf("run with the new key: %q %v", got, err)
	}
}

func TestRetiredStoreToleratesAGarbageLineAndKeepsItVerbatim(t *testing.T) {
	keys := rotKeys{}
	first, _ := ApplianceIdentity(keys)
	keys[RetiredIdentitySecret] = "not an identity"
	ids, err := Identities(keys)
	if err != nil || len(ids) != 1 {
		t.Fatalf("Identities = %d %v", len(ids), err)
	}
	retired, bad, err := RetiredIdentityReport(keys)
	if err != nil || len(retired) != 0 || bad != 1 {
		t.Fatalf("report = %d %d %v", len(retired), bad, err)
	}
	if _, err := RotateIdentity(keys); err != nil {
		t.Fatal(err)
	}
	stored := keys[RetiredIdentitySecret]
	if !strings.HasPrefix(stored, "not an identity\n") || !strings.Contains(stored, first.String()) {
		t.Fatal("the bad line was not preserved next to the retired key")
	}
	retired, bad, _ = RetiredIdentityReport(keys)
	if len(retired) != 1 || bad != 1 {
		t.Fatalf("report after = %d %d", len(retired), bad)
	}
	ids, err = Identities(keys)
	if err != nil || len(ids) != 2 {
		t.Fatalf("Identities after = %d %v", len(ids), err)
	}
}

func TestCleanRewrapTempsRemovesOnlyRewrapTemps(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pkg", "run1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := []string{"data.age", "backup.json", ".other-123", "rewrap-notatemp"}
	for _, name := range append([]string{".rewrap-111", ".rewrap-222"}, keep...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A directory that happens to be named like a temp file stays.
	if err := os.Mkdir(filepath.Join(dir, ".rewrap-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	n, err := CleanRewrapTemps(root)
	if err != nil || n != 2 {
		t.Fatalf("removed %d %v", n, err)
	}
	for _, name := range append(keep, ".rewrap-dir") {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s removed: %v", name, err)
		}
	}
	for _, name := range []string{".rewrap-111", ".rewrap-222"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Fatalf("%s survived", name)
		}
	}
}

func TestRewrapCleansLeftoversThenConverts(t *testing.T) {
	keys := rotKeys{}
	old, _ := ApplianceIdentity(keys)
	root := t.TempDir()
	writeAge(t, filepath.Join(root, "a.age"), []byte("x"), old.Recipient())
	stale := filepath.Join(root, ".rewrap-999")
	if err := os.WriteFile(stale, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RotateIdentity(keys); err != nil {
		t.Fatal(err)
	}
	if sum, err := runRewrap(t, keys, root); err != nil || sum.Rewrapped != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("the interrupted re-wrap's temp file survived")
	}
}

func TestSecondRewrapIsRefusedUntilTheFirstEnds(t *testing.T) {
	keys := rotKeys{}
	if _, err := ApplianceIdentity(keys); err != nil {
		t.Fatal(err)
	}
	def, err := RewrapDefinition(keys, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RewrapDefinition(keys, t.TempDir(), nil); !errors.Is(err, ErrRewrapRunning) {
		t.Fatalf("second = %v", err)
	}
	if _, err := RotateIdentity(keys); !errors.Is(err, ErrRewrapRunning) {
		t.Fatalf("rotate during a re-wrap = %v", err)
	}
	def.Cleanup()
	def.Cleanup() // idempotent
	again, err := RewrapDefinition(keys, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("after release = %v", err)
	}
	again.Cleanup()
}

func TestRewrapStopsBetweenFilesWhenCancelled(t *testing.T) {
	keys := rotKeys{}
	old, _ := ApplianceIdentity(keys)
	root := t.TempDir()
	writeAge(t, filepath.Join(root, "a.age"), []byte("x"), old.Recipient())
	fresh, _ := age.GenerateX25519Identity()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sum := RewrapRuns(ctx, root, fresh, []age.Identity{old})
	if sum.Rewrapped != 0 || sum.Complete {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestRewrapSummaryAndDefinitionCarryNoKeyMaterial(t *testing.T) {
	keys := rotKeys{}
	old, _ := ApplianceIdentity(keys)
	root := t.TempDir()
	writeAge(t, filepath.Join(root, "a.age"), []byte("x"), old.Recipient())
	res, _ := RotateIdentity(keys)
	sum, err := runRewrap(t, keys, root)
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(sum.Failed, ",") + sum.Fingerprint; strings.Contains(s, "AGE-SECRET-KEY") || strings.Contains(s, res.New.String()) {
		t.Fatal("summary carries key material")
	}
}

func TestJobDefinitionHoldsTheRunGateUntilCleanup(t *testing.T) {
	keys := rotKeys{}
	if _, err := ApplianceIdentity(keys); err != nil {
		t.Fatal(err)
	}
	mount := t.TempDir()
	job := scheduleJob(t, mount, t.TempDir())
	job.RecipientSource = func() ([]age.Recipient, func(), error) { return AcquireRecipients(keys, nil) }
	def, err := JobDefinition(job, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RotateIdentity(keys); !errors.Is(err, ErrBackupRunning) {
		t.Fatalf("rotate while a definition is planned = %v", err)
	}
	def.Cleanup()
	if _, err := RotateIdentity(keys); err != nil {
		t.Fatalf("rotate after cleanup = %v", err)
	}
}

func TestJobDefinitionReleasesTheGateWhenPlanningFails(t *testing.T) {
	keys := rotKeys{}
	if _, err := ApplianceIdentity(keys); err != nil {
		t.Fatal(err)
	}
	bad := Job{RecipientSource: func() ([]age.Recipient, func(), error) { return AcquireRecipients(keys, nil) }}
	if _, err := JobDefinition(bad, nil); err == nil {
		t.Fatal("an empty job planned")
	}
	if _, err := RotateIdentity(keys); err != nil {
		t.Fatalf("the failed plan leaked the run gate: %v", err)
	}
}
