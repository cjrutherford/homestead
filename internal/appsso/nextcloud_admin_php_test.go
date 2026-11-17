package appsso

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/testtools"
)

// The homestead_sso Nextcloud app is PHP written into a container at
// install time; nothing else parses it before a live run (S506).
func TestNextcloudAdminAppIsValidPHP(t *testing.T) {
	php := testtools.SkipUnlessTool(t, "php")
	dir := t.TempDir()
	n := 0
	for _, f := range nextcloudAdminFiles {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.Body), 0o644); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(f.Path, ".php") {
			continue
		}
		n++
		if out, err := exec.Command(php, "-l", path).CombinedOutput(); err != nil {
			t.Errorf("php -l %s: %v\n%s", f.Path, err, out)
		}
	}
	if n == 0 {
		t.Fatal("no PHP files in the app")
	}
}
