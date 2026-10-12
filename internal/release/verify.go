// Package release verifies platform release artifacts (S264, ADR-025).
//
// Trust chain: the artifact's SHA-256 must appear in a checksums file,
// and the checksums file must carry a minisign non-prehashed ("Ed")
// Ed25519 signature that verifies against a trusted key. The minisign
// formats are parsed here with the standard library only; no minisign
// binary or BLAKE2b dependency exists. Nothing in this package fetches:
// the owner supplies every file locally.
package release

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Size limits for owner-supplied files. Checksums and signature files
// are tiny, so anything larger is refused before it is read into
// memory; artifacts are bounded so a hostile path cannot make the
// daemon hash an unbounded stream.
const (
	// MaxSmallFileBytes bounds checksums and signature files (1 MiB).
	MaxSmallFileBytes = 1 << 20
	// MaxArtifactBytes bounds an artifact (2 GiB).
	MaxArtifactBytes int64 = 2 << 30
	maxSmallFile           = MaxSmallFileBytes
)

// Sentinel errors. Authenticity failures (everything but ErrRollback)
// have no override by design (ADR-025).
var (
	ErrInvalidKey     = errors.New("release: invalid minisign public key")
	ErrInvalidSig     = errors.New("release: invalid minisign signature")
	ErrPrehashed      = errors.New("release: prehashed (ED) signatures are rejected; sign with the non-prehashed form")
	ErrUnknownKey     = errors.New("release: signature key is not trusted")
	ErrBadSignature   = errors.New("release: signature does not verify")
	ErrBadChecksums   = errors.New("release: invalid checksums file")
	ErrChecksumAbsent = errors.New("release: artifact not listed in checksums file")
	ErrChecksum       = errors.New("release: artifact SHA-256 does not match checksums file")
	// ErrNotRegularFile is the single answer for any path that is not a
	// readable regular file (missing, unreadable, symlink, FIFO, device,
	// directory). It deliberately does not say which, so the route cannot
	// be used to probe the filesystem.
	ErrNotRegularFile = errors.New("release: not a readable regular file")
	// ErrTooLarge marks a file over its size limit.
	ErrTooLarge = errors.New("release: file exceeds the size limit")
	// ErrDigestChanged marks an artifact whose bytes no longer match the
	// digest recorded at verification (see VerifyDigestUnchanged).
	ErrDigestChanged = errors.New("release: artifact changed since verification")
	// ErrRollback marks an artifact whose counter is below the installed
	// one: the only refusal an explicit owner override can lift.
	ErrRollback = errors.New("release: artifact is older than the installed release")
	// ErrCounterConflict marks an equal counter with a different
	// version; it has no override.
	ErrCounterConflict = errors.New("release: same counter as the installed release but a different version")
)

// PublicKey is a parsed minisign public key.
type PublicKey struct {
	// KeyID is the 8-byte minisign key identifier, upper-case hex in
	// minisign's byte order (as shown in "minisign -V" output).
	KeyID string
	Key   ed25519.PublicKey
	raw   [8]byte
}

// Encoded returns the canonical base64 public key line.
func (k PublicKey) Encoded() string {
	b := make([]byte, 0, 42)
	b = append(b, 'E', 'd')
	b = append(b, k.raw[:]...)
	b = append(b, k.Key...)
	return base64.StdEncoding.EncodeToString(b)
}

func keyIDString(id []byte) string {
	// minisign prints the key id little-endian; mirror that.
	out := make([]byte, len(id))
	for i := range id {
		out[len(id)-1-i] = id[i]
	}
	return strings.ToUpper(hex.EncodeToString(out))
}

