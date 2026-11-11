package backup

import (
	"bytes"
	"context"
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
	"sync"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/hkdf"

	"homestead/internal/operation"
	"homestead/internal/secret"
)

// Recovery key rotation (S492, ADR-022 amendment).
//
// A backup file is "age header + payload". The header holds one stanza
// per recipient, each wrapping the same random per-file key; the payload
// is encrypted with that file key, not with the recovery key. Rotation
// therefore generates a new appliance identity (RotateIdentity) and a
// separate background operation (RewrapDefinition, S505) re-wraps the
// file key of every existing run to it by rewriting only the header (the stanzas
// already there are kept verbatim, so owner recipients and the old key
// still work). The payload bytes are copied unchanged and the new file is
// fully decrypted with the new identity before it replaces the old one,
// so a run is never left unreadable and no run is deleted.

// RetiredIdentitySecret holds earlier appliance identities, one per
// line. They stay on the appliance so a run that could not be re-wrapped
// remains readable; a later re-wrap retries those runs.
const RetiredIdentitySecret = IdentitySecret + "_retired"

// maxHeaderBytes bounds how much of a file is scanned for its header.
const maxHeaderBytes = 1 << 20

// RotateResult reports a rotation. It carries no key material except
// New, which the caller must reveal once and never log. Rotation only
// mints (S505); re-wrapping existing runs is RewrapDefinition.
type RotateResult struct {
	New            *age.X25519Identity
	OldFingerprint string // "" when there was no earlier identity
}

var (
	// ErrBackupRunning refuses a rotation while a backup-writing
	// operation is in flight: it could finish encrypted to a key the
	// rotation just retired from the recipient list (409).
	ErrBackupRunning = errors.New("backup: a backup is running; rotate the recovery key when it has finished")
	// ErrRewrapRunning refuses a second re-wrap, and a rotation, while one
	// is running (409).
	ErrRewrapRunning = errors.New("backup: a re-wrap of existing backups is already running")
)

// rewrapMu serializes re-wraps and the temp-file cleanup that precedes them.
var rewrapMu sync.Mutex

// RetiredIdentities returns the earlier appliance identities that can be
// read. An unreadable line is skipped, never fatal: restore must keep
// working with the identities that are readable (S505).
func RetiredIdentities(store KeyStore) ([]*age.X25519Identity, error) {
	ids, _, err := RetiredIdentityReport(store)
	return ids, err
}

// RetiredIdentityReport returns the readable retired identities and how
// many stored lines could not be parsed.
func RetiredIdentityReport(store KeyStore) ([]*age.X25519Identity, int, error) {
	value, err := store.Get(RetiredIdentitySecret)
	if err != nil {
		if errors.Is(err, secret.ErrNotFound) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("backup: read retired identities: %w", err)
	}
	var out []*age.X25519Identity
	unreadable := 0
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, perr := age.ParseX25519Identity(line)
		if perr != nil {
			unreadable++
			continue
		}
		out = append(out, id)
	}
	return out, unreadable, nil
}

// RotateIdentity mints a new appliance identity: the old one is appended
// to the retired store (unreadable lines already there are preserved
// verbatim), then the new one is stored. It does not touch backup files;
// RewrapDefinition does that in the background. It refuses while a
// backup-writing operation holds the run gate. The caller holds the
// returned identity only until it has been revealed to the owner.
func RotateIdentity(store KeyStore) (RotateResult, error) {
	if store == nil {
		return RotateResult{}, errors.New("backup: rotation needs the secret store")
	}
	if !runGate.TryLock() {
		return RotateResult{}, ErrBackupRunning
	}
	defer runGate.Unlock()
	if rewrapMu.TryLock() {
		rewrapMu.Unlock()
	} else {
		return RotateResult{}, ErrRewrapRunning
	}
	identityMu.Lock()
	defer identityMu.Unlock()
	var old *age.X25519Identity
	if value, err := store.Get(IdentitySecret); err == nil {
		id, perr := age.ParseX25519Identity(strings.TrimSpace(value))
		if perr != nil {
			return RotateResult{}, errors.New("backup: stored backup identity is unreadable")
		}
		old = id
	} else if !errors.Is(err, secret.ErrNotFound) {
		return RotateResult{}, fmt.Errorf("backup: read identity: %w", err)
	}
	raw := ""
	if value, err := store.Get(RetiredIdentitySecret); err == nil {
		raw = strings.TrimRight(value, "\n")
	} else if !errors.Is(err, secret.ErrNotFound) {
		return RotateResult{}, fmt.Errorf("backup: read retired identities: %w", err)
	}
	fresh, err := age.GenerateX25519Identity()
	if err != nil {
		return RotateResult{}, fmt.Errorf("backup: generate identity: %w", err)
	}
	res := RotateResult{New: fresh}
	if old != nil {
		res.OldFingerprint = Fingerprint(old.Recipient().String())
		if raw != "" {
			raw += "\n"
		}
		// Retire before replacing: a failure between the two writes leaves
		// the old key in both places, never in neither.
		if err := store.SetSystem(RetiredIdentitySecret, raw+old.String()); err != nil {
			return RotateResult{}, fmt.Errorf("backup: retire identity: %w", err)
		}
	}
	if err := store.SetSystem(IdentitySecret, fresh.String()); err != nil {
		return RotateResult{}, fmt.Errorf("backup: store identity: %w", err)
	}
	return res, nil
}

