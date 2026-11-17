package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/exposure"
	"homestead/internal/operation"
	"homestead/internal/store"

	_ "modernc.org/sqlite"
)

func scheduleManifest() catalog.Manifest {
	return catalog.Manifest{
		Name: "photos", Version: "1.0.0", Title: "Photos", Blurb: "Photos.",
		Description: "Photos.", Category: "media", Icon: "mdi:image",
		Services:     []catalog.Service{{Name: "db", Image: "example/db:1.0.0"}},
		Requirements: catalog.Requirements{MemoryMB: 64},
		Exposure: exposure.PackageExposure{Package: "photos", Endpoints: []exposure.EndpointExposure{
			{Endpoint: "web", Recommended: "local"},
		}},
		Database: &catalog.DatabaseBackup{Service: "db", Kind: catalog.DatabaseSQLite, Path: "app.db"},
		Hooks: []catalog.BackupHook{
			{Stage: catalog.HookPre, Service: "db", Command: []string{"true"}},
		},
	}
}

func openSchedules(t *testing.T) *Schedules {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewSchedules(s)
}

func TestScheduleStore(t *testing.T) {
	schedules := openSchedules(t)
	saved, err := schedules.Save(Schedule{Package: "photos", IntervalSeconds: 86400, Retain: 7, Enabled: true, Actor: "owner"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Package != "photos" || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved = %+v", saved)
	}
	fetched, err := schedules.Get("photos")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched != saved {
		t.Fatalf("fetched = %+v, want %+v", fetched, saved)
	}
	listed, err := schedules.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed = %+v", listed)
	}
	if _, err := schedules.Save(Schedule{Package: "photos", IntervalSeconds: 60, Retain: 1, Actor: "owner"}); err == nil {
		t.Fatal("saved sub-hourly schedule")
	}
	if _, err := schedules.Save(Schedule{Package: "", IntervalSeconds: 3600, Actor: "owner"}); err == nil {
		t.Fatal("saved empty package")
	}
	if err := schedules.Delete("photos"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := schedules.Get("photos"); err == nil {
		t.Fatal("got deleted schedule")
	}
	if err := schedules.Delete("photos"); err != nil {
		t.Fatalf("re-delete: %v", err)
	}
}

func scheduleJob(t *testing.T, mount, dest string) Job {
	t.Helper()
	data := filepath.Join(mount, "photos", "config")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(data, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	dbDir := filepath.Join(mount, "photos", "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sqliteFixtureForJob(t, filepath.Join(dbDir, "app.db"))
	return Job{
		Manifest:  scheduleManifest(),
		Package:   "photos",
		Sources:   []Source{{Mountpoint: mount, Path: "photos/config"}},
		Container: "photos-db",
		SrcRoot:   filepath.Join(mount, "photos", "db"),
		DestRoot:  dest,
		Retain:    2,
	}
}

func sqliteFixtureForJob(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func openJobRunner(t *testing.T) *operation.Runner {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "run.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

func TestJobFlow(t *testing.T) {
	mount := t.TempDir()
	dest := t.TempDir()
	job := scheduleJob(t, mount, dest)
	stubExec(t, func(_ string, _ string, _ []string) (string, error) {
		return "ok", nil
	})
	def, err := JobDefinition(job, fixedJobTime)
	if err != nil {
		t.Fatalf("JobDefinition: %v", err)
	}
	if def.Type != "backup.job" || len(def.Steps) != 4 ||
		def.Steps[0].Name != "backup:photos/config" || def.Steps[1].Name != "export:db" ||
		def.Steps[2].Name != "write-manifest" || def.Steps[3].Name != "prune" {
		t.Fatalf("def = %+v", def)
	}
	runner := openJobRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	run := filepath.Join(dest, "photos", "20261008T120000Z")
	for _, name := range []string{"photos/config/settings.json", "app.db", "backup.json"} {
		if _, err := os.Stat(filepath.Join(run, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	// Retention keeps two: seed three old runs plus the fresh one.
	for _, stamp := range []string{"20261001T120000Z", "20261002T120000Z", "20261003T120000Z"} {
		if err := os.MkdirAll(filepath.Join(dest, "photos", stamp), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dest, "photos", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	hourLater := func() time.Time {
		return time.Date(2026, 10, 8, 13, 0, 0, 0, time.UTC)
	}
	def, err = JobDefinition(job, hourLater)
	if err != nil {
		t.Fatalf("JobDefinition: %v", err)
	}
	op, err = runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if done, err := runner.Wait(context.Background(), op.ID); err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("done = %+v %v", done, err)
	}
	entries, err := os.ReadDir(filepath.Join(dest, "photos"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var runs []string
	for _, entry := range entries {
		if entry.IsDir() {
			runs = append(runs, entry.Name())
		}
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %v", runs)
	}
	if _, err := os.Stat(filepath.Join(dest, "photos", "keep.txt")); err != nil {
		t.Fatal("prune ate foreign files")
	}
}

func TestJobValidateServerDatabasePassword(t *testing.T) {
	for _, kind := range []catalog.DatabaseKind{
		catalog.DatabasePostgres, catalog.DatabaseMariaDB, catalog.DatabaseMySQL,
	} {
		manifest := scheduleManifest()
		manifest.Database = &catalog.DatabaseBackup{
			Service: "db", Kind: kind, Database: "photos", Username: "photos",
		}
		job := Job{
			Manifest: manifest, Package: "photos",
			Container: "photos-db", SrcRoot: t.TempDir(), DestRoot: t.TempDir(),
		}
		if err := job.Validate(); err == nil ||
			!strings.Contains(err.Error(), string(kind)+" export needs a password") {
			t.Fatalf("%s without password: err = %v", kind, err)
		}
		job.Password = "secret"
		if err := job.Validate(); err != nil {
			t.Fatalf("%s with password: %v", kind, err)
		}
	}
}

func fixedJobTime() time.Time {
	return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
}

func TestJobValidation(t *testing.T) {
	fresh := func() (Job, string, string) {
		t.Helper()
		mount := t.TempDir()
		dest := t.TempDir()
		return scheduleJob(t, mount, dest), mount, dest
	}
	job, _, _ := fresh()
	job.Package = ""
	if _, err := JobDefinition(job, fixedJobTime); err == nil {
		t.Fatal("planned empty package")
	}
	job, _, _ = fresh()
	job.Manifest.Database = nil
	job.Sources = nil
	if _, err := JobDefinition(job, fixedJobTime); err == nil {
		t.Fatal("planned capturing nothing")
	}
	job, _, _ = fresh()
	job.Retain = -1
	if _, err := JobDefinition(job, fixedJobTime); err == nil {
		t.Fatal("planned negative retention")
	}
	// Files-only jobs skip export without container or password.
	job, _, _ = fresh()
	manifest := scheduleManifest()
	manifest.Database = nil
	manifest.Hooks = nil
	job.Manifest = manifest
	job.Container = ""
	def, err := JobDefinition(job, fixedJobTime)
	if err != nil {
		t.Fatalf("JobDefinition: %v", err)
	}
	for _, step := range def.Steps {
		if step.Name == "export:db" {
			t.Fatal("export step without database")
		}
	}
	if strings.Contains(func() string {
		names := ""
		for _, step := range def.Steps {
			names += step.Name + ","
		}
		return names
	}(), "prune") == false {
		t.Fatal("missing prune step")
	}
}

func TestScheduleEditKeepsCadenceAnchor(t *testing.T) {
	schedules := openSchedules(t)
	first, err := schedules.Save(Schedule{Package: "photos", IntervalSeconds: 86400, Retain: 7, Enabled: true, Actor: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	edited, err := schedules.Save(Schedule{Package: "photos", IntervalSeconds: 86400, Retain: 3, Enabled: true, Actor: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if !edited.UpdatedAt.Equal(first.UpdatedAt) || edited.Retain != 3 || edited.Actor != "other" {
		t.Fatalf("retention edit moved the window: %+v vs %+v", edited, first)
	}
	time.Sleep(2 * time.Millisecond)
	changed, err := schedules.Save(Schedule{Package: "photos", IntervalSeconds: 7200, Retain: 3, Enabled: true, Actor: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.UpdatedAt.After(first.UpdatedAt) {
		t.Fatal("interval change must restart the window")
	}
	if _, err := schedules.Save(Schedule{Package: "photos", IntervalSeconds: 7200, Retain: 3, Enabled: false, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	reenabled, err := schedules.Save(Schedule{Package: "photos", IntervalSeconds: 7200, Retain: 3, Enabled: true, Actor: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if !reenabled.UpdatedAt.After(changed.UpdatedAt) {
		t.Fatal("re-enabling must restart the window")
	}
}
