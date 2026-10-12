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
	"strings"
	"syscall"
	"testing"
	"time"
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

func writeSig(t *testing.T, f fixture, text string) {
	t.Helper()
	if err := os.WriteFile(f.sigFile, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A prehashed signature naming a different key id is refused as
// prehashed before key lookup, never as an unknown key or a pass.
func TestPrehashedWithMismatchedKeyID(t *testing.T) {
	s, other := newTestSigner(t), newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	data, _ := os.ReadFile(f.checksums)
	writeSig(t, f, other.sign(data, "ED"))
	if _, err := Verify(f.inputs(s.parsed(t), other.parsed(t))); !errors.Is(err, ErrPrehashed) {
		t.Fatalf("err = %v, want ErrPrehashed", err)
	}
}

// Corrupting only the global signature (the file signature is intact)
// fails verification.
func TestTamperedGlobalSignatureAlone(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	data, _ := os.ReadFile(f.sigFile)
	ls := lines(string(data))
	global, _ := base64.StdEncoding.DecodeString(ls[3])
	global[0] ^= 0xff
	ls[3] = base64.StdEncoding.EncodeToString(global)
	writeSig(t, f, strings.Join(ls, "\n")+"\n")
	if _, err := Verify(f.inputs(s.parsed(t))); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// The untrusted comment is unsigned by design: changing it is accepted.
func TestUntrustedCommentTamperAccepted(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	data, _ := os.ReadFile(f.sigFile)
	writeSig(t, f, strings.Replace(string(data), "untrusted comment: signature", "untrusted comment: anything at all", 1))
	if _, err := Verify(f.inputs(s.parsed(t))); err != nil {
		t.Fatalf("untrusted comment change must not matter: %v", err)
	}
}

// CRLF line endings in the signature file are tolerated; in the signed
// checksums file they change the bytes, so the signature fails, and a
// file re-signed with CRLF verifies and parses.
func TestCRLFHandling(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	data, _ := os.ReadFile(f.sigFile)
	writeSig(t, f, strings.ReplaceAll(string(data), "\n", "\r\n"))
	if _, err := Verify(f.inputs(s.parsed(t))); err != nil {
		t.Fatalf("CRLF signature file: %v", err)
	}
	sums, _ := os.ReadFile(f.checksums)
	crlf := []byte(strings.ReplaceAll(string(sums), "\n", "\r\n"))
	if err := os.WriteFile(f.checksums, crlf, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.inputs(s.parsed(t))); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("CRLF rewrite of signed file: err = %v, want ErrBadSignature", err)
	}
	writeSig(t, f, s.sign(crlf, "Ed"))
	if _, err := Verify(f.inputs(s.parsed(t))); err != nil {
		t.Fatalf("CRLF checksums signed as-is: %v", err)
	}
}

func TestMalformedBase64(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	data, _ := os.ReadFile(f.sigFile)
	ls := lines(string(data))
	for name, mutate := range map[string]func([]string){
		"signature line": func(l []string) { l[1] = "!!!not-base64!!!" },
		"global line":    func(l []string) { l[3] = "%%%" },
		"short global":   func(l []string) { l[3] = base64.StdEncoding.EncodeToString([]byte("short")) },
	} {
		cp := append([]string(nil), ls...)
		mutate(cp)
		if _, err := ParseSignature(strings.Join(cp, "\n")); !errors.Is(err, ErrInvalidSig) {
			t.Errorf("%s: err = %v, want ErrInvalidSig", name, err)
		}
	}
	for name, text := range map[string]string{
		"key not base64": "untrusted comment: k\n$$$$\n",
		"key truncated":  "RWQ=\n",
	} {
		if _, err := ParsePublicKey(text); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("%s: err = %v, want ErrInvalidKey", name, err)
		}
	}
}

func TestChecksumsCountersAndNames(t *testing.T) {
	digest := strings.Repeat("0", 64)
	for name, text := range map[string]string{
		"negative counter":  "version=1\ncounter=-3\n" + digest + "  a\n",
		"overflow counter":  "version=1\ncounter=99999999999999999999\n" + digest + "  a\n",
		"float counter":     "version=1\ncounter=1.5\n" + digest + "  a\n",
		"dup empty version": "version=\nversion=1\ncounter=1\n" + digest + "  a\n",
		"empty version":     "version=\ncounter=1\n" + digest + "  a\n",
		"slash name":        "version=1\ncounter=1\n" + digest + "  dir/a\n",
		"dotdot name":       "version=1\ncounter=1\n" + digest + "  ../a\n",
		"backslash name":    "version=1\ncounter=1\n" + digest + "  ..\\a\n",
		"absolute name":     "version=1\ncounter=1\n" + digest + "  /etc/passwd\n",
		"star-path name":    "version=1\ncounter=1\n" + digest + " */tmp/a\n",
		"overlong line":     "version=1\ncounter=1\n" + strings.Repeat("x", maxChecksumLine+10) + "\n",
	} {
		if _, err := ParseChecksums([]byte(text)); !errors.Is(err, ErrBadChecksums) {
			t.Errorf("%s: err = %v, want ErrBadChecksums", name, err)
		}
	}
	if _, err := ParseChecksums([]byte("version=1\ncounter=9223372036854775807\n" + digest + " *a\n")); err != nil {
		t.Fatalf("max int64 counter and binary-mode name: %v", err)
	}
}

func TestChecksumAbsent(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	other := filepath.Join(f.dir, "unlisted")
	if err := os.WriteFile(other, []byte("binary-contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := f.inputs(s.parsed(t))
	in.ArtifactPath = other
	if _, err := Verify(in); !errors.Is(err, ErrChecksumAbsent) {
		t.Fatalf("err = %v, want ErrChecksumAbsent", err)
	}
}

func TestVerifyRefusesNonRegularFiles(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	key := s.parsed(t)

	// mk builds a special file at <dir>/<base>; the artifact case needs
	// the listed base name so the refusal comes from the file type.
	mk := map[string]func(dir, base string) string{
		"fifo": func(dir, base string) string {
			p := filepath.Join(dir, base)
			if err := syscall.Mkfifo(p, 0o644); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
			return p
		},
		"symlink": func(dir, base string) string {
			p := filepath.Join(dir, base)
			if err := os.Symlink(f.artifact, p); err != nil {
				t.Fatal(err)
			}
			return p
		},
		"directory": func(dir, base string) string {
			p := filepath.Join(dir, base)
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
			return p
		},
		"device": func(dir, base string) string {
			p := filepath.Join(dir, base)
			if err := os.Symlink("/dev/null", p); err != nil {
				t.Fatal(err)
			}
			return p
		},
		"missing": func(dir, base string) string { return filepath.Join(dir, base) },
	}
	for name, build := range mk {
		for field, mutate := range map[string]func(*Inputs, string){
			"artifact":  func(in *Inputs, p string) { in.ArtifactPath = p },
			"checksums": func(in *Inputs, p string) { in.ChecksumsPath = p },
			"signature": func(in *Inputs, p string) { in.SignaturePath = p },
		} {
			base := "SPECIAL"
			if field == "artifact" {
				base = "homestead-server"
			}
			dir := t.TempDir()
			path := build(dir, base)
			in := f.inputs(key)
			mutate(&in, path)
			done := make(chan error, 1)
			go func() { _, err := Verify(in); done <- err }()
			select {
			case err := <-done:
				if !errors.Is(err, ErrNotRegularFile) {
					t.Errorf("%s as %s: err = %v, want ErrNotRegularFile", name, field, err)
				} else if strings.Contains(err.Error(), dir) {
					t.Errorf("%s as %s: error echoes the path: %v", name, field, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s as %s: Verify blocked", name, field)
			}
		}
	}
	// A true device file (not a symlink) is refused too.
	if info, err := os.Stat("/dev/null"); err == nil && info.Mode()&os.ModeDevice != 0 {
		in := f.inputs(key)
		in.SignaturePath = "/dev/null"
		if _, err := Verify(in); !errors.Is(err, ErrNotRegularFile) {
			t.Errorf("/dev/null: err = %v", err)
		}
	}
}

func TestVerifyEnforcesSizeLimits(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	big := filepath.Join(f.dir, "big")
	file, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxSmallFileBytes + 1); err != nil {
		t.Skipf("sparse files unavailable: %v", err)
	}
	file.Close()
	in := f.inputs(s.parsed(t))
	in.ChecksumsPath = big
	if _, err := Verify(in); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("big checksums: err = %v, want ErrTooLarge", err)
	}
	huge := filepath.Join(f.dir, "huge")
	file, err = os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxArtifactBytes + 1); err != nil {
		t.Skipf("sparse files unavailable: %v", err)
	}
	file.Close()
	if _, err := fileSHA256(huge); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("huge artifact: err = %v, want ErrTooLarge", err)
	}
}

func TestVerifyDigestUnchanged(t *testing.T) {
	s := newTestSigner(t)
	f := s.fixture(t, "0.2.0", 5)
	res, err := Verify(f.inputs(s.parsed(t)))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigestUnchanged(f.artifact, res.SHA256); err != nil {
		t.Fatalf("unchanged: %v", err)
	}
	if err := os.WriteFile(f.artifact, []byte("swapped after verification"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigestUnchanged(f.artifact, res.SHA256); !errors.Is(err, ErrDigestChanged) {
		t.Fatalf("swapped: err = %v, want ErrDigestChanged", err)
	}
	if err := VerifyDigestUnchanged(f.artifact, ""); !errors.Is(err, ErrDigestChanged) {
		t.Fatalf("empty digest: err = %v", err)
	}
	link := filepath.Join(f.dir, "link")
	_ = os.Symlink(f.artifact, link)
	if err := VerifyDigestUnchanged(link, res.SHA256); !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("symlink: err = %v", err)
	}
}