// RewrapOperationType is the operation type of a background re-wrap.
const RewrapOperationType = "backup.rewrap"

// maxFailedListed bounds the failed paths kept in a summary.
const maxFailedListed = 50

// RewrapSummary is what a re-wrap leaves behind for the status, the
// Doctor and the owner. It names files, never keys.
type RewrapSummary struct {
	OperationID    int64    `json:"operation_id,omitempty"`
	Fingerprint    string   `json:"fingerprint"`
	FinishedAt     string   `json:"finished_at,omitempty"`
	Rewrapped      int      `json:"rewrapped"`
	AlreadyCurrent int      `json:"already_current"`
	Failed         []string `json:"failed"`
	FailedCount    int      `json:"failed_count"`
	Complete       bool     `json:"complete"`
}

// CleanRewrapTemps removes temp files an interrupted re-wrap left behind
// (regular files named .rewrap-*) and returns how many it removed.
// Callers hold the re-wrap lock.
func CleanRewrapTemps(root string) (int, error) {
	removed := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || !strings.HasPrefix(d.Name(), ".rewrap-") {
			return nil
		}
		if rerr := os.Remove(path); rerr == nil {
			removed++
		}
		return nil
	})
	return removed, err
}

// RewrapDefinition plans a background re-wrap of every run under root to
// the current appliance identity. It takes the re-wrap lock now (a second
// start gets ErrRewrapRunning) and releases it when the operation ends on
// any path. Keys are read inside the step; none is carried in the
// definition. report, when set, receives the summary when the walk ends.
func RewrapDefinition(keys KeyStore, root string, report func(RewrapSummary)) (operation.Definition, error) {
	if keys == nil {
		return operation.Definition{}, errors.New("backup: re-wrap needs the secret store")
	}
	if root == "" {
		return operation.Definition{}, errors.New("backup: re-wrap needs a backup destination")
	}
	if !rewrapMu.TryLock() {
		return operation.Definition{}, ErrRewrapRunning
	}
	release := sync.OnceFunc(rewrapMu.Unlock)
	return operation.Definition{
		Type:    RewrapOperationType,
		Cleanup: release,
		Steps: []operation.StepDef{
			{Name: "clean", Run: func(ctx context.Context) error {
				_, err := CleanRewrapTemps(root)
				return err
			}},
			{Name: "rewrap", Run: func(ctx context.Context) error {
				fresh, ok, err := PeekApplianceIdentity(keys)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("backup: there is no recovery identity yet, so nothing to re-wrap to")
				}
				retired, _, err := RetiredIdentityReport(keys)
				if err != nil {
					return err
				}
				var unwrap []age.Identity
				for _, id := range retired {
					unwrap = append(unwrap, id)
				}
				sum := RewrapRuns(ctx, root, fresh, unwrap)
				if report != nil {
					report(sum)
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if sum.FailedCount > 0 {
					return fmt.Errorf("%d files could not be re-wrapped", sum.FailedCount)
				}
				return nil
			}},
		},
	}, nil
}

// RewrapRuns adds fresh's recipient to every encrypted file under root.
// A directory that cannot be read counts as a failure. It stops between
// files when ctx ends, leaving Complete false.
func RewrapRuns(ctx context.Context, root string, fresh *age.X25519Identity, unwrap []age.Identity) RewrapSummary {
	sum := RewrapSummary{Fingerprint: Fingerprint(fresh.Recipient().String()), Failed: []string{}}
	fail := func(rel string) {
		sum.FailedCount++
		if len(sum.Failed) < maxFailedListed {
			sum.Failed = append(sum.Failed, rel)
		}
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(root, path)
		if err != nil {
			fail(rel)
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(path, FileSuffix) {
			return nil
		}
		changed, rerr := RewrapFile(path, fresh, unwrap)
		switch {
		case rerr != nil:
			fail(rel)
		case changed:
			sum.Rewrapped++
		default:
			sum.AlreadyCurrent++
		}
		return nil
	})
	sum.Complete = ctx.Err() == nil && sum.FailedCount == 0
	sum.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	return sum
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
