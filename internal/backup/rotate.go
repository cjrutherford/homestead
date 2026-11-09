package backup

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"golang.org/x/crypto/hkdf"

	"homestead/internal/secret"
)

// Recovery key rotation (S492, ADR-022 amendment).
//
// A backup file is "age header + payload". The header holds one stanza
// per recipient, each wrapping the same random per-file key; the payload
// is encrypted with that file key, not with the recovery key. Rotation
// therefore generates a new appliance identity and re-wraps the file key
// of every existing run to it by rewriting only the header (the stanzas
// already there are kept verbatim, so owner recipients and the old key
// still work). The payload bytes are copied unchanged and the new file is
// fully decrypted with the new identity before it replaces the old one,
// so a run is never left unreadable and no run is deleted.

// RetiredIdentitySecret holds earlier appliance identities, one per
// line. They stay on the appliance so a run that could not be re-wrapped
// remains readable; a later rotation retries those runs.
const RetiredIdentitySecret = IdentitySecret + "_retired"

// maxHeaderBytes bounds how much of a file is scanned for its header.
const maxHeaderBytes = 1 << 20

// RotateResult reports a rotation. It carries no key material except
// New, which the caller must reveal once and never log.
type RotateResult struct {
	New            *age.X25519Identity
	OldFingerprint string // "" when there was no earlier identity
	Rewrapped      int
	AlreadyCurrent int
	Failed         []string // file paths (relative to the root) that could not be re-wrapped
}

// RetiredIdentities returns the earlier appliance identities.
func RetiredIdentities(store KeyStore) ([]*age.X25519Identity, error) {
	value, err := store.Get(RetiredIdentitySecret)
	if err != nil {
		if errors.Is(err, secret.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: read retired identities: %w", err)
	}
	var out []*age.X25519Identity
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, perr := age.ParseX25519Identity(line)
		if perr != nil {
			return nil, errors.New("backup: stored retired identity is unreadable")
		}
		out = append(out, id)
	}
	return out, nil
}

// RotateIdentity replaces the appliance identity. The old one is kept as
// a retired identity first, then the new one is stored, then the runs
// under root (when not empty) are re-wrapped. The caller holds the
// returned identity only until it has been revealed to the owner.
func RotateIdentity(store KeyStore, root string) (RotateResult, error) {
	if store == nil {
		return RotateResult{}, errors.New("backup: rotation needs the secret store")
	}
	identityMu.Lock()
	var old *age.X25519Identity
	if value, err := store.Get(IdentitySecret); err == nil {
		id, perr := age.ParseX25519Identity(strings.TrimSpace(value))
		if perr != nil {
			identityMu.Unlock()
			return RotateResult{}, errors.New("backup: stored backup identity is unreadable")
		}
		old = id
	} else if !errors.Is(err, secret.ErrNotFound) {
		identityMu.Unlock()
		return RotateResult{}, fmt.Errorf("backup: read identity: %w", err)
	}
	retired, err := RetiredIdentities(store)
	if err != nil {
		identityMu.Unlock()
		return RotateResult{}, err
	}
	fresh, err := age.GenerateX25519Identity()
	if err != nil {
		identityMu.Unlock()
		return RotateResult{}, fmt.Errorf("backup: generate identity: %w", err)
	}
	res := RotateResult{New: fresh}
	if old != nil {
		res.OldFingerprint = Fingerprint(old.Recipient().String())
		retired = append(retired, old)
		lines := make([]string, 0, len(retired))
		for _, id := range retired {
			lines = append(lines, id.String())
		}
		// Retire before replacing: a failure between the two writes leaves
		// the old key in both places, never in neither.
		if err := store.SetSystem(RetiredIdentitySecret, strings.Join(lines, "\n")); err != nil {
			identityMu.Unlock()
			return RotateResult{}, fmt.Errorf("backup: retire identity: %w", err)
		}
	}
	if err := store.SetSystem(IdentitySecret, fresh.String()); err != nil {
		identityMu.Unlock()
		return RotateResult{}, fmt.Errorf("backup: store identity: %w", err)
	}
	identityMu.Unlock()
	if root != "" {
		var unwrap []age.Identity
		for _, id := range retired {
			unwrap = append(unwrap, id)
		}
		RewrapRuns(root, fresh, unwrap, &res)
	}
	return res, nil
}

