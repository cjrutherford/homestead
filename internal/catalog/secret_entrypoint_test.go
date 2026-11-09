package catalog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/exposure"
	"homestead/internal/storage"
)

// exportingManifest is a minimal package whose one service has no
// `*_FILE` input, so it needs the supervisor (S342).
func exportingManifest() Manifest {
	return Manifest{
		Name: "paperless-ngx", Version: "2.15.1",
		Title: "Paperless", Blurb: "Searchable household archive.",
		Description: "Scan, OCR and index household documents.",
		Category:    "documents",
		Icon:        "mdi:file",
		Services: []Service{
			{
				Name: "webserver", Image: "ghcr.io/paperless-ngx/paperless-ngx:2.15.1", Exposes: "web",
				Entrypoint: []string{"/sbin/docker-entrypoint.sh"},
				Command:    []string{"gunicorn", "-c", "/usr/src/paperless/gunicorn.conf.py"},
			},
		},
		Requirements: Requirements{
			MemoryMB: 2048,
			Storage:  []StorageNeed{{Class: storage.ClassApplication, MinBytes: 1 << 30}},
			Ports:    []int{8000},
		},
		Exposure: exposure.PackageExposure{
			Package:   "paperless-ngx",
			Endpoints: []exposure.EndpointExposure{{Endpoint: "web", Recommended: "local"}},
		},
		BackupPaths: []string{"data", "media"},
		Secrets: []SecretDecl{
			{
				Name: "db_password",
				Consumers: []SecretConsumer{
					{Service: "webserver", Env: "PAPERLESS_DBPASS_FILE", Export: true},
				},
			},
		},
	}
}

func TestExportRequiresEntrypoint(t *testing.T) {
	m := exportingManifest()
	m.Services[0].Entrypoint = nil
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "declares no entrypoint") {
		t.Fatalf("err = %v, want missing-entrypoint rejection", err)
	}
}

func TestExportRequiresFileSuffix(t *testing.T) {
	m := exportingManifest()
	m.Secrets[0].Consumers[0].Env = "PAPERLESS_DBPASS"
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "*_FILE") {
		t.Fatalf("err = %v, want *_FILE rejection", err)
	}
}

func TestExportRejectsPlainCollision(t *testing.T) {
	m := exportingManifest()
	m.Services[0].Environment = map[string]string{"PAPERLESS_DBPASS": "plain"}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "also sets it plainly") {
		t.Fatalf("err = %v, want plain-collision rejection", err)
	}
}

func TestExportRejectsDuplicateTarget(t *testing.T) {
	m := exportingManifest()
	m.Secrets = append(m.Secrets, SecretDecl{
		Name:      "db_password2",
		Consumers: []SecretConsumer{{Service: "webserver", Env: "PAPERLESS_DBPASS_FILE", Export: true}},
	})
	// Two declarations cannot share one Env, and the first check catches
	// that; the duplicate-target check applies when the carrier names
	// differ but the exported variable does not.
	m.Secrets[1].Consumers[0].Env = "PAPERLESS_DBPASS_FILE"
	err := m.Validate()
	if err == nil {
		t.Fatal("want a rejection")
	}
}

func TestGenerateSupervisesExportingService(t *testing.T) {
	m := exportingManifest()
	files, err := Generate(m, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"entrypoint:",
		"- " + EntrypointName,
		"./" + EntrypointFile("webserver"),
		"PAPERLESS_DBPASS_FILE: /run/secrets/webserver_db_password",
	} {
		if !strings.Contains(files.ComposeYAML, want) {
			t.Fatalf("compose missing %q:\n%s", want, files.ComposeYAML)
		}
	}
	if !strings.Contains(files.ComposeYAML, "gunicorn") {
		t.Fatalf("original command lost:\n%s", files.ComposeYAML)
	}
	if strings.Contains(files.ComposeYAML, "PAPERLESS_DBPASS:") {
		t.Fatalf("secret value variable must not be a plain environment entry:\n%s", files.ComposeYAML)
	}
	if len(files.EntrypointScripts) != 1 || files.EntrypointScripts[0].Service != "webserver" {
		t.Fatalf("scripts = %+v", files.EntrypointScripts)
	}
}

func TestSupervisorScriptExportsAndExecs(t *testing.T) {
	m := exportingManifest()
	dir := t.TempDir()
	sec := newMemSecrets(t)
	sec.values[SecretStoreName("paperless-ngx", "db_password")] = "s3cret-value"
	files, err := Generate(m, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteStack(dir, files, sec); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, EntrypointFile("webserver"))
	info, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
	body, _ := os.ReadFile(script)
	if strings.Contains(string(body), "s3cret") {
		t.Fatal("script must never hold a value")
	}

	// Run the script against a fixture secret and a fake command that
	// prints what the supervisor exported.
	secretsDir := t.TempDir()
	secretPath := filepath.Join(secretsDir, "webserver_db_password")
	if err := os.WriteFile(secretPath, []byte("s3cret-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved := strings.NewReplacer(
		secretContainerPath(secretKey("webserver", "db_password")), secretPath,
	).Replace(string(body))
	run := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(run, []byte(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", run, "/bin/sh", "-c", "printf '%s' \"$PAPERLESS_DBPASS\"; printf '|%s' \"${PAPERLESS_DBPASS_FILE-unset}\"").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v: %s", err, out)
	}
	if got := string(out); got != "s3cret-value|unset" {
		t.Fatalf("got %q, want exported value and dropped path variable", got)
	}
}

func TestSupervisorFailsClosedOnMissingFile(t *testing.T) {
	m := exportingManifest()
	dir := t.TempDir()
	sec := newMemSecrets(t)
	sec.values[SecretStoreName("paperless-ngx", "db_password")] = "s3cret-value"
	files, err := Generate(m, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteStack(dir, files, sec); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, EntrypointFile("webserver")))
	missing := filepath.Join(t.TempDir(), "absent")
	moved := strings.NewReplacer(
		secretContainerPath(secretKey("webserver", "db_password")), missing,
	).Replace(string(body))
	run := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(run, []byte(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", run, "/bin/true").CombinedOutput()
	if err == nil {
		t.Fatalf("want failure, got success: %s", out)
	}
	if !strings.Contains(string(out), "missing secret file for PAPERLESS_DBPASS") {
		t.Fatalf("stderr = %q, want a named missing-secret error", out)
	}
}

func TestManifestsWithoutExportGetNoSupervisor(t *testing.T) {
	m := authentikManifest(t)
	files, err := Generate(m, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files.EntrypointScripts) != 0 {
		t.Fatalf("scripts = %+v, want none", files.EntrypointScripts)
	}
	if strings.Contains(files.ComposeYAML, EntrypointName) {
		t.Fatalf("unexpected supervisor mount:\n%s", files.ComposeYAML)
	}
}
