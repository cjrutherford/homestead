package backup

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"filippo.io/age"

	"homestead/internal/operation"
)

// RestoreDefinition plans approved restoration: one copy step per
// planned entry plus a closing verification comparing the restored
// trees against their backup sources by relative file set and sizes.
// Verification reuses the copy machinery in reverse — same traversal,
// same refusal of surprises — so restore and verify can never
// disagree about what a tree holds. Plans preview in S185; this unit
// executes them, failing fast with offenders named.
func RestoreDefinition(plan RestorePlan, identities ...age.Identity) (operation.Definition, error) {
	if len(plan.Entries) == 0 {
		return operation.Definition{}, fmt.Errorf("backup: restore plan captures nothing")
	}
	for i := range plan.Entries {
		if plan.Entries[i].BackupPath == "" || plan.Entries[i].Target == "" {
			return operation.Definition{}, fmt.Errorf("backup: restore entry #%d needs backup path and target", i)
		}
	}
	def := operation.Definition{Type: "backup.restore"}
	for i := range plan.Entries {
		entry := plan.Entries[i]
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "restore:" + entry.BackupPath,
			Run: func(ctx context.Context) error {
				return restoreEntry(entry, identities)
			},
		})
	}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "verify",
		Run: func(ctx context.Context) error {
			return verifyRestore(plan, identities)
		},
	})
	return def, nil
}

// restoreEntry replicates one backup tree over its live target with
// mirror semantics: the target clears first so orphaned live files
// cannot linger beside restored ones, then copies. Clearing trusts
// the approved plan — targets reviewed in S185 — and the backup
// source stays intact, so a failed copy retries from safety rather
// than from loss.
func restoreEntry(entry PlannedEntry, identities []age.Identity) error {
	if entry.Encrypted || isEncryptedFile(entry.BackupPath) {
		// Staged and swapped: a missing identity, wrong key, or damaged
		// stream fails with the live target untouched.
		if err := restoreEncrypted(entry.BackupPath, entry.Target, identities, entry.Sensitive, entry.Preserve...); err != nil {
			return fmt.Errorf("backup: restore %q: %w", entry.BackupPath, err)
		}
		return nil
	}
	if err := clearTarget(entry.Target, entry.Preserve); err != nil {
		return fmt.Errorf("backup: clear %q: %w", entry.Target, err)
	}
	if _, err := copyTree(entry.BackupPath, entry.Target); err != nil {
		return fmt.Errorf("backup: restore %q: %w", entry.BackupPath, err)
	}
	return nil
}

// clearTarget empties target except the preserved top-level children;
// without any it removes the target itself.
func clearTarget(target string, preserve []string) error {
	if len(preserve) == 0 {
		return os.RemoveAll(target)
	}
	children, err := os.ReadDir(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, child := range children {
		if slices.Contains(preserve, child.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(target, child.Name())); err != nil {
			return err
		}
	}
	return nil
}

// treeSignature maps relative file paths to sizes for comparison.
func treeSignature(root string, skip ...string) (map[string]int64, error) {
	signature := map[string]int64{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if slices.Contains(skip, strings.SplitN(filepath.ToSlash(relative), "/", 2)[0]) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		signature[relative] = info.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return signature, nil
}

// verifyRestore compares every restored tree against its backup
// source by file set and sizes: same names, same bytes counts, no
// extras either way. Content hashing would prove more and cost
// proportionally; sizes catch truncation and mixups, which is what
// restores actually get wrong.
func verifyRestore(plan RestorePlan, identities []age.Identity) error {
	for _, entry := range plan.Entries {
		var want map[string]int64
		var err error
		if entry.Encrypted || isEncryptedFile(entry.BackupPath) {
			want, err = archiveSignature(entry.BackupPath, identities)
		} else {
			want, err = treeSignature(entry.BackupPath)
		}
		if err != nil {
			return fmt.Errorf("backup: verify %q: %w", entry.BackupPath, err)
		}
		got, err := treeSignature(entry.Target, entry.Preserve...)
		if err != nil {
			return fmt.Errorf("backup: verify %q: %w", entry.Target, err)
		}
		if len(want) != len(got) {
			return fmt.Errorf("backup: verify %q: %d files, want %d", entry.Target, len(got), len(want))
		}
		for name, size := range want {
			if got[name] != size {
				return fmt.Errorf("backup: verify %q: %q differs", entry.Target, name)
			}
		}
	}
	return nil
}
