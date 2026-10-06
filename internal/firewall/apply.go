package firewall

import (
	"fmt"
	"os"
)

// ApplySet validates and atomically replaces the homestead table content:
// pre-check the transaction file, commit it in one nft transaction, then
// verify the live table matches. Only the homestead table is ever touched
// (ADR-008); administrator tables cannot appear here by construction —
// the script below names no other table.
func ApplySet(set RuleSet) error {
	if err := set.Validate(); err != nil {
		return err
	}
	script, err := BuildApplyScript(set)
	if err != nil {
		return err
	}
	path, err := writeTempScript(script)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	if _, err := runCmd("nft", "--check", "--file", path); err != nil {
		return fmt.Errorf("firewall: transaction check failed, nothing applied: %w", err)
	}
	if _, err := runCmd("nft", "--file", path); err != nil {
		return fmt.Errorf("firewall: atomic apply failed: %w", err)
	}
	live, err := LiveTable()
	if err != nil {
		return fmt.Errorf("firewall: applied but state unreadable: %w", err)
	}
	plan, err := Build(set, live)
	if err != nil {
		return fmt.Errorf("firewall: applied but unverifiable: %w", err)
	}
	if !plan.InSync {
		return fmt.Errorf("firewall: applied but live state differs: %v", plan.Changes)
	}
	return nil
}

// BuildApplyScript renders the atomic transaction: ensure the table,
// empty it, then re-add the chain and rules — one nft -f commit, so
// observers see the old or the new table, never a half-written one.
// add-table and flush are idempotent across missing and present tables,
// which is what makes a fixed script safe for both.
func BuildApplyScript(set RuleSet) (string, error) {
	if err := set.Validate(); err != nil {
		return "", err
	}
	out := fmt.Sprintf("add table %s %s\n", TableFamily, HomesteadTable)
	out += fmt.Sprintf("flush table %s %s\n", TableFamily, HomesteadTable)
	out += fmt.Sprintf("add chain %s %s input { type filter hook input priority filter; policy accept; }\n",
		TableFamily, HomesteadTable)
	for _, rule := range set.Sorted() {
		nft, err := renderRule(rule)
		if err != nil {
			return "", err
		}
		out += fmt.Sprintf("add rule %s %s input %s\n", TableFamily, HomesteadTable, nft)
	}
	return out, nil
}

// writeTempScript stores the transaction at 0600 for the privileged apply.
func writeTempScript(script string) (string, error) {
	file, err := os.CreateTemp("", "homestead-nft-*.nft")
	if err != nil {
		return "", fmt.Errorf("firewall: temp script: %w", err)
	}
	name := file.Name()
	if _, err := file.WriteString(script); err != nil {
		file.Close()
		os.Remove(name)
		return "", fmt.Errorf("firewall: temp script: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("firewall: temp script: %w", err)
	}
	return name, nil
}
