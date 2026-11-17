package backup

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"

	"homestead/internal/catalog"

	_ "modernc.org/sqlite"
)

// execContainer runs a command in a service container, returning
// stdout only (stderr joins the error, never the output, so tool
// warnings cannot corrupt a dump). Secrets travel on stdin and are
// exported inside the container by the command itself: never argv, never
// the host process environment (`docker exec` forwards neither safely).
// Stubbed in tests. The container images carry their own tooling —
// pg_dump ships in postgres-family images — so no host footprint.
var execContainer = func(ctx context.Context, container, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", container}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if stdin != "" {
			detail = strings.ReplaceAll(detail, strings.TrimSpace(stdin), "[redacted]")
		}
		return "", fmt.Errorf("backup: exec %s: %w: %s", container, err, detail)
	}
	return stdout.String(), nil
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
// the database password for a server engine (per call from secrets,
// never stored here).
type ExportRequest struct {
	Manifest  catalog.Manifest
	Package   string
	Database  catalog.DatabaseBackup
	SrcRoot   string
	DestDir   string
	Container string
	Password  string
	Hooks     []catalog.BackupHook
	// Recipients encrypt the export (S259): the artifact is written as
	// <name>.age and no plaintext dump lands in the destination.
	Recipients []age.Recipient
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
		_, err := execContainer(call, containerName(packageName, hook.Service), "", hook.Command...)
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
	return exportSQLiteTo(srcPath, dstPath, nil)
}

// scratchBase is the RAM-backed scratch root and scratchPrefix names
// the per-export directories inside it. tmpfs pages can be swapped to
// disk on hosts with swap, so the scratch holds a snapshot only for the
// seconds an export takes and is swept at daemon start.
var scratchBase = "/dev/shm"

const scratchPrefix = "homestead-backup-"

// SweepScratch removes scratch directories a crashed export left behind.
// Called once at daemon start, before any backup can run.
func SweepScratch() {
	matches, _ := filepath.Glob(filepath.Join(scratchBase, scratchPrefix+"*"))
	for _, m := range matches {
		_ = os.RemoveAll(m)
	}
}

// sqliteScratchDir picks RAM-backed scratch space for the one place
// SQLite needs a file (VACUUM INTO): encrypted exports must never put
// plaintext on a disk, so it refuses to fall back to ordinary storage.
func sqliteScratchDir() (string, error) {
	shm := scratchBase
	var fsStat syscall.Statfs_t
	if err := syscall.Statfs(shm, &fsStat); err != nil || fsStat.Type != 0x01021994 { // TMPFS_MAGIC
		return "", fmt.Errorf("backup: encrypted sqlite export needs tmpfs scratch space at %s", shm)
	}
	return shm, nil
}

// exportSQLiteTo snapshots to dstPath, encrypting when recipients are
// given: the snapshot goes to a private tmpfs scratch directory first
// and is removed straight after being streamed into the .age file.
func exportSQLiteTo(srcPath, dstPath string, recipients []age.Recipient) error {
	if len(recipients) == 0 {
		return vacuumInto(srcPath, dstPath)
	}
	base, err := sqliteScratchDir()
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp(base, scratchPrefix)
	if err != nil {
		return fmt.Errorf("backup: scratch: %w", err)
	}
	defer os.RemoveAll(scratch)
	snapshot := filepath.Join(scratch, "snapshot.db")
	if err := vacuumInto(srcPath, snapshot); err != nil {
		return err
	}
	if err := encryptFile(snapshot, dstPath, recipients); err != nil {
		return fmt.Errorf("backup: encrypt snapshot: %w", err)
	}
	return nil
}