// meaningfulLines returns non-empty lines.
func lines(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// ParsePublicKey accepts a minisign public key file ("untrusted
// comment" line then base64) or the bare base64 line.
func ParsePublicKey(text string) (PublicKey, error) {
	ls := lines(text)
	var encoded string
	switch {
	case len(ls) == 1 && !strings.HasPrefix(ls[0], "untrusted comment:"):
		encoded = ls[0]
	case len(ls) == 2 && strings.HasPrefix(ls[0], "untrusted comment:"):
		encoded = ls[1]
	default:
		return PublicKey{}, fmt.Errorf("%w: expected a comment line and a key line", ErrInvalidKey)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) != 2+8+ed25519.PublicKeySize {
		return PublicKey{}, fmt.Errorf("%w: bad encoding or length", ErrInvalidKey)
	}
	if raw[0] != 'E' || raw[1] != 'd' {
		return PublicKey{}, fmt.Errorf("%w: unsupported algorithm", ErrInvalidKey)
	}
	k := PublicKey{Key: ed25519.PublicKey(append([]byte(nil), raw[10:]...))}
	copy(k.raw[:], raw[2:10])
	k.KeyID = keyIDString(raw[2:10])
	return k, nil
}

// Signature is a parsed minisign signature file.
type Signature struct {
	keyID          [8]byte
	KeyID          string
	sig            []byte
	TrustedComment string
	global         []byte
}

// ParseSignature parses a minisign .minisig file. Prehashed ("ED")
// signatures are rejected (ADR-025).
func ParseSignature(text string) (Signature, error) {
	ls := lines(text)
	if len(ls) != 4 || !strings.HasPrefix(ls[0], "untrusted comment:") || !strings.HasPrefix(ls[2], "trusted comment:") {
		return Signature{}, fmt.Errorf("%w: expected four-line minisign layout", ErrInvalidSig)
	}
	raw, err := base64.StdEncoding.DecodeString(ls[1])
	if err != nil || len(raw) != 2+8+ed25519.SignatureSize {
		return Signature{}, fmt.Errorf("%w: bad signature encoding or length", ErrInvalidSig)
	}
	switch string(raw[:2]) {
	case "Ed":
	case "ED":
		return Signature{}, ErrPrehashed
	default:
		return Signature{}, fmt.Errorf("%w: unknown algorithm", ErrInvalidSig)
	}
	global, err := base64.StdEncoding.DecodeString(ls[3])
	if err != nil || len(global) != ed25519.SignatureSize {
		return Signature{}, fmt.Errorf("%w: bad global signature", ErrInvalidSig)
	}
	s := Signature{
		sig:            append([]byte(nil), raw[10:]...),
		TrustedComment: strings.TrimPrefix(strings.TrimPrefix(ls[2], "trusted comment:"), " "),
		global:         global,
		KeyID:          keyIDString(raw[2:10]),
	}
	copy(s.keyID[:], raw[2:10])
	return s, nil
}

// Verify checks the signature over message with key: the message
// signature, then minisign's global signature over signature plus
// trusted comment.
func (s Signature) Verify(key PublicKey, message []byte) error {
	if s.keyID != key.raw {
		return ErrUnknownKey
	}
	if !ed25519.Verify(key.Key, message, s.sig) {
		return ErrBadSignature
	}
	global := append(append([]byte(nil), s.sig...), []byte(s.TrustedComment)...)
	if !ed25519.Verify(key.Key, global, s.global) {
		return ErrBadSignature
	}
	return nil
}

// Checksums is the parsed, signed checksums file. Format:
//
//	version=<release version>
//	counter=<positive integer>
//	<sha256 hex>  <file name>     (sha256sum layout, one per artifact)
type Checksums struct {
	Version string
	Counter int64
	Files   map[string]string
}

// maxChecksumLine bounds one checksums line; real lines are ~100 bytes.
const maxChecksumLine = 16 << 10

// ParseChecksums parses the checksums file; version and counter are
// mandatory (ADR-025).
func ParseChecksums(data []byte) (Checksums, error) {
	c := Checksums{Files: map[string]string{}}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// A line longer than the buffer fails the scan (checked below) rather
	// than being silently truncated.
	scanner.Buffer(make([]byte, 0, 4096), maxChecksumLine)
	haveCounter, haveVersion := false, false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "version="):
			if haveVersion {
				return Checksums{}, fmt.Errorf("%w: duplicate version", ErrBadChecksums)
			}
			haveVersion = true
			c.Version = strings.TrimSpace(strings.TrimPrefix(line, "version="))
		case strings.HasPrefix(line, "counter="):
			if haveCounter {
				return Checksums{}, fmt.Errorf("%w: duplicate counter", ErrBadChecksums)
			}
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "counter=")), 10, 64)
			if err != nil || n < 1 {
				return Checksums{}, fmt.Errorf("%w: counter must be a positive integer", ErrBadChecksums)
			}
			c.Counter, haveCounter = n, true
		default:
			fields := strings.Fields(line)
			if len(fields) != 2 || len(fields[0]) != 64 {
				return Checksums{}, fmt.Errorf("%w: unreadable line %q", ErrBadChecksums, line)
			}
			if _, err := hex.DecodeString(fields[0]); err != nil {
				return Checksums{}, fmt.Errorf("%w: bad digest for %q", ErrBadChecksums, fields[1])
			}
			name := strings.TrimPrefix(fields[1], "*")
			if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
				return Checksums{}, fmt.Errorf("%w: file name %q must be a bare name, not a path", ErrBadChecksums, name)
			}
			if _, dup := c.Files[name]; dup {
				return Checksums{}, fmt.Errorf("%w: duplicate entry %q", ErrBadChecksums, name)
			}
			c.Files[name] = strings.ToLower(fields[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return Checksums{}, fmt.Errorf("%w: %v", ErrBadChecksums, err)
	}
	if c.Version == "" || !haveCounter {
		return Checksums{}, fmt.Errorf("%w: version and counter are required", ErrBadChecksums)
	}
	return c, nil
}

