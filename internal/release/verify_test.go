package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Signer is a test minisign signer producing the real file layouts.
type testSigner struct {
	id   [8]byte
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newTestSigner(t *testing.T) *testSigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := &testSigner{pub: pub, priv: priv}
	_, _ = rand.Read(s.id[:])
	return s
}

func (s *testSigner) pubText() string {
	b := append([]byte("Ed"), s.id[:]...)
	b = append(b, s.pub...)
	return "untrusted comment: minisign public key\n" + base64.StdEncoding.EncodeToString(b) + "\n"
}

func (s *testSigner) parsed(t *testing.T) PublicKey {
	t.Helper()
	k, err := ParsePublicKey(s.pubText())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func (s *testSigner) sign(msg []byte, algo string) string {
	payload := msg
	sig := ed25519.Sign(s.priv, payload)
	raw := append([]byte(algo), s.id[:]...)
	raw = append(raw, sig...)
	comment := "timestamp:1"
	global := ed25519.Sign(s.priv, append(append([]byte(nil), sig...), []byte(comment)...))
	return "untrusted comment: signature\n" + base64.StdEncoding.EncodeToString(raw) +
		"\ntrusted comment: " + comment + "\n" + base64.StdEncoding.EncodeToString(global) + "\n"
}

type fixture struct {
	dir                          string
	artifact, checksums, sigFile string
}

func (s *testSigner) fixture(t *testing.T, version string, counter int64) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{dir: dir, artifact: filepath.Join(dir, "homestead-server"),
		checksums: filepath.Join(dir, "SHA256SUMS"), sigFile: filepath.Join(dir, "SHA256SUMS.minisig")}
	body := []byte("binary-contents")
	sum := sha256.Sum256(body)
	sums := fmt.Sprintf("version=%s\ncounter=%d\n%s  homestead-server\n", version, counter, hex.EncodeToString(sum[:]))
	for path, data := range map[string][]byte{f.artifact: body, f.checksums: []byte(sums), f.sigFile: []byte(s.sign([]byte(sums), "Ed"))} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f fixture) inputs(keys ...PublicKey) Inputs {
	return Inputs{ArtifactPath: f.artifact, ChecksumsPath: f.checksums, SignaturePath: f.sigFile, Trusted: keys}
}

func TestVerifyHappyPath(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	res, err := Verify(f.inputs(s.parsed(t)))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Version != "0.2.0" || res.Counter != 5 || res.Override {
		t.Fatalf("result = %+v", res)
	}
}

func TestVerifyTamperedArtifact(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	if err := os.WriteFile(f.artifact, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.inputs(s.parsed(t))); !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
}

func TestVerifyTamperedChecksums(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	data, _ := os.ReadFile(f.checksums)
	data = append([]byte("# x\n"), data...)
	if err := os.WriteFile(f.checksums, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.inputs(s.parsed(t))); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyTamperedTrustedComment(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	data, _ := os.ReadFile(f.sigFile)
	text := string(data)
	text = text[:len("untrusted comment: signature\n")] + text[len("untrusted comment: signature\n"):]
	text = replaceOnce(text, "timestamp:1", "timestamp:2")
	if err := os.WriteFile(f.sigFile, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.inputs(s.parsed(t))); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func replaceOnce(s, old, repl string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + repl + s[i+len(old):]
		}
	}
	return s
}

func TestVerifyUnknownKey(t *testing.T) {
	signer, other := newTestSigner(t), newTestSigner(t)
	f := signer.fixture(t, "0.2.0", 5)
	if _, err := Verify(f.inputs(other.parsed(t))); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", err)
	}
	// An owner-added key then verifies the same files.
	if _, err := Verify(f.inputs(other.parsed(t), signer.parsed(t))); err != nil {
		t.Fatalf("owner-trusted key: %v", err)
	}
}

func TestVerifyForgedKeyIDRejected(t *testing.T) {
	signer, other := newTestSigner(t), newTestSigner(t)
	other.id = signer.id // same key id, different key material
	f := signer.fixture(t, "0.2.0", 5)
	if _, err := Verify(f.inputs(other.parsed(t))); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestPrehashedSignatureRejected(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	data, _ := os.ReadFile(f.checksums)
	if err := os.WriteFile(f.sigFile, []byte(s.sign(data, "ED")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.inputs(s.parsed(t))); !errors.Is(err, ErrPrehashed) {
		t.Fatalf("err = %v, want ErrPrehashed", err)
	}
}

func TestChecksumsRequireVersionAndCounter(t *testing.T) {
	digest := "0000000000000000000000000000000000000000000000000000000000000000"
	for name, text := range map[string]string{
		"no counter":   "version=1.0.0\n" + digest + "  a\n",
		"no version":   "counter=1\n" + digest + "  a\n",
		"zero counter": "version=1.0.0\ncounter=0\n" + digest + "  a\n",
		"bad digest":   "version=1.0.0\ncounter=1\nzz  a\n",
		"dup counter":  "version=1.0.0\ncounter=1\ncounter=2\n" + digest + "  a\n",
		"dup file":     "version=1.0.0\ncounter=1\n" + digest + "  a\n" + digest + "  a\n",
	} {
		if _, err := ParseChecksums([]byte(text)); !errors.Is(err, ErrBadChecksums) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRollbackProtection(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.1.0", 3)
	in := f.inputs(s.parsed(t))
	in.Installed = Installed{Version: "0.2.0", Counter: 5}
	res, err := Verify(in)
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("err = %v, want ErrRollback", err)
	}
	if res.Version != "0.1.0" || res.Counter != 3 {
		t.Fatalf("rollback result lacks versions: %+v", res)
	}
	in.AllowOlder = true
	res, err = Verify(in)
	if err != nil || !res.Override {
		t.Fatalf("override: res=%+v err=%v", res, err)
	}
	// Equal counter, different version: refused even with override.
	g := s.fixture(t, "0.9.9", 5)
	in = g.inputs(s.parsed(t))
	in.Installed = Installed{Version: "0.2.0", Counter: 5}
	in.AllowOlder = true
	if _, err := Verify(in); !errors.Is(err, ErrCounterConflict) {
		t.Fatalf("err = %v, want ErrCounterConflict", err)
	}
	// Equal counter, same version (reinstall) passes; newer passes.
	in.Installed = Installed{Version: "0.9.9", Counter: 5}
	if _, err := Verify(in); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	in.Installed = Installed{Version: "0.1.0", Counter: 1}
	if _, err := Verify(in); err != nil {
		t.Fatalf("newer: %v", err)
	}
}

func TestAuthFailureNotOverridable(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	if err := os.WriteFile(f.artifact, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := f.inputs(s.parsed(t))
	in.AllowOlder = true
	if _, err := Verify(in); !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmbeddedKeyParses(t *testing.T) {
	if _, err := ParsePublicKey(EmbeddedPublicKey); err != nil {
		t.Fatalf("embedded key: %v", err)
	}
}
