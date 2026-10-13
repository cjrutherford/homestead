package backup

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"

	"homestead/internal/store"
)

func openRecipients(t *testing.T) *RecipientStore {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewRecipientStore(st)
}

func TestParseRecipientIsStrict(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	good := id.Recipient().String()
	if _, err := ParseRecipient(" " + good + "\n"); err != nil {
		t.Fatalf("trimmed valid key refused: %v", err)
	}
	for _, bad := range []string{
		"", "age1", strings.ToUpper(good), good + " extra", good + "\n" + good,
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
		id.String(), "age1pq1abc",
	} {
		if _, err := ParseRecipient(bad); err == nil {
			t.Errorf("ParseRecipient(%q) accepted", bad)
		}
	}
}

func TestRecipientStoreAddRemoveAndRefusals(t *testing.T) {
	s := openRecipients(t)
	keys := newKeys()
	appliance, _ := ApplianceIdentity(keys)
	app := appliance.Recipient().String()
	owner, _ := age.GenerateX25519Identity()
	ok := owner.Recipient().String()

	if _, err := s.Add("nope", "", "chris", app); err == nil {
		t.Fatal("invalid key accepted")
	}
	if _, err := s.Add(app, "", "chris", app); err == nil {
		t.Fatal("appliance recipient accepted as owner recipient")
	}
	if _, err := s.Add(ok, "", " ", app); err == nil {
		t.Fatal("missing actor accepted")
	}
	rec, err := s.Add(ok, "laptop", "chris", app)
	if err != nil || rec.Fingerprint != Fingerprint(ok) || rec.Kind != "owner" {
		t.Fatalf("add: %+v %v", rec, err)
	}
	if _, err := s.Add(ok, "", "chris", app); err != ErrRecipientExists {
		t.Fatalf("duplicate err = %v", err)
	}
	if _, err := s.Remove(Fingerprint(app), app); err != ErrApplianceRecipient {
		t.Fatalf("appliance removal err = %v", err)
	}
	if _, err := s.Remove("deadbeef", app); err != ErrRecipientNotFound {
		t.Fatalf("unknown err = %v", err)
	}
	if _, err := s.Remove(rec.Fingerprint, app); err != nil {
		t.Fatal(err)
	}
	// Even with every owner recipient gone the appliance remains.
	all, err := RecipientsWithOwner(keys, s)
	if err != nil || len(all) != 1 {
		t.Fatalf("recipients = %d err %v", len(all), err)
	}
}

func TestOwnerProtectedByRecipientOrAcknowledgedExport(t *testing.T) {
	s := openRecipients(t)
	if held, _ := s.OwnerProtected(); held {
		t.Fatal("protected with nothing configured")
	}
	if _, err := s.Acknowledge(); err != ErrNothingToAcknowledge {
		t.Fatalf("ack with no export = %v", err)
	}
	if _, err := s.RecordExport("abc", "chris"); err != nil {
		t.Fatal(err)
	}
	if held, _ := s.OwnerProtected(); held {
		t.Fatal("an unacknowledged export counted")
	}
	e, err := s.Acknowledge()
	if err != nil || !e.Acknowledged() {
		t.Fatalf("ack = %+v %v", e, err)
	}
	if _, err := s.Acknowledge(); err != ErrNothingToAcknowledge {
		t.Fatalf("second ack = %v", err)
	}
	if held, _ := s.OwnerProtected(); !held {
		t.Fatal("acknowledged export did not count")
	}
	last, ok, _ := s.LastExport()
	if !ok || last.Actor != "chris" || last.Fingerprint != "abc" || !last.Acknowledged() {
		t.Fatalf("export = %+v", last)
	}
}

