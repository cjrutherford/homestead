package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/exposure"

	_ "modernc.org/sqlite"
)

func sqliteFixture(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items (value) VALUES ('one'), ('two')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return path
}

func sqliteManifest() catalog.Manifest {
	return catalog.Manifest{
		Name: "photos", Version: "1.0.0", Title: "Photos", Blurb: "Photos.",
		Description: "Photos.", Category: "media", Icon: "mdi:image",
		Services:     []catalog.Service{{Name: "db", Image: "example/db:1.0.0"}},
		Requirements: catalog.Requirements{MemoryMB: 64},
		Exposure: exposure.PackageExposure{Package: "photos", Endpoints: []exposure.EndpointExposure{
			{Endpoint: "web", Recommended: "local"},
		}},
	}
}

func stubExec(t *testing.T, handler func(container, stdin string, args []string) (string, error)) *[]string {
	t.Helper()
	var calls []string
	old := execContainer
	execContainer = func(_ context.Context, container, stdin string, args ...string) (string, error) {
		calls = append(calls, container+" "+strings.Join(args, " "))
		return handler(container, stdin, args)
	}
	t.Cleanup(func() { execContainer = old })
	return &calls
}

func TestExportSQLite(t *testing.T) {
	src := t.TempDir()
	sqliteFixture(t, src, "app.db")
	dest := t.TempDir()
	req := ExportRequest{
		Manifest: sqliteManifest(),
		Package:  "photos",
		Database: catalog.DatabaseBackup{Service: "db", Kind: catalog.DatabaseSQLite, Path: "app.db"},
		SrcRoot:  src,
		DestDir:  filepath.Join(dest, "run"),
		Hooks: []catalog.BackupHook{
			{Stage: catalog.HookPre, Service: "db", Command: []string{"pre-hook"}},
			{Stage: catalog.HookPost, Service: "db", Command: []string{"post-hook"}},
		},
	}
	calls := stubExec(t, func(_ string, _ string, _ []string) (string, error) {
		return "ok", nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := ExportDatabase(ctx, req)
	if err != nil {
		t.Fatalf("ExportDatabase: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("hooks = %v", *calls)
	}
	db, err := sql.Open("sqlite", "file:"+out+"?mode=ro")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
		t.Fatalf("query exported: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d", count)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %q %v", integrity, err)
	}
	// Export failure still runs post hooks, reporting both.
	stubExec(t, func(_ string, _ string, _ []string) (string, error) {
		return "", errors.New("hook down")
	})
	if _, err := ExportDatabase(ctx, req); err == nil ||
		!strings.Contains(err.Error(), "hook down") {
		t.Fatalf("err = %v", err)
	}
}

func TestExportPostgres(t *testing.T) {
	manifest := sqliteManifest()
	var seenStdin string
	var seenArgs []string
	stubExec(t, func(_ string, stdin string, args []string) (string, error) {
		seenStdin = stdin
		seenArgs = args
		return "-- postgres dump\nSELECT 1;\n", nil
	})
	req := ExportRequest{
		Manifest: manifest, Package: "photos",
		Database:  catalog.DatabaseBackup{Service: "db", Kind: catalog.DatabasePostgres, Database: "photos", Username: "photos"},
		SrcRoot:   t.TempDir(),
		DestDir:   t.TempDir(),
		Container: "photos-db",
		Password:  "secret",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := ExportDatabase(ctx, req)
	if err != nil {
		t.Fatalf("ExportDatabase: %v", err)
	}
	if !strings.HasSuffix(out, "photos.sql") {
		t.Fatalf("out = %q", out)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "SELECT 1;") {
		t.Fatalf("dump = %q", content)
	}
	joined := strings.Join(seenArgs, " ")
	for _, want := range []string{"pg_dump", "--username=\"$1\"", "--dbname=\"$2\"", "PGPASSWORD", "photos"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args = %v", seenArgs)
		}
	}
	if seenStdin != "secret\n" {
		t.Fatalf("stdin = %q, want the password", seenStdin)
	}
	if strings.Contains(joined, "secret") {
		t.Fatal("password in argv")
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestExportValidation(t *testing.T) {
	manifest := sqliteManifest()
	good := ExportRequest{
		Manifest: manifest, Package: "photos",
		Database: catalog.DatabaseBackup{Service: "db", Kind: catalog.DatabaseSQLite, Path: "app.db"},
		SrcRoot:  "/srv", DestDir: "/backup",
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	badManifest := good
	badManifest.Manifest.Name = "Bad!"
	if err := badManifest.Validate(); err == nil {
		t.Fatal("validated bad manifest")
	}
	unknown := good
	unknown.Database.Service = "ghost"
	if err := unknown.Validate(); err == nil {
		t.Fatal("validated ghost service")
	}
	relative := good
	relative.SrcRoot = "srv"
	if err := relative.Validate(); err == nil {
		t.Fatal("validated relative root")
	}
	dup := good
	dup.Hooks = []catalog.BackupHook{
		{Stage: catalog.HookPre, Service: "db", Command: []string{"true"}},
		{Stage: catalog.HookPre, Service: "db", Command: []string{"true"}},
	}
	if err := dup.Validate(); err == nil {
		t.Fatal("validated duplicate hooks")
	}
	if _, err := ExportDatabase(context.Background(), ExportRequest{}); err == nil {
		t.Fatal("exported empty request")
	}
}

// TestExecContainerDeliversPasswordOnStdinOnly drives the real
// execContainer with a fake `docker` on PATH that records its argv,
// environment, and stdin: the password must arrive on stdin and nowhere
// else the host can see.
func TestExecContainerDeliversPasswordOnStdinOnly(t *testing.T) {
	bin := t.TempDir()
	out := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + out + "/argv\nenv > " + out + "/env\ncat > " + out + "/stdin\necho dump-body\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dst := filepath.Join(t.TempDir(), "photos.sql")
	if err := exportPostgres(context.Background(), "photos-db", "photos", "photos", "hunter2-secret", dst); err != nil {
		t.Fatal(err)
	}
	stdin, _ := os.ReadFile(filepath.Join(out, "stdin"))
	if string(stdin) != "hunter2-secret\n" {
		t.Fatalf("stdin = %q", stdin)
	}
	for _, name := range []string{"argv", "env"} {
		raw, _ := os.ReadFile(filepath.Join(out, name))
		if strings.Contains(string(raw), "hunter2-secret") {
			t.Fatalf("password visible in host %s", name)
		}
	}
	dump, _ := os.ReadFile(dst)
	if strings.TrimSpace(string(dump)) != "dump-body" {
		t.Fatalf("dump = %q", dump)
	}
}
