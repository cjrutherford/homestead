package backup

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
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

func TestRotateRewrapsExistingRunsForTheNewKey(t *testing.T) {
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
	before, _ := os.Stat(a)

	res, err := RotateIdentity(keys, root)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rewrapped != 2 || len(res.Failed) != 0 || res.OldFingerprint != Fingerprint(old.Recipient().String()) {
		t.Fatalf("result = %+v", res)
	}
	cur, ok, _ := PeekApplianceIdentity(keys)
	if !ok || cur.String() != res.New.String() || cur.String() == old.String() {
		t.Fatal("the stored identity is not the new one")
	}
	// Old runs open with the NEW key alone, and still with the old key
	// and the owner's key.
	for _, id := range []age.Identity{res.New, old, owner} {
		got, err := readAge(t, a, id)
		if err != nil || !bytes.Equal(got, big) {
			t.Fatalf("data.tar.age with %T: %v", id, err)
		}
	}
	if got, err := readAge(t, b, res.New); err != nil || string(got) != "small" {
		t.Fatalf("db.age: %v", err)
	}
	if after, _ := os.Stat(a); after.Mode().Perm() != before.Mode().Perm() {
		t.Fatal("mode changed")
	}
	// A new run encrypts to the new key only; the old key cannot read it.
	recipients, _ := Recipients(keys)
	c := filepath.Join(root, "pkg", "run2", "data.age")
	writeAge(t, c, []byte("fresh"), recipients...)
	if _, err := readAge(t, c, old); err == nil {
		t.Fatal("old key reads a run made after rotation")
	}
	if _, err := readAge(t, c, res.New); err != nil {
		t.Fatal(err)
	}
	// No temp file is left behind.
	left, _ := filepath.Glob(filepath.Join(root, "pkg", "run1", ".rewrap-*"))
	if len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	// Restore still sees both, and a second rotation chains retirement.
	ids, _ := Identities(keys)
	if len(ids) != 2 {
		t.Fatalf("identities = %d", len(ids))
	}
	res2, err := RotateIdentity(keys, root)
	if err != nil || res2.Rewrapped != 3 {
		t.Fatalf("second rotation %+v %v", res2, err)
	}
	if got, err := readAge(t, a, res2.New); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("after two rotations: %v", err)
	}
	if ids, _ = Identities(keys); len(ids) != 3 {
		t.Fatalf("identities = %d", len(ids))
	}
}

func TestRotateRewrapIsIdempotentAndReportsUnreadableFiles(t *testing.T) {
	keys := rotKeys{}
	old, _ := ApplianceIdentity(keys)
	stranger, _ := age.GenerateX25519Identity()
	root := t.TempDir()
	good := filepath.Join(root, "a.age")
	bad := filepath.Join(root, "b.age")
	writeAge(t, good, []byte("x"), old.Recipient())
	writeAge(t, bad, []byte("y"), stranger.Recipient())
	res, err := RotateIdentity(keys, root)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rewrapped != 1 || len(res.Failed) != 1 || res.Failed[0] != "b.age" {
		t.Fatalf("result = %+v", res)
	}
	// The unreadable file is untouched.
	if got, err := readAge(t, bad, stranger); err != nil || string(got) != "y" {
		t.Fatalf("bad file changed: %v", err)
	}
	// Re-wrapping again changes nothing.
	again := RotateResult{}
	RewrapRuns(root, res.New, nil, &again)
	if again.Rewrapped != 0 || again.AlreadyCurrent != 1 {
		t.Fatalf("again = %+v", again)
	}
}

func TestRotateWithoutAnIdentityCreatesOne(t *testing.T) {
	keys := rotKeys{}
	res, err := RotateIdentity(keys, "")
	if err != nil || res.OldFingerprint != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, ok := keys[RetiredIdentitySecret]; ok {
		t.Fatal("retired entry written with nothing to retire")
	}
	if _, err := RotateIdentity(nil, ""); err == nil {
		t.Fatal("nil store accepted")
	}
}
