package release

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"homestead/internal/store"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewStore(st)
}

func TestTrustKeyLifecycle(t *testing.T) {
	s := openStore(t)
	keys, _ := s.Keys()
	if len(keys) != 1 || keys[0].Source != SourceEmbedded {
		t.Fatalf("default keys = %+v", keys)
	}
	a, b := newTestSigner(t), newTestSigner(t)
	added, err := s.AddKey(a.pubText(), "mine", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddKey(a.pubText(), "dup", "owner"); err != ErrKeyExists {
		t.Fatalf("dup err = %v", err)
	}
	if _, err := s.ReplaceKey("0000000000000000", b.pubText(), "", "owner"); err != ErrKeyNotFound {
		t.Fatalf("replace missing err = %v", err)
	}
	replaced, err := s.ReplaceKey(added.KeyID, b.pubText(), "rotated", "owner")
	if err != nil || replaced.KeyID == added.KeyID {
		t.Fatalf("replace: %v %+v", err, replaced)
	}
	// The owner may remove the embedded key and restore it later.
	emb := platformClass.embedded()
	if err := s.RemoveKey(emb.KeyID); err != nil {
		t.Fatal(err)
	}
	keys, _ = s.Keys()
	if len(keys) != 1 || keys[0].KeyID != replaced.KeyID {
		t.Fatalf("after removal = %+v", keys)
	}
	if err := s.RemoveKey(emb.KeyID); err != ErrKeyNotFound {
		t.Fatalf("second remove = %v", err)
	}
	if _, err := s.AddKey(EmbeddedPublicKey, "", "owner"); err != nil {
		t.Fatal(err)
	}
	keys, _ = s.Keys()
	if len(keys) != 2 {
		t.Fatalf("after restore = %+v", keys)
	}
	if _, err := s.AddKey("garbage", "", "owner"); err == nil {
		t.Fatal("garbage key accepted")
	}
}

func TestRenderYAML(t *testing.T) {
	s := openStore(t)
	a := newTestSigner(t)
	if _, err := s.AddKey(a.pubText(), "mine", "owner"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "platform-trust.yaml")
	if err := s.RenderYAML(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.Contains(text, "trusted_keys:") || !strings.Contains(text, a.parsed(t).KeyID) || !strings.Contains(text, "source: embedded") {
		t.Fatalf("yaml:\n%s", text)
	}
}

func TestInstalledAndReleases(t *testing.T) {
	s := openStore(t)
	if in, _ := s.Installed("homestead-server"); in.Counter != 0 {
		t.Fatalf("installed = %+v", in)
	}
	if err := s.RecordInstalled("homestead-server", "0.2.0", 5); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.Installed("homestead-server"); in.Counter != 5 || in.Version != "0.2.0" {
		t.Fatalf("installed = %+v", in)
	}
	if err := s.SaveRelease(Release{Component: "homesteadctl", State: StateVerified, Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	rels, _ := s.Releases()
	if len(rels) != 1 || rels[0].State != StateVerified {
		t.Fatalf("releases = %+v", rels)
	}
}

func TestSeedInstalledNeverOverwrites(t *testing.T) {
	s := openStore(t)
	ok, err := s.SeedInstalled("homestead-server", "1.0.0", 4)
	if err != nil || !ok {
		t.Fatalf("first seed = %v, %v", ok, err)
	}
	if ok, err := s.SeedInstalled("homestead-server", "9.0.0", 99); err != nil || ok {
		t.Fatalf("second seed = %v, %v; must not overwrite", ok, err)
	}
	if in, _ := s.Installed("homestead-server"); in.Version != "1.0.0" || in.Counter != 4 {
		t.Fatalf("installed = %+v", in)
	}
}

func TestEmbeddedKeyIsMarkedPlaceholder(t *testing.T) {
	s := openStore(t)
	keys, _ := s.Keys()
	if len(keys) != 1 || !keys[0].Placeholder {
		t.Fatalf("keys = %+v", keys)
	}
	owner, err := s.AddKey(newTestSigner(t).pubText(), "mine", "owner")
	if err != nil || owner.Placeholder {
		t.Fatalf("owner key = %+v, %v", owner, err)
	}
	path := filepath.Join(t.TempDir(), "trust.yaml")
	if err := s.RenderYAML(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "placeholder: true") {
		t.Fatalf("yaml lacks placeholder marker:\n%s", data)
	}
}

// Concurrent adds of the same key: exactly one wins, the rest are 409s
// (ErrKeyExists), none is a generic failure.
func TestAddKeyRaceIsConflict(t *testing.T) {
	s := openStore(t)
	text := newTestSigner(t).pubText()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.AddKey(text, "race", "owner")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		switch err {
		case nil:
			wins++
		case ErrKeyExists:
		default:
			t.Errorf("racing add returned %v, want nil or ErrKeyExists", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want 1", wins)
	}
}

func TestBuildTimeReleaseKeyReplacesPlaceholder(t *testing.T) {
	plain := newPlatformClass("").embedded()
	if !plain.Placeholder || plain.KeyID == "" {
		t.Fatalf("plain build = %+v", plain)
	}
	signer := newTestSigner(t)
	line := strings.Split(strings.TrimSpace(signer.pubText()), "\n")
	prod := newPlatformClass(line[len(line)-1]).embedded()
	if prod.Placeholder || prod.KeyID != signer.parsed(t).KeyID || prod.Label != productionKeyLabel || prod.Source != SourceEmbedded {
		t.Fatalf("production build = %+v", prod)
	}
}

// TestRotationEndToEnd follows the owner procedure: trust the new key
// beside the old one, accept releases signed by either, then retire the
// old key so only the new one verifies.
func TestRotationEndToEnd(t *testing.T) {
	s := openStore(t)
	oldKey, newKey := newTestSigner(t), newTestSigner(t)
	if _, err := s.AddKey(oldKey.pubText(), "old", "owner"); err != nil {
		t.Fatal(err)
	}
	verifyWith := func(sg *testSigner) error {
		keys, err := s.Keys()
		if err != nil {
			t.Fatal(err)
		}
		var parsed []PublicKey
		for _, k := range keys {
			p, err := ParsePublicKey(k.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			parsed = append(parsed, p)
		}
		_, err = Verify(sg.fixture(t, "1.0.0", 5).inputs(parsed...))
		return err
	}
	if err := verifyWith(oldKey); err != nil {
		t.Fatalf("old key before rotation: %v", err)
	}
	if _, err := s.AddKey(newKey.pubText(), "new", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := verifyWith(oldKey); err != nil {
		t.Fatalf("old key while both trusted: %v", err)
	}
	if err := verifyWith(newKey); err != nil {
		t.Fatalf("new key while both trusted: %v", err)
	}
	if err := s.RemoveKey(oldKey.parsed(t).KeyID); err != nil {
		t.Fatal(err)
	}
	if err := verifyWith(newKey); err != nil {
		t.Fatalf("new key after retiring old: %v", err)
	}
	if err := verifyWith(oldKey); err == nil {
		t.Fatal("retired key still verifies")
	}
}
