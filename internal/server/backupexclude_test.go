package server

import (
	"strings"
	"testing"

	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/storage"
)

func dbManifest(withExport bool) catalog.Manifest {
	m := catalog.Manifest{Name: "demo", Services: []catalog.Service{{
		Name: "db",
		Data: []catalog.DataPath{
			{Target: "/var/lib/postgresql/data", Class: storage.ClassApplication, Subpath: "postgres", Database: true},
			{Target: "/media", Class: storage.ClassApplication, Subpath: "media"},
			{Target: "/cache", Ephemeral: true, Database: true, Reason: "scratch"},
		},
	}}}
	if withExport {
		m.Database = &catalog.DatabaseBackup{Service: "db", Kind: catalog.DatabasePostgres, Database: "demo", Username: "demo"}
	}
	return m
}

func TestDeclaredExportExcludesDatabaseDirectory(t *testing.T) {
	mounts := map[storage.Class]string{storage.ClassApplication: "/mnt/app"}
	sources := []backup.Source{{Mountpoint: "/mnt/app", Path: "demo", Class: storage.ClassApplication}}
	got, notes := applyDatabaseHandling(dbManifest(true), mounts, sources)
	if len(got[0].Exclude) != 1 || got[0].Exclude[0] != "postgres" {
		t.Fatalf("exclude = %v", got[0].Exclude)
	}
	if len(sources[0].Exclude) != 0 {
		t.Fatal("input sources mutated")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "export is authoritative") {
		t.Fatalf("notes = %v", notes)
	}
	if err := got[0].Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUndeclaredExportCopiesAndWarnsNotCrashConsistent(t *testing.T) {
	mounts := map[storage.Class]string{storage.ClassApplication: "/mnt/app"}
	sources := []backup.Source{{Mountpoint: "/mnt/app", Path: "demo", Class: storage.ClassApplication}}
	got, notes := applyDatabaseHandling(dbManifest(false), mounts, sources)
	if len(got[0].Exclude) != 0 {
		t.Fatalf("excluded without an export: %v", got[0].Exclude)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "not crash-consistent") {
		t.Fatalf("notes = %v", notes)
	}
}

func TestExclusionOnlyAppliesToSourceOnThatMount(t *testing.T) {
	mounts := map[storage.Class]string{storage.ClassApplication: "/mnt/app", storage.ClassBulk: "/mnt/bulk"}
	sources := []backup.Source{{Mountpoint: "/mnt/bulk", Path: "demo", Class: storage.ClassBulk}}
	got, notes := applyDatabaseHandling(dbManifest(true), mounts, sources)
	if len(got[0].Exclude) != 0 || len(notes) != 0 {
		t.Fatalf("bulk source touched: %v %v", got[0].Exclude, notes)
	}
}