func vacuumInto(srcPath, dstPath string) error {
	db, err := sql.Open("sqlite", "file:"+srcPath+"?_pragma=busy_timeout%285000%29&_pragma=temp_store%282%29")
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

// writeDump stores one plain-SQL dump at dstPath, encrypting it when
// recipients are given. Dumps are 0600: they carry the whole database.
func writeDump(dstPath, dump string, recipients []age.Recipient) error {
	if len(recipients) > 0 {
		if err := encryptBytes(dstPath, []byte(dump), recipients); err != nil {
			return fmt.Errorf("backup: write dump: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(dstPath, []byte(dump), 0o600); err != nil {
		return fmt.Errorf("backup: write dump: %w", err)
	}
	return nil
}

// exportPostgres dumps through the database container's own pg_dump
// as plain SQL: inspectable, restorable with psql, no custom-format
// tooling to match later. The password goes to the container on stdin
// and is exported inside `sh` for the single pg_dump invocation; it never
// touches argv, the host environment, disk, or errors. pg_dump connects
// over the container's own network address so the password is actually
// checked (loopback and the socket trust every caller): a stale stored
// password fails the backup instead of passing unnoticed.
func exportPostgres(ctx context.Context, container, user, database, password, dstPath string, recipients ...age.Recipient) error {
	const script = `IFS= read -r PGPASSWORD; export PGPASSWORD; exec pg_dump -h "$(hostname)" --username="$1" --dbname="$2" --no-password`
	dump, err := execContainer(ctx, container, password+"\n", "sh", "-c", script, "sh", user, database)
	if err != nil {
		return fmt.Errorf("backup: pg_dump: %w", err)
	}
	return writeDump(dstPath, dump, recipients)
}

// exportMySQL dumps a MariaDB or MySQL database with that engine's own
// dump client. --single-transaction takes one consistent snapshot of
// transactional (InnoDB) tables without blocking writers; --quick
// streams rows instead of buffering the whole database; --skip-lock-tables
// (which single-transaction already implies) keeps read locks off
// non-transactional tables too; --no-tablespaces avoids requiring the
// PROCESS privilege that MySQL 8 otherwise asks for. The password
// goes to the container on stdin and is exported as MYSQL_PWD inside
// the same shell for the single client invocation: never argv, the
// host environment, disk, or errors. The client connects over
// 127.0.0.1 so the server checks the password instead of trusting a
// socket login.
func exportMySQL(ctx context.Context, container, user, database, password, client string, engineArgs []string, dstPath string, recipients ...age.Recipient) error {
	const script = `IFS= read -r MYSQL_PWD; export MYSQL_PWD; client="$1"; user="$2"; shift 2; exec "$client" --host=127.0.0.1 --user="$user" --single-transaction --quick --skip-lock-tables --no-tablespaces "$@"`
	args := append([]string{"sh", "-c", script, "sh", client, user}, engineArgs...)
	args = append(args, database)
	dump, err := execContainer(ctx, container, password+"\n", args...)
	if err != nil {
		return fmt.Errorf("backup: %s: %w", client, err)
	}
	return writeDump(dstPath, dump, recipients)
}

// serverDump names the dump client and the engine-specific flags for a
// server database kind. MariaDB and MySQL share the consistency flags
// inside exportMySQL; MySQL needs its own GTID handling.
func serverDump(kind catalog.DatabaseKind) (string, []string, error) {
	switch kind {
	case catalog.DatabaseMariaDB:
		return "mariadb-dump", nil, nil
	case catalog.DatabaseMySQL:
		return "mysqldump", []string{"--set-gtid-purged=OFF"}, nil
	default:
		return "", nil, fmt.Errorf("backup: unknown database kind %q", kind)
	}
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
		if len(req.Recipients) > 0 {
			outPath += FileSuffix
		}
		exportErr = exportSQLiteTo(filepath.Join(req.SrcRoot, req.Database.Path), outPath, req.Recipients)
	case catalog.DatabasePostgres, catalog.DatabaseMariaDB, catalog.DatabaseMySQL:
		outPath = filepath.Join(destDir, req.Database.Database+".sql")
		if len(req.Recipients) > 0 {
			outPath += FileSuffix
		}
		if req.Database.Kind == catalog.DatabasePostgres {
			exportErr = exportPostgres(ctx, req.Container, req.Database.Username, req.Database.Database, req.Password, outPath, req.Recipients...)
			break
		}
		client, engineArgs, err := serverDump(req.Database.Kind)
		if err != nil {
			exportErr = err
			break
		}
		exportErr = exportMySQL(ctx, req.Container, req.Database.Username, req.Database.Database, req.Password, client, engineArgs, outPath, req.Recipients...)
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
