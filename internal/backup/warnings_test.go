package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJobManifestRecordsWarnings(t *testing.T) {
	mount := t.TempDir()
	dest := t.TempDir()
	job := scheduleJob(t, mount, dest)
	job.Warnings = []string{"image volume /cache is not backed up"}
	if err := os.MkdirAll(filepath.Join(dest, "photos", "20261008T120000Z"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeJobManifest(job, "20261008T120000Z"); err != nil {
		t.Fatalf("writeJobManifest: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dest, "photos", "20261008T120000Z", "backup.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "image volume /cache is not backed up") {
		t.Fatalf("manifest missing warning:\n%s", raw)
	}
}
