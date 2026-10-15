package craft

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodManifest = `format: homestead-craft-manifest/1
counter: 3
context_digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
apps:
  - name: wordcraft
    version: 1.2.3
    artifact:
      name: wordcraft-1.2.3.tar.gz
      urls:
        - https://example.invalid/w.tar.gz
      sha256: 1111111111111111111111111111111111111111111111111111111111111111
      size: 10
`

func TestParseManifestStrict(t *testing.T) {
	if _, err := ParseManifest([]byte(goodManifest)); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"unknown field":      strings.Replace(goodManifest, "counter: 3", "counter: 3\nextra: 1", 1),
		"duplicate counter":  strings.Replace(goodManifest, "counter: 3", "counter: 3\ncounter: 4", 1),
		"wrong format":       strings.Replace(goodManifest, "manifest/1", "manifest/2", 1),
		"zero counter":       strings.Replace(goodManifest, "counter: 3", "counter: 0", 1),
		"http url":           strings.Replace(goodManifest, "https://", "http://", 1),
		"url credentials":    strings.Replace(goodManifest, "https://", "https://u:p@", 1),
		"path artifact name": strings.Replace(goodManifest, "wordcraft-1.2.3.tar.gz", "../w.tar.gz", 1),
		"bad extension":      strings.Replace(goodManifest, "wordcraft-1.2.3.tar.gz", "w.exe", 1),
		"short digest":       strings.Replace(goodManifest, "1111111111111111111111111111111111111111111111111111111111111111", "abc", 1),
		"bad context digest": strings.Replace(goodManifest, "sha256:0000", "md5:0000", 1),
		"bad version":        strings.Replace(goodManifest, "1.2.3\n    artifact", "latest\n    artifact", 1),
		"zero size":          strings.Replace(goodManifest, "size: 10", "size: 0", 1),
		"second document":    goodManifest + "---\nformat: x\n",
		"no apps":            "format: homestead-craft-manifest/1\ncounter: 1\ncontext_digest: sha256:" + strings.Repeat("0", 64) + "\napps: []\n",
		"duplicate app":      goodManifest + strings.SplitN(goodManifest, "apps:\n", 2)[1],
	}
	for name, text := range cases {
		if _, err := ParseManifest([]byte(text)); !errors.Is(err, ErrManifest) {
			t.Errorf("%s: err = %v, want ErrManifest", name, err)
		}
	}
}

func TestParseRef(t *testing.T) {
	if app, v, ok := ParseRef("homestead/craft-wordcraft:1.2.3"); !ok || app != "wordcraft" || v != "1.2.3" {
		t.Fatal("valid ref rejected")
	}
	for _, bad := range []string{"nginx:1.0", "registry.example/homestead/craft-wordcraft:1.0", "homestead/craft-wordcraft:latest", "homestead/craft-:1.0", "homestead/craft-Word:1.0", "homestead/craft-w"} {
		if _, _, ok := ParseRef(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if Tag("w", "1.0") != "homestead/craft-w:1.0" {
		t.Fatal("tag")
	}
}

func TestTreeDigestStable(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b.txt", "b")
	write("a/z.txt", "z")
	first, err := TreeDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Modes and empty directories do not matter; content and names do.
	_ = os.Chmod(filepath.Join(dir, "b.txt"), 0o755)
	_ = os.Mkdir(filepath.Join(dir, "empty"), 0o755)
	if again, _ := TreeDigest(dir); again != first {
		t.Fatal("digest depends on modes or empty dirs")
	}
	write("b.txt", "c")
	if changed, _ := TreeDigest(dir); changed == first {
		t.Fatal("digest ignores content")
	}
}