// RewrapRuns adds fresh's recipient to every encrypted file under root.
func RewrapRuns(root string, fresh *age.X25519Identity, unwrap []age.Identity, res *RotateResult) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || !strings.HasSuffix(path, FileSuffix) {
			return nil
		}
		changed, rerr := RewrapFile(path, fresh, unwrap)
		rel, _ := filepath.Rel(root, path)
		switch {
		case rerr != nil:
			res.Failed = append(res.Failed, rel)
		case changed:
			res.Rewrapped++
		default:
			res.AlreadyCurrent++
		}
		return nil
	})
}

// captureIdentity tries each identity and remembers the file key that
// opened the header.
type captureIdentity struct {
	ids []age.Identity
	key []byte
}

func (c *captureIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	for _, id := range c.ids {
		key, err := id.Unwrap(stanzas)
		if err == nil {
			c.key = append([]byte(nil), key...)
			return key, nil
		}
		if !errors.Is(err, age.ErrIncorrectIdentity) {
			return nil, err
		}
	}
	return nil, age.ErrIncorrectIdentity
}

// RewrapFile adds fresh's recipient to one age file by rewriting its
// header. It reports false when fresh can already open the file. unwrap
// are the identities able to open the file today (the retired ones).
func RewrapFile(path string, fresh *age.X25519Identity, unwrap []age.Identity) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if r, err := age.Decrypt(f, fresh); err == nil {
		_ = r
		return false, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	capture := &captureIdentity{ids: unwrap}
	if _, err := age.Decrypt(f, capture); err != nil {
		return false, fmt.Errorf("backup: open header: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	head := make([]byte, maxHeaderBytes)
	n, err := f.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	head = head[:n]
	mark := bytes.Index(head, []byte("\n--- "))
	if mark < 0 {
		return false, errors.New("backup: header end not found")
	}
	macEnd := bytes.IndexByte(head[mark+1:], '\n')
	if macEnd < 0 {
		return false, errors.New("backup: header end not found")
	}
	payloadAt := int64(mark + 1 + macEnd + 1)

	stanzas, err := fresh.Recipient().Wrap(capture.key)
	if err != nil {
		return false, err
	}
	var hdr bytes.Buffer
	hdr.Write(head[:mark+1])
	for _, s := range stanzas {
		writeStanza(&hdr, s)
	}
	hdr.WriteString("---")
	mac, err := headerMAC(capture.key, hdr.Bytes())
	if err != nil {
		return false, err
	}
	hdr.WriteString(" " + base64.RawStdEncoding.EncodeToString(mac) + "\n")

	tmp, err := os.CreateTemp(filepath.Dir(path), ".rewrap-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(hdr.Bytes()); err != nil {
		return false, err
	}
	if _, err := io.Copy(tmp, io.NewSectionReader(f, payloadAt, info.Size()-payloadAt)); err != nil {
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		return false, err
	}
	// The new file must open with the new identity and read to the end
	// (payload authentication) before it replaces the old one.
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	r, err := age.Decrypt(tmp, fresh)
	if err != nil {
		return false, fmt.Errorf("backup: re-wrapped header rejected: %w", err)
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		return false, fmt.Errorf("backup: re-wrapped payload unreadable: %w", err)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return false, err
	}
	ok = true
	return true, nil
}

func writeStanza(w *bytes.Buffer, s *age.Stanza) {
	w.WriteString("-> " + strings.Join(append([]string{s.Type}, s.Args...), " ") + "\n")
	body := base64.RawStdEncoding.EncodeToString(s.Body)
	for len(body) >= 64 {
		w.WriteString(body[:64] + "\n")
		body = body[64:]
	}
	w.WriteString(body + "\n")
}

func headerMAC(fileKey, header []byte) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, fileKey, nil, []byte("header")), key); err != nil {
		return nil, err
	}
	h := hmac.New(sha256.New, key)
	h.Write(header)
	return h.Sum(nil), nil
}
