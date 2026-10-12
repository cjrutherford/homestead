package backup

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Applications ship relative links inside their code trees (Nextcloud
// .license files); copyTree keeps them as links, never following them,
// and still refuses links that leave the tree.
func TestCopyTreeKeepsContainedRelativeSymlinks(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(src, "js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "js", "a.js.license"), []byte("MIT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.js.license", filepath.Join(src, "js", "a.js.map.license")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst")
	files, err := copyTree(src, dst)
	if err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if files != 2 {
		t.Fatalf("files = %d, want 2", files)
	}
	link, err := os.Readlink(filepath.Join(dst, "js", "a.js.map.license"))
	if err != nil || link != "a.js.license" {
		t.Fatalf("Readlink = %q, %v", link, err)
	}
	want, err := treeSignature(src)
	if err != nil {
		t.Fatalf("treeSignature src: %v", err)
	}
	got, err := treeSignature(dst)
	if err != nil || len(got) != len(want) {
		t.Fatalf("treeSignature dst = %v, %v; want %v", got, err, want)
	}
}

func TestCopyTreeRefusesEscapingSymlinks(t *testing.T) {
	for name, link := range map[string]string{"absolute": "/etc", "parent": "../outside"} {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "src")
			if err := os.MkdirAll(src, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(link, filepath.Join(src, "l")); err != nil {
				t.Fatal(err)
			}
			_, err := copyTree(src, filepath.Join(t.TempDir(), "dst"))
			if err == nil || !strings.Contains(err.Error(), "refused") {
				t.Fatalf("err = %v, want refusal", err)
			}
		})
	}
}

// A root-run copy keeps the source owner (postgres data directories).
func TestCopyTreePreservesOwnerWhenRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(src, "f")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(file, 70, 70); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst")
	if _, err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dst, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if stat := info.Sys().(*syscall.Stat_t); stat.Uid != 70 || stat.Gid != 70 {
		t.Fatalf("owner = %d:%d, want 70:70", stat.Uid, stat.Gid)
	}
}
