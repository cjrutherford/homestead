package catalog

import (
	"fmt"
	"regexp"
	"strings"
)

// DatabaseKind selects the export mechanics: file copies for
// embedded SQLite, dumps for server databases. Credentials ride
// secret-backed variables at export time (S266: PasswordSecret) — declarations
// name what to export and as whom, never passwords.
type DatabaseKind string

const (
	// DatabaseSQLite exports an embedded database file.
	DatabaseSQLite DatabaseKind = "sqlite"
	// DatabasePostgres dumps a server database.
	DatabasePostgres DatabaseKind = "postgres"
)

// Valid reports a known database kind.
func (k DatabaseKind) Valid() bool {
	return k == DatabaseSQLite || k == DatabasePostgres
}

var validDBIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DatabaseBackup declares one database export: the owning service,
// the engine, and the engine parameters. SQLite takes a relative
// contained path to the file; Postgres takes database and username
// with nothing else — cross-engine fields refuse rather than
// silently ignoring what an author mistyped.
type DatabaseBackup struct {
	Service  string
	Kind     DatabaseKind
	Path     string
	Database string
	Username string
	// PasswordSecret names the manifest secret (SecretDecl.Name)
	// holding the Postgres password; backups resolve it from the
	// secret store (S266). Empty means the caller supplies one.
	PasswordSecret string
}

// Validate checks the declaration against known services.
func (d DatabaseBackup) Validate(services map[string]bool) error {
	if !services[d.Service] {
		return fmt.Errorf("catalog: database service %q unknown", d.Service)
	}
	if !d.Kind.Valid() {
		return fmt.Errorf("catalog: database kind must be sqlite or postgres")
	}
	if d.Kind == DatabaseSQLite {
		if d.Path == "" || d.Path[0] == '/' || d.Path == "." ||
			strings.HasPrefix(d.Path, "../") || strings.Contains(d.Path, "/../") {
			return fmt.Errorf("catalog: sqlite path must be relative and contained")
		}
		if d.Database != "" || d.Username != "" {
			return fmt.Errorf("catalog: sqlite takes no database or username")
		}
		return nil
	}
	if !validDBIdentifier.MatchString(d.Database) {
		return fmt.Errorf("catalog: postgres database must be a valid identifier")
	}
	if !validDBIdentifier.MatchString(d.Username) {
		return fmt.Errorf("catalog: postgres username must be a valid identifier")
	}
	if d.Path != "" {
		return fmt.Errorf("catalog: postgres takes no path")
	}
	return nil
}

// HookStage orders consistency hooks around data capture.
type HookStage string

const (
	// HookPre runs before capture (quiesce, flush, checkpoint).
	HookPre HookStage = "pre"
	// HookPost runs after capture (resume, verify, notify).
	HookPost HookStage = "post"
)

// BackupHook declares one consistency command executed in a service
// container around capture, in list order within its stage. Timeouts
// bound hook runs (0 means the five-minute default); commands run
// exactly as declared with no shell, so pipelines and redirections
// belong in entrypoints, not here.
type BackupHook struct {
	Stage   HookStage
	Service string
	Command []string
	Timeout int
}

// Validate checks the hook against known services.
func (h BackupHook) Validate(services map[string]bool) error {
	if h.Stage != HookPre && h.Stage != HookPost {
		return fmt.Errorf("catalog: hook stage must be pre or post")
	}
	if !services[h.Service] {
		return fmt.Errorf("catalog: hook service %q unknown", h.Service)
	}
	if len(h.Command) == 0 {
		return fmt.Errorf("catalog: hook command must not be empty")
	}
	for _, arg := range h.Command {
		if arg == "" {
			return fmt.Errorf("catalog: hook arguments must not be empty")
		}
	}
	if h.Timeout != 0 && (h.Timeout < 5 || h.Timeout > 3600) {
		return fmt.Errorf("catalog: hook timeout must be 0 or 5-3600 seconds")
	}
	return nil
}
