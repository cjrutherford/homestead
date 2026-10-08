package catalog

import (
	"strings"
	"testing"
)

func TestDatabaseBackupValidation(t *testing.T) {
	services := map[string]bool{"db": true, "web": true}
	sqlite := DatabaseBackup{Service: "web", Kind: DatabaseSQLite, Path: "data/app.db"}
	if err := sqlite.Validate(services); err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	postgres := DatabaseBackup{Service: "db", Kind: DatabasePostgres, Database: "app", Username: "app"}
	if err := postgres.Validate(services); err != nil {
		t.Fatalf("postgres: %v", err)
	}
	for _, decl := range []DatabaseBackup{
		{Service: "ghost", Kind: DatabaseSQLite, Path: "x.db"},
		{Service: "web", Kind: "mysql", Path: "x.db"},
		{Service: "web", Kind: DatabaseSQLite, Path: "/abs.db"},
		{Service: "web", Kind: DatabaseSQLite, Path: "x.db", Database: "app"},
		{Service: "web", Kind: DatabasePostgres, Database: "9bad", Username: "app"},
		{Service: "web", Kind: DatabasePostgres, Database: "app"},
		{Service: "web", Kind: DatabasePostgres, Database: "app", Username: "app", Path: "x.db"},
	} {
		if err := decl.Validate(services); err == nil {
			t.Fatalf("accepted %+v", decl)
		}
	}
}

func TestBackupHookValidation(t *testing.T) {
	services := map[string]bool{"web": true}
	hook := BackupHook{Stage: HookPre, Service: "web", Command: []string{"sqlite3", ".wal-checkpoint"}, Timeout: 60}
	if err := hook.Validate(services); err != nil {
		t.Fatalf("hook: %v", err)
	}
	bare := BackupHook{Stage: HookPost, Service: "web", Command: []string{"true"}}
	if err := bare.Validate(services); err != nil {
		t.Fatalf("bare: %v", err)
	}
	for _, hook := range []BackupHook{
		{Stage: "during", Service: "web", Command: []string{"true"}},
		{Stage: HookPre, Service: "ghost", Command: []string{"true"}},
		{Stage: HookPre, Service: "web"},
		{Stage: HookPre, Service: "web", Command: []string{""}},
		{Stage: HookPre, Service: "web", Command: []string{"true"}, Timeout: 2},
	} {
		if err := hook.Validate(services); err == nil {
			t.Fatalf("accepted %+v", hook)
		}
	}
}

func TestManifestBackupContract(t *testing.T) {
	manifest := validManifest()
	manifest.Database = &DatabaseBackup{Service: "jellyfin", Kind: DatabaseSQLite, Path: "data/library.db"}
	manifest.Hooks = []BackupHook{
		{Stage: HookPre, Service: "jellyfin", Command: []string{"sh", "-c", "true"}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	bad := validManifest()
	bad.Database = &DatabaseBackup{Service: "ghost", Kind: DatabaseSQLite, Path: "x.db"}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("err = %v", err)
	}
	badHooks := validManifest()
	badHooks.Hooks = []BackupHook{{Stage: HookPre, Service: "ghost", Command: []string{"true"}}}
	if err := badHooks.Validate(); err == nil || !strings.Contains(err.Error(), "hook #0") {
		t.Fatalf("err = %v", err)
	}
	// Existing manifests without backup contracts keep validating.
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("backward compat: %v", err)
	}
}
