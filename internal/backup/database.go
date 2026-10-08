package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"homestead/internal/catalog"

	_ "modernc.org/sqlite"
)

// execContainer runs a command in a service container, returning
// stdout. Environment carries secrets (never logged, never stored);
// stubbed in tests. The container images carry their own tooling —
// pg_dump ships in postgres-family images — so no host footprint.
var execContainer = func(ctx context.Context, container string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", container}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("backup: exec %s: %w", container, err)
	}
	return string(out), nil
}

// containerName resolves the S153 naming convention shared by
// generation and hooks: package-service, one name everywhere.
func containerName(packageName, service string) string {
	return packageName + "-" + service
}

// ExportRequest scopes one database export: the manifest owning the
// declaration, the package name for container resolution, the source
// root the relative database path hangs under, the run directory
// receiving output, the container for server engines and hooks, and
// the database password for Postgres (per call from secrets, never
// stored here).
type ExportRequest struct {
	Manifest  catalog.Manifest
	Package   string
	Database  catalog.DatabaseBackup
	SrcRoot   string
	DestDir   string
	Container string
	Password  string
	Hooks     []catalog.BackupHook
}

// Validate checks export shape against manifest services, so
// declarations cannot drift from the package they belong to.
func (r ExportRequest) Validate() error {
	if err := r.Manifest.Validate(); err != nil {
		return err
	}
	if r.Package == "" {
		return fmt.Errorf("backup: package must not be empty")
	}
	services := map[string]bool{}
	for _, service := range r.Manifest.Services {
		services[service.Name] = true
	}
	if err := r.Database.Validate(services); err != nil {
		return err
	}
	if r.SrcRoot == "" || r.SrcRoot[0] != '/' {
		return fmt.Errorf("backup: source root must be absolute")
	}
	if r.DestDir == "" {
		return fmt.Errorf("backup: destination directory must not be empty")
	}
	seen := map[string]bool{}
	for i := range r.Hooks {
		if err := r.Hooks[i].Validate(services); err != nil {
			return fmt.Errorf("backup: hook #%d: %w", i, err)
		}
		key := string(r.Hooks[i].Stage) + "\x00" + r.Hooks[i].Service + "\x00" + strings.Join(r.Hooks[i].Command, "\x00")
		if seen[key] {
			return fmt.Errorf("backup: duplicate hook #%d", i)
		}
		seen[key] = true
	}
	return nil
}

// runHooks executes one stage in list order with a five-minute
// default bounding each hook; declared timeouts override per hook.
// Commands run exactly as declared with no shell and no environment
// beyond inheritance.
func runHooks(ctx context.Context, packageName string, hooks []catalog.BackupHook, stage catalog.HookStage) error {
	for _, hook := range hooks {
		if hook.Stage != stage {
			continue
		}
		wait := 5 * time.Minute
		if hook.Timeout != 0 {
			wait = time.Duration(hook.Timeout) * time.Second
		}
		call, cancel := context.WithTimeout(ctx, wait)
		_, err := execContainer(call, containerName(packageName, hook.Service), nil, hook.Command...)
		cancel()
		if err != nil {
			return fmt.Errorf("backup: %s hook %q: %w", stage, strings.Join(hook.Command, " "), err)
		}
	}
	return nil
}

// exportSQLite snapshots a live database file with VACUUM INTO: a
// transactionally consistent copy without stopping writers, straight
// from the pure-Go driver already in the tree. Busy timeouts absorb
// momentary lock contention instead of failing it.
func exportSQLite(srcPath, dstPath string) error {
	db, err := sql.Open("sqlite", "file:"+srcPath+"?_pragma=busy_timeout%285000%29")
	if err != nil {
		return fmt.Errorf("backup: open sqlite: %w", err)
	}
	defer db.Close()
	quoted := "'" + strings.ReplaceAll(dstPath, "'", "''") + "'"
	if _, err := db.Exec("VACUUM INTO " + quoted); err != nil {
		return fmt.Errorf("backup: vacuum into: %w", err)
	}
	return nil
}

// exportPostgres dumps through the database container's own pg_dump
// as plain SQL: inspectable, restorable with psql, no custom-format
// tooling to match later. The password travels process environment
// for the single invocation and never touches disk or errors.
func exportPostgres(ctx context.Context, container, user, database, password, dstPath string) error {
	dump, err := execContainer(ctx, container,
		[]string{"PGPASSWORD=" + password},
		"pg_dump", "--username="+user, "--dbname="+database, "--no-password")
	if err != nil {
		return fmt.Errorf("backup: pg_dump: %w", err)
	}
	if err := os.WriteFile(dstPath, []byte(dump), 0o600); err != nil {
		return fmt.Errorf("backup: write dump: %w", err)
	}
	return nil
}

// ExportDatabase runs one application-aware export: pre hooks, engine
// export, post hooks, then the output path. Post hooks run even when
// export fails — resume hooks must resume — with both failures
// reported instead of the second hiding the first.
func ExportDatabase(ctx context.Context, req ExportRequest) (string, error) {
	if err := req.Validate(); err != nil {
		return "", err
	}
	destDir := req.DestDir
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("backup: destination: %w", err)
	}
	if err := runHooks(ctx, req.Package, req.Hooks, catalog.HookPre); err != nil {
		return "", err
	}
	var outPath string
	var exportErr error
	switch req.Database.Kind {
	case catalog.DatabaseSQLite:
		outPath = filepath.Join(destDir, filepath.Base(req.Database.Path))
		exportErr = exportSQLite(filepath.Join(req.SrcRoot, req.Database.Path), outPath)
	case catalog.DatabasePostgres:
		outPath = filepath.Join(destDir, req.Database.Database+".sql")
		exportErr = exportPostgres(ctx, req.Container, req.Database.Username, req.Database.Database, req.Password, outPath)
	default:
		exportErr = fmt.Errorf("backup: unknown database kind %q", req.Database.Kind)
	}
	postErr := runHooks(ctx, req.Package, req.Hooks, catalog.HookPost)
	if exportErr != nil && postErr != nil {
		return "", fmt.Errorf("%v (post hook also failed: %v)", exportErr, postErr)
	}
	if exportErr != nil {
		return "", exportErr
	}
	if postErr != nil {
		return "", postErr
	}
	return outPath, nil
}
