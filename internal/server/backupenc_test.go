package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/doctor"
	"homestead/internal/secret"
	"homestead/internal/storage"
)

func TestBackupEncryptionDoctorWarnsWhileOnlyApplianceRecipient(t *testing.T) {
	env := newRecipientEnv(t)
	srv := New("127.0.0.1:0")
	srv.MountDoctor(backupEncryptionDoctorCheck(env.st))
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/doctor", nil)
	body := rec.Body.String()
	if !strings.Contains(body, `"backup-encryption"`) || !strings.Contains(body, "appliance identity") {
		t.Fatalf("doctor body = %s", body)
	}
	checks := backupEncryptionDoctorCheck(env.st)()
	if len(checks) != 1 || checks[0].Status != doctor.StatusWarn {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestStackSecretsSourceIsSensitiveWhenDirExists(t *testing.T) {
	root := t.TempDir()
	old := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(root, name) }
	t.Cleanup(func() { stackDirFor = old })
	if _, ok := stackSecretsSource("demo"); ok {
		t.Fatal("source reported without a secrets directory")
	}
	if err := os.MkdirAll(filepath.Join(root, "demo", catalog.SecretsDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	source, ok := stackSecretsSource("demo")
	if !ok || !source.Sensitive || source.Mountpoint != root || source.Path != "demo/secrets" {
		t.Fatalf("source = %+v ok=%v", source, ok)
	}
	if err := source.Validate(); err != nil {
		t.Fatal(err)
	}
}

// The S277 pre-migration volume copy is encrypted to the appliance
// identity, and the identity never reaches the owner-facing plan.
func TestPreMigrationBackupIsEncrypted(t *testing.T) {
	env := newMigrationEnv(t)
	bk := filepath.Join(filepath.Dir(env.mount), "bk")
	if err := os.MkdirAll(bk, 0o755); err != nil {
		t.Fatal(err)
	}
	discovered := discover.Storage{Mounts: []discover.Mount{
		{Mountpoint: env.mount, FSType: "ext4", Accessible: true, FreeBytes: env.free},
		{Mountpoint: bk, FSType: "ext4", Accessible: true, FreeBytes: env.free},
	}}
	discoverStorage = func() (discover.Storage, error) { return discovered, nil }
	registry := storage.NewRegistry(env.st)
	if _, err := registry.Register(bk, "bk", "test", discovered); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.NewAssignments(env.st).Assign("backup", bk, "test", registry, discovered, 0); err != nil {
		t.Fatal(err)
	}
	secrets, err := secret.New(env.st, filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	h := &migrationHandlers{st: env.st, secrets: secrets}
	entries, err := catalog.Load(filepath.Join(filepath.Dir(env.mount), "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := entries.Get("demo")
	plan := catalog.MigrationPlan{OldVolumes: []catalog.OldVolume{{Name: "vol1", Source: env.vol, SizeBytes: 10}}}
	job, availability := h.preMigrationBackup(manifest, plan)
	if !availability.Possible || len(job.Recipients) != 1 {
		t.Fatalf("availability = %+v recipients = %d", availability, len(job.Recipients))
	}
	identity, err := secrets.Get(backup.IdentitySecret)
	if err != nil || identity == "" {
		t.Fatalf("identity not stored: %v", err)
	}
	if strings.Contains(availability.Reason, identity) || strings.Contains(availability.Destination, identity) {
		t.Fatal("identity leaked into availability")
	}
	// Owner-facing secret listings carry names, never the identity.
	list, _ := secrets.List()
	for _, info := range list {
		if strings.Contains(info.Name, "AGE-SECRET-KEY") {
			t.Fatal("identity in listing")
		}
	}
}