// Installed is the recorded installed release for rollback checks. A
// zero value means nothing is recorded and no rollback check applies.
type Installed struct {
	Version string
	Counter int64
}

// Result is a successful authenticity check.
type Result struct {
	Version string
	Counter int64
	KeyID   string
	SHA256  string
	// Override is true when allowOlder lifted a rollback refusal.
	Override bool
}

// Inputs are the owner-supplied local files plus policy.
type Inputs struct {
	ArtifactPath  string
	ChecksumsPath string
	SignaturePath string
	Trusted       []PublicKey
	Installed     Installed
	AllowOlder    bool
}

// openRegular opens path for reading only if it is a regular file no
// larger than limit. The checks run on the open descriptor (no
// check-then-open race): O_NOFOLLOW refuses a symlink final component,
// O_NONBLOCK stops a FIFO from blocking the open, and fstat then refuses
// anything that is not a regular file. Every failure to open or classify
// is ErrNotRegularFile, so callers cannot learn what exists.
func openRegular(path string, limit int64) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrNotRegularFile
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrNotRegularFile
	}
	if info.Size() > limit {
		_ = f.Close()
		return nil, fmt.Errorf("%w (%d bytes, limit %d)", ErrTooLarge, info.Size(), limit)
	}
	return f, nil
}

func readSmall(path string) ([]byte, error) {
	f, err := openRegular(path, maxSmallFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSmallFile+1))
	if err != nil {
		return nil, ErrNotRegularFile
	}
	if len(data) > maxSmallFile {
		return nil, fmt.Errorf("%w (limit %d)", ErrTooLarge, maxSmallFile)
	}
	return data, nil
}

// Verify runs the full chain. Order: signature (authenticity of the
// checksums file), checksums parse, artifact digest, then rollback
// policy. The returned Result is meaningful on ErrRollback too, so
// callers can audit both versions.
func Verify(in Inputs) (Result, error) {
	sigText, err := readSmall(in.SignaturePath)
	if err != nil {
		return Result{}, err
	}
	sig, err := ParseSignature(string(sigText))
	if err != nil {
		return Result{}, err
	}
	sums, err := readSmall(in.ChecksumsPath)
	if err != nil {
		return Result{}, err
	}
	var matched *PublicKey
	for i := range in.Trusted {
		if in.Trusted[i].raw == sig.keyID {
			matched = &in.Trusted[i]
			break
		}
	}
	if matched == nil {
		return Result{}, fmt.Errorf("%w (key id %s)", ErrUnknownKey, sig.KeyID)
	}
	if err := sig.Verify(*matched, sums); err != nil {
		return Result{}, err
	}
	parsed, err := ParseChecksums(sums)
	if err != nil {
		return Result{}, err
	}
	name := filepath.Base(in.ArtifactPath)
	want, ok := parsed.Files[name]
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrChecksumAbsent, name)
	}
	got, err := fileSHA256(in.ArtifactPath)
	if err != nil {
		return Result{}, err
	}
	if got != want {
		return Result{}, fmt.Errorf("%w: %s", ErrChecksum, name)
	}
	res := Result{Version: parsed.Version, Counter: parsed.Counter, KeyID: matched.KeyID, SHA256: got}
	inst := in.Installed
	if inst.Counter > 0 {
		switch {
		case parsed.Counter < inst.Counter:
			if !in.AllowOlder {
				return res, fmt.Errorf("%w: counter %d (%s) < installed %d (%s)",
					ErrRollback, parsed.Counter, parsed.Version, inst.Counter, inst.Version)
			}
			res.Override = true
		case parsed.Counter == inst.Counter && parsed.Version != inst.Version:
			return res, fmt.Errorf("%w: counter %d is %s, installed is %s",
				ErrCounterConflict, parsed.Counter, parsed.Version, inst.Version)
		}
	}
	return res, nil
}

func fileSHA256(path string) (string, error) {
	f, err := openRegular(path, MaxArtifactBytes)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	// Read one byte past the limit so a file that grew after fstat is
	// still bounded.
	n, err := io.Copy(h, io.LimitReader(f, MaxArtifactBytes+1))
	if err != nil {
		return "", ErrNotRegularFile
	}
	if n > MaxArtifactBytes {
		return "", fmt.Errorf("%w (limit %d)", ErrTooLarge, MaxArtifactBytes)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyDigestUnchanged re-hashes the artifact at path and compares it
// with digest, the SHA-256 Verify returned (stored in the release
// record). Verification and use are separate moments: an owner-writable
// artifact can be swapped in between. Update actuation (S265) must call
// this immediately before replacing the binary, and refuse on any error.
func VerifyDigestUnchanged(path, digest string) error {
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, digest) || len(digest) != sha256.Size*2 {
		return ErrDigestChanged
	}
	return nil
}
