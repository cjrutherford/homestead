// Package layout is the authoritative Homestead filesystem layout (S012).
//
// The five directories below are the only host paths the daemon manages
// (binaries live at /usr/local/bin per docs/ARCHITECTURE.md). Ownership is
// uniformly root:root: this is a single-appliance system (ADR-012) whose
// daemon runs as a root-owned service (ADR-001). Group/other access policies
// for additional local users arrive only if a slice ever introduces them.
//
// Modes encode least privilege for what each directory will hold:
//   - LibDir is 0700: SQLite operational state includes sessions and tokens.
//   - EtcDir stays readable: desired state is customer-inspectable YAML.
//
// Secrets themselves arrive with the secret store (S096), which may tighten
// EtcDir further; that slice owns the change.
package layout

import (
	"os"
)

// Dir describes one daemon-managed directory.
type Dir struct {
	// Path is the absolute production path.
	Path string
	// Mode is the enforced permission bits.
	Mode os.FileMode
	// Purpose documents what the directory holds and which slice fills it.
	Purpose string
}

var (
	// EtcDir holds customer-readable desired configuration (YAML; S014).
	EtcDir = Dir{Path: "/etc/homestead", Mode: 0o755, Purpose: "desired configuration (YAML)"}
	// LibDir holds SQLite operational state (S013): sessions, onboarding, audits.
	LibDir = Dir{Path: "/var/lib/homestead", Mode: 0o700, Purpose: "operational state (SQLite)"}
	// LogDir holds file logs; the journal remains the primary log sink.
	LogDir = Dir{Path: "/var/log/homestead", Mode: 0o755, Purpose: "file logs"}
	// RunDir holds runtime sockets and pidfiles. Provisioned here for
	// non-service contexts and managed by systemd RuntimeDirectory at runtime.
	RunDir = Dir{Path: "/run/homestead", Mode: 0o755, Purpose: "runtime sockets/pidfiles"}
	// StacksDir holds Compose stacks; populated by stack lifecycle (S074+).
	StacksDir = Dir{Path: "/opt/homestead/stacks", Mode: 0o755, Purpose: "Compose stacks"}
)

// Dirs lists every daemon-managed directory.
var Dirs = []Dir{EtcDir, LibDir, LogDir, RunDir, StacksDir}

// Ensure creates dir.Path (including parents) and enforces dir.Mode.
// It is idempotent: re-running repairs drift instead of failing.
func Ensure(dir Dir) error {
	if err := os.MkdirAll(dir.Path, dir.Mode); err != nil {
		return err
	}
	return os.Chmod(dir.Path, dir.Mode)
}

// EnsureAll provisions every directory in Dirs. It must run as root in
// production; see scripts/provision-layout.sh, which mirrors this list.
func EnsureAll() error {
	for _, dir := range Dirs {
		if err := Ensure(dir); err != nil {
			return err
		}
	}
	return nil
}
