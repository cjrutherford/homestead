package secret

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"homestead/internal/operation"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/store"
)

func hasFile(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

func seed(t *testing.T, st *Store, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := st.Set(n, "value-of-"+n); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
}

func readable(t *testing.T, st *Store, names ...string) {
	t.Helper()
	for _, n := range names {
		got, err := st.Get(n)
		if err != nil || got != "value-of-"+n {
			t.Fatalf("Get(%s) = %q, %v", n, got, err)
		}
	}
}

func rowVersions(t *testing.T, s *store.Store) map[string]int {
	t.Helper()
	rows, err := s.DB().Query(`SELECT name, key_version FROM secrets`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var n string
		var v int
		if err := rows.Scan(&n, &v); err != nil {
			t.Fatal(err)
		}
		out[n] = v
	}
	return out
}

func TestMigratesLegacyKeyFromPreS258Fixture(t *testing.T) {
	dir := t.TempDir()
	legacy, err := os.ReadFile("testdata/pre_s258/secret.key")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, KeyFileName)
	if err := os.WriteFile(keyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	raw, err := os.ReadFile("testdata/pre_s258/rows.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct{ Name, Plain, Nonce, Ciphertext string }
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	// Insert rows exactly as pre-S258 code did: no key_version.
	before := map[string][]byte{}
	for _, r := range rows {
		nonce, _ := hex.DecodeString(r.Nonce)
		ct, _ := hex.DecodeString(r.Ciphertext)
		before[r.Name] = ct
		if _, err := db.DB().Exec(
			`INSERT INTO secrets (name, nonce, ciphertext, updated_at) VALUES (?, ?, ?, '2026-01-01T00:00:00Z')`,
			r.Name, nonce, ct); err != nil {
			t.Fatal(err)
		}
	}
	st, err := New(db, keyPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, r := range rows {
		got, err := st.Get(r.Name)
		if err != nil || got != r.Plain {
			t.Fatalf("Get(%s) = %q, %v", r.Name, got, err)
		}
	}
	// v1 is a byte copy; legacy untouched and left in place.
	v1, err := os.ReadFile(keyPath + ".v1")
	if err != nil || string(v1) != string(legacy) {
		t.Fatalf("v1 key mismatch: %v", err)
	}
	if now, _ := os.ReadFile(keyPath); string(now) != string(legacy) {
		t.Fatal("legacy key modified")
	}
	for name, v := range rowVersions(t, db) {
		if v != 1 {
			t.Fatalf("row %s stamped %d", name, v)
		}
	}
	// Ciphertext unchanged by migration.
	for name, ct := range before {
		var got []byte
		if err := db.DB().QueryRow(`SELECT ciphertext FROM secrets WHERE name = ?`, name).Scan(&got); err != nil || string(got) != string(ct) {
			t.Fatalf("ciphertext changed for %s", name)
		}
	}
	// A legacy file that disagrees with v1 (before any re-key) refuses start.
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db, keyPath); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("mismatched legacy/v1 err = %v", err)
	}
	// After a re-key v1 is gone and the legacy file is ignored.
	if err := os.WriteFile(keyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := New(db, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Rekey(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := New(db, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := after.Get(rows[0].Name); err != nil || got != rows[0].Plain {
		t.Fatalf("after legacy change: %q, %v", got, err)
	}
}

func TestRekeyHappyPath(t *testing.T) {
	st, dir := openSecret(t)
	seed(t, st, "alpha", "beta")
	keyPath := filepath.Join(dir, KeyFileName)
	v, err := st.Rekey()
	if err != nil || v != 2 {
		t.Fatalf("Rekey = %d, %v", v, err)
	}
	readable(t, st, "alpha", "beta")
	if hasFile(t, keyPath+".v1") || !hasFile(t, keyPath+".v2") {
		t.Fatal("expected only v2 key file")
	}
	if st.KeyVersion() != 2 {
		t.Fatalf("KeyVersion = %d", st.KeyVersion())
	}
	for n, rv := range rowVersions(t, storeFor(t, dir)) {
		if rv != 2 {
			t.Fatalf("row %s on %d", n, rv)
		}
	}
	// New writes use the new key; reopen reads them.
	seed(t, st, "gamma")
	again, err := New(storeFor(t, dir), keyPath)
	if err != nil {
		t.Fatal(err)
	}
	readable(t, again, "alpha", "beta", "gamma")
	// A second re-key works too.
	if v, err := again.Rekey(); err != nil || v != 3 {
		t.Fatalf("Rekey = %d, %v", v, err)
	}
	readable(t, again, "alpha", "beta", "gamma")
}

func TestRekeyEmptyStore(t *testing.T) {
	st, dir := openSecret(t)
	if v, err := st.Rekey(); err != nil || v != 2 {
		t.Fatalf("Rekey = %d, %v", v, err)
	}
	if !hasFile(t, filepath.Join(dir, KeyFileName+".v2")) {
		t.Fatal("v2 missing")
	}
}

func TestRekeyInjectedFailures(t *testing.T) {
	boom := errors.New("injected")
	for _, point := range []string{pointKeyWrite, pointMidTx, pointAfterTx, pointOldDelete} {
		t.Run(point, func(t *testing.T) {
			st, dir := openSecret(t)
			keyPath := filepath.Join(dir, KeyFileName)
			seed(t, st, "alpha", "beta")
			st.hook = func(p string) error {
				if p == point {
					return boom
				}
				return nil
			}
			if _, err := st.Rekey(); !errors.Is(err, boom) {
				t.Fatalf("Rekey err = %v", err)
			}
			if st.RekeyInProgress() {
				t.Fatal("store left claimed after failure")
			}
			// Readable in the failed process...
			readable(t, st, "alpha", "beta")
			// ...and after a simulated restart from disk.
			restarted, err := New(storeFor(t, dir), keyPath)
			if err != nil {
				t.Fatalf("restart: %v", err)
			}
			readable(t, restarted, "alpha", "beta")
			switch point {
			case pointKeyWrite:
				if hasFile(t, keyPath+".v2") {
					t.Fatal("key written before its hook point")
				}
			case pointMidTx:
				// Transaction rolled back: rows on v1, both keys kept.
				for n, v := range rowVersions(t, storeFor(t, dir)) {
					if v != 1 {
						t.Fatalf("row %s on %d after rollback", n, v)
					}
				}
				if !hasFile(t, keyPath+".v1") || !hasFile(t, keyPath+".v2") {
					t.Fatal("both keys must remain")
				}
			case pointAfterTx, pointOldDelete:
				// Committed rows on v2; restart cleanup retired v1.
				if hasFile(t, keyPath+".v1") {
					t.Fatal("restart should complete old-key cleanup")
				}
			}
			// Rerun succeeds on both the live and restarted stores.
			restarted.hook = nil
			if _, err := restarted.Rekey(); err != nil {
				t.Fatalf("rerun: %v", err)
			}
			readable(t, restarted, "alpha", "beta")
			for n, v := range rowVersions(t, storeFor(t, dir)) {
				if v != restarted.KeyVersion() {
					t.Fatalf("row %s on %d, current %d", n, v, restarted.KeyVersion())
				}
			}
			entries, _ := filepath.Glob(keyPath + ".v*")
			if len(entries) != 1 {
				t.Fatalf("key files after rerun: %v", entries)
			}
		})
	}
}

func TestRekeyRerunInSameProcess(t *testing.T) {
	for _, point := range []string{pointKeyWrite, pointMidTx, pointAfterTx, pointOldDelete} {
		t.Run(point, func(t *testing.T) {
			st, _ := openSecret(t)
			seed(t, st, "alpha", "beta")
			st.hook = func(p string) error {
				if p == point {
					return errors.New("injected")
				}
				return nil
			}
			if _, err := st.Rekey(); err == nil {
				t.Fatal("expected failure")
			}
			readable(t, st, "alpha", "beta")
			st.hook = nil
			seed(t, st, "delta")
			if _, err := st.Rekey(); err != nil {
				t.Fatalf("rerun: %v", err)
			}
			readable(t, st, "alpha", "beta", "delta")
		})
	}
}

func TestStrayKeyReusedByRerun(t *testing.T) {
	st, dir := openSecret(t)
	keyPath := filepath.Join(dir, KeyFileName)
	seed(t, st, "alpha")
	st.hook = func(p string) error {
		if p == pointMidTx {
			return errors.New("injected")
		}
		return nil
	}
	if _, err := st.Rekey(); err == nil {
		t.Fatal("expected failure")
	}
	restarted, err := New(storeFor(t, dir), keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := restarted.Rekey(); err != nil || v != 2 {
		t.Fatalf("Rekey = %d, %v (stray v2 should be reused)", v, err)
	}
	if hasFile(t, keyPath+".v3") {
		t.Fatal("minted extra key")
	}
}

func TestStartupKeepsOlderKeysUntilAllRowsMigrated(t *testing.T) {
	st, dir := openSecret(t)
	keyPath := filepath.Join(dir, KeyFileName)
	seed(t, st, "alpha")
	st.hook = func(p string) error {
		if p == pointMidTx {
			return errors.New("injected")
		}
		return nil
	}
	st.Rekey()
	if _, err := New(storeFor(t, dir), keyPath); err != nil {
		t.Fatal(err)
	}
	if !hasFile(t, keyPath+".v1") {
		t.Fatal("older key deleted while rows still need it")
	}
}

func TestConcurrentRekeyRefused(t *testing.T) {
	st, _ := openSecret(t)
	def := st.RekeyDefinition()
	if err := def.Steps[0].Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	other := st.RekeyDefinition()
	if err := other.Steps[0].Run(t.Context()); !errors.Is(err, ErrRekeyInProgress) {
		t.Fatalf("err = %v", err)
	}
	for _, step := range def.Steps[1:] {
		if err := step.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if st.RekeyInProgress() {
		t.Fatal("still claimed")
	}
}

func TestRekeyErrorsCarryNoKeyMaterial(t *testing.T) {
	st, dir := openSecret(t)
	seed(t, st, "alpha")
	v1, _ := os.ReadFile(filepath.Join(dir, KeyFileName+".v1"))
	st.hook = func(string) error { return errors.New("injected") }
	_, err := st.Rekey()
	if err == nil || strings.Contains(err.Error(), hex.EncodeToString(v1)) || strings.Contains(err.Error(), string(v1)) {
		t.Fatalf("err = %v", err)
	}
}

func TestRekeyDefinitionStepNames(t *testing.T) {
	st, _ := openSecret(t)
	def := st.RekeyDefinition()
	var names []string
	for _, s := range def.Steps {
		names = append(names, s.Name)
	}
	if def.Type != OperationType || strings.Join(names, ",") != "write-key,reencrypt,verify,retire-old" {
		t.Fatalf("def = %s %v", def.Type, names)
	}
}

func TestRefusesMintingKeyOverExistingRows(t *testing.T) {
	st, dir := openSecret(t)
	seed(t, st, "alpha")
	keyPath := filepath.Join(dir, KeyFileName)
	for _, f := range []string{keyPath, keyPath + ".v1"} {
		os.Remove(f)
	}
	_, err := New(storeFor(t, dir), keyPath)
	if err == nil || !strings.Contains(err.Error(), keyPath) || !strings.Contains(err.Error(), "recovery") {
		t.Fatalf("err = %v", err)
	}
	if hasFile(t, keyPath+".v1") {
		t.Fatal("minted a key over existing rows")
	}
}

func TestExposedLegacyKeyWithoutV1Refused(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, KeyFileName)
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(storeFor(t, dir), keyPath); err == nil || !strings.Contains(err.Error(), "readable") {
		t.Fatalf("err = %v", err)
	}
	if hasFile(t, keyPath+".v1") {
		t.Fatal("v1 written from exposed legacy key")
	}
}

func TestWriteKeyFileFailureInjection(t *testing.T) {
	boom := errors.New("injected")
	for name, set := range map[string]func(){
		"rename": func() { renameFile = func(string, string) error { return boom } },
		"fsync":  func() { syncFile = func(*os.File) error { return boom } },
	} {
		t.Run(name, func(t *testing.T) {
			origRename, origSync := renameFile, syncFile
			defer func() { renameFile, syncFile = origRename, origSync }()
			dir := t.TempDir()
			path := filepath.Join(dir, "k.v2")
			set()
			if err := writeKeyFile(path, [32]byte{1}); !errors.Is(err, boom) {
				t.Fatalf("err = %v", err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("leftover files: %v", entries)
			}
		})
	}
}

func TestRekeyKeyVersionReportsRowsAfterFailure(t *testing.T) {
	st, _ := openSecret(t)
	seed(t, st, "alpha")
	st.hook = func(p string) error {
		if p == pointMidTx {
			return errors.New("injected")
		}
		return nil
	}
	if _, err := st.Rekey(); err == nil {
		t.Fatal("expected failure")
	}
	if st.KeyVersion() != 1 {
		t.Fatalf("KeyVersion = %d after failed re-key, rows are on 1", st.KeyVersion())
	}
	st.hook = nil
	if v, err := st.Rekey(); err != nil || v != 2 || st.KeyVersion() != 2 {
		t.Fatalf("Rekey = %d, %v, KeyVersion %d", v, err, st.KeyVersion())
	}
}

func TestRerunAfterFailedRetireDoesNotMintKey(t *testing.T) {
	st, dir := openSecret(t)
	keyPath := filepath.Join(dir, KeyFileName)
	seed(t, st, "alpha")
	st.hook = func(p string) error {
		if p == pointOldDelete {
			return errors.New("injected")
		}
		return nil
	}
	if _, err := st.Rekey(); err == nil {
		t.Fatal("expected failure")
	}
	st.hook = nil
	if v, err := st.Rekey(); err != nil || v != 2 {
		t.Fatalf("rerun = %d, %v", v, err)
	}
	if hasFile(t, keyPath+".v3") || hasFile(t, keyPath+".v1") || !hasFile(t, keyPath+".v2") {
		t.Fatal("rerun should only retire v1")
	}
	readable(t, st, "alpha")
}

func TestCancelledRekeyReleasesLock(t *testing.T) {
	st, dir := openSecret(t)
	seed(t, st, "alpha", "beta")
	runner := operation.NewRunner(storeFor(t, dir))
	reached, release := make(chan struct{}), make(chan struct{})
	st.hook = func(p string) error {
		if p == pointMidTx {
			close(reached)
			<-release
		}
		return nil
	}
	op, err := runner.Start(context.Background(), st.RekeyDefinition(), "")
	if err != nil {
		t.Fatal(err)
	}
	<-reached
	if err := runner.Cancel(op.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for st.RekeyInProgress() {
		if time.Now().After(deadline) {
			t.Fatal("re-key lock stuck after cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	st.hook = nil
	readable(t, st, "alpha", "beta")
	if _, err := st.Rekey(); err != nil {
		t.Fatalf("rerun after cancel: %v", err)
	}
	readable(t, st, "alpha", "beta")
}

func TestConcurrentSetGetDuringRekey(t *testing.T) {
	st, _ := openSecret(t)
	seed(t, st, "alpha", "beta")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 16)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("worker%d", i)
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				val := fmt.Sprintf("v%d", n)
				if err := st.Set(name, val); err != nil {
					errs <- err
					return
				}
				if got, err := st.Get(name); err != nil || got != val {
					errs <- fmt.Errorf("Get(%s) = %q, %v", name, got, err)
					return
				}
				if got, err := st.Get("alpha"); err != nil || got != "value-of-alpha" {
					errs <- fmt.Errorf("Get(alpha) = %q, %v", got, err)
					return
				}
			}
		}(i)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.Rekey(); err != nil {
			t.Errorf("Rekey: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	readable(t, st, "alpha", "beta")
}
