package firewall

import (
	"fmt"
	"os"
	"strings"
)

// Snapshot is the exact live table text before a risky change, captured
// for rollback. Exists false means no table was present.
type Snapshot struct {
	Exists bool
	Script string
}

// TakeSnapshot reads the live homestead table verbatim. A missing table
// is normal pre-install, not an error; permission failures are.
func TakeSnapshot() (Snapshot, error) {
	out, err := runCmd("nft", "list", "table", TableFamily, HomesteadTable)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such file or directory") {
			return Snapshot{}, nil
		}
		return Snapshot{}, fmt.Errorf("firewall: snapshot table: %w", err)
	}
	return Snapshot{Exists: true, Script: out}, nil
}

// Restore returns the table to the snapshot: delete plus the captured
// definition in one transaction when it existed, table removal when it
// did not (undoing a created-then-failed apply). Afterwards the re-listed
// table must equal the snapshot, byte for byte.
func (s Snapshot) Restore() error {
	if !s.Exists {
		if _, err := runCmd("nft", "delete", "table", TableFamily, HomesteadTable); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "no such file or directory") {
				return nil
			}
			return fmt.Errorf("firewall: rollback remove: %w", err)
		}
		return nil
	}
	path, err := writeTempScript(fmt.Sprintf("delete table %s %s\n%s", TableFamily, HomesteadTable, s.Script))
	if err != nil {
		return err
	}
	defer os.Remove(path)
	if _, err := runCmd("nft", "--check", "--file", path); err != nil {
		return fmt.Errorf("firewall: rollback check failed: %w", err)
	}
	if _, err := runCmd("nft", "--file", path); err != nil {
		return fmt.Errorf("firewall: rollback apply failed: %w", err)
	}
	again, err := TakeSnapshot()
	if err != nil {
		return fmt.Errorf("firewall: rollback unverifiable: %w", err)
	}
	if !again.Exists || again.Script != s.Script {
		return fmt.Errorf("firewall: rollback mismatch: live table differs from snapshot")
	}
	return nil
}