func TestExportRateLimit(t *testing.T) {
	s := openRecipients(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for i := 0; i < MaxExportsPerHour; i++ {
		if _, err := s.RecordExport("fp", "chris"); err != nil {
			t.Fatalf("export %d: %v", i, err)
		}
	}
	if _, err := s.RecordExport("fp", "chris"); err != ErrExportRateLimited {
		t.Fatalf("over limit = %v", err)
	}
	now = now.Add(61 * time.Minute)
	if _, err := s.RecordExport("fp", "chris"); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestPeekDoesNotCreateIdentity(t *testing.T) {
	keys := newKeys()
	if _, ok, err := PeekApplianceIdentity(keys); ok || err != nil {
		t.Fatalf("peek = %v %v", ok, err)
	}
	if len(keys.values) != 0 {
		t.Fatal("peek stored an identity")
	}
	if _, err := ApplianceIdentity(keys); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := PeekApplianceIdentity(keys); !ok {
		t.Fatal("peek missed an existing identity")
	}
}

// The cap is enforced under concurrent adds, and a recipient removed
// concurrently is deleted exactly once.
func TestRecipientCapAndRemoveUnderConcurrency(t *testing.T) {
	s := openRecipients(t)
	var wg sync.WaitGroup
	var added atomic.Int64
	for i := 0; i < MaxOwnerRecipients+8; i++ {
		id, _ := age.GenerateX25519Identity()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Add(id.Recipient().String(), "", "chris", ""); err == nil {
				added.Add(1)
			}
		}()
	}
	wg.Wait()
	owners, _ := s.Owner()
	if added.Load() != MaxOwnerRecipients || len(owners) != MaxOwnerRecipients {
		t.Fatalf("added %d, stored %d, cap %d", added.Load(), len(owners), MaxOwnerRecipients)
	}
	var removed, missing atomic.Int64
	target := owners[0].Fingerprint
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Remove(target, "")
			switch err {
			case nil:
				removed.Add(1)
			case ErrRecipientNotFound:
				missing.Add(1)
			}
		}()
	}
	wg.Wait()
	if removed.Load() != 1 || missing.Load() != 5 {
		t.Fatalf("removed %d, not found %d", removed.Load(), missing.Load())
	}
}

func TestRunsEncryptToNewRecipientsOnly(t *testing.T) {
	s := openRecipients(t)
	keys := newKeys()
	appliance, _ := ApplianceIdentity(keys)
	owner, _ := age.GenerateX25519Identity()
	src, out := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "f.txt"), "payload")

	before, _ := RecipientsWithOwner(keys, s)
	oldRun := filepath.Join(out, "old.tar.age")
	if _, err := archiveTree(src, oldRun, before); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Add(owner.Recipient().String(), "", "chris", appliance.Recipient().String())
	if err != nil {
		t.Fatal(err)
	}
	with, _ := RecipientsWithOwner(keys, s)
	if len(with) != 2 {
		t.Fatalf("recipients = %d", len(with))
	}
	newRun := filepath.Join(out, "new.tar.age")
	if _, err := archiveTree(src, newRun, with); err != nil {
		t.Fatal(err)
	}
	// The owner's identity alone decrypts the new run, not the old one.
	if got := decryptWith(t, newRun, owner); got == 0 {
		t.Fatal("new run empty")
	}
	if _, err := openAndRead(oldRun, owner); err == nil {
		t.Fatal("owner decrypted a run made before it was added")
	}
	// The appliance identity still reads both.
	decryptWith(t, newRun, appliance)
	decryptWith(t, oldRun, appliance)

	if _, err := s.Remove(rec.Fingerprint, appliance.Recipient().String()); err != nil {
		t.Fatal(err)
	}
	after, _ := RecipientsWithOwner(keys, s)
	laterRun := filepath.Join(out, "later.tar.age")
	if _, err := archiveTree(src, laterRun, after); err != nil {
		t.Fatal(err)
	}
	if _, err := openAndRead(laterRun, owner); err == nil {
		t.Fatal("removed recipient decrypted a later run")
	}
	decryptWith(t, laterRun, appliance)
	// Earlier runs are not re-encrypted: the removed recipient keeps them.
	decryptWith(t, newRun, owner)
}

func openAndRead(path string, id age.Identity) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := age.Decrypt(f, id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func decryptWith(t *testing.T, path string, id age.Identity) int {
	t.Helper()
	data, err := openAndRead(path, id)
	if err != nil {
		t.Fatalf("decrypt %s: %v", path, err)
	}
	return len(data)
}

func TestRenderRecipientsYAMLHasPublicKeysOnly(t *testing.T) {
	s := openRecipients(t)
	keys := newKeys()
	appliance, _ := ApplianceIdentity(keys)
	owner, _ := age.GenerateX25519Identity()
	if _, err := s.Add(owner.Recipient().String(), "laptop", "chris", appliance.Recipient().String()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordExport("abc", "chris"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acknowledge(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup-recipients.yaml")
	if err := s.RenderYAML(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte(owner.Recipient().String())) || !bytes.Contains(raw, []byte("acknowledged_at")) {
		t.Fatalf("yaml = %s", raw)
	}
	if bytes.Contains(raw, []byte("AGE-SECRET-KEY")) || bytes.Contains(raw, []byte(appliance.String())) {
		t.Fatal("secret key in yaml")
	}
}
