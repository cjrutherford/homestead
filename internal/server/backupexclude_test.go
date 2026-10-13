package server

import (
	"errors"
	"fmt"
	"homestead/internal/scheduler"
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

func TestDestinationErrorClassification(t *testing.T) {
	unavailable := fmt.Errorf("%w: backup: destination %q is read-only", backup.ErrDestinationUnavailable, "/mnt/b")
	if !errors.Is(classifyDestinationError(unavailable), scheduler.ErrUnavailable) {
		t.Fatal("an unavailable destination must be retryable")
	}
	config := errors.New("backup: no backup class assignment: not found")
	if errors.Is(classifyDestinationError(config), scheduler.ErrUnavailable) {
		t.Fatal("a configuration fault must not be retryable")
	}
}

func TestExclusionOnlyForDirectoriesTheExportCovers(t *testing.T) {
	m := catalog.Manifest{Name: "demo", Services: []catalog.Service{
		{Name: "db", Data: []catalog.DataPath{{Target: "/pg", Class: storage.ClassApplication, Subpath: "postgres", Database: true}}},
		{Name: "cache", Data: []catalog.DataPath{{Target: "/redis", Class: storage.ClassApplication, Subpath: "redis", Database: true}}},
		{Name: "app", Data: []catalog.DataPath{
			{Target: "/data", Class: storage.ClassApplication, Subpath: "data", Database: true},
			{Target: "/other", Class: storage.ClassApplication, Subpath: "other", Database: true},
		}},
	}, Database: &catalog.DatabaseBackup{Service: "db", Kind: catalog.DatabasePostgres, Database: "demo", Username: "demo"}}
	mounts := map[storage.Class]string{storage.ClassApplication: "/mnt/app"}
	sources := []backup.Source{{Mountpoint: "/mnt/app", Path: "demo", Class: storage.ClassApplication}}
	got, notes := applyDatabaseHandling(m, mounts, sources)
	if len(got[0].Exclude) != 1 || got[0].Exclude[0] != "postgres" {
		t.Fatalf("exclude = %v, want only the exported database", got[0].Exclude)
	}
	crash := 0
	for _, n := range notes {
		if strings.Contains(n, "not crash-consistent") {
			crash++
		}
	}
	if crash != 3 {
		t.Fatalf("notes = %v, want a warning for each uncovered directory", notes)
	}

	m.Database = &catalog.DatabaseBackup{Service: "app", Kind: catalog.DatabaseSQLite, Path: "data/app.db"}
	got, _ = applyDatabaseHandling(m, mounts, sources)
	if len(got[0].Exclude) != 1 || got[0].Exclude[0] != "data" {
		t.Fatalf("sqlite exclude = %v, want only data", got[0].Exclude)
	}
}
