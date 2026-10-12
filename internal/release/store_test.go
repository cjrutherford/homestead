package release

import (
	"os"
	"path/filepath"
	"strings"
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
	emb := embeddedKey()
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
