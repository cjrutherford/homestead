package server

import (
	"context"
	"errors"
	"fmt"
	"os"

	"homestead/internal/catalog"
	"homestead/internal/operation"
)

// Check the resolved /config mount before provisioning or starting Komga.
// Claim status alone is insufficient: a reused database may have zero users.
func freshKomgaDataStep(plan catalog.InstallPlan) operation.StepDef {
	return operation.StepDef{Name: "check-fresh-komga-data", Run: func(context.Context) error {
		if plan.Package != "komga" {
			return errors.New("Komga first-run guard received another package")
		}
		found := false
		for _, mount := range plan.Mounts {
			if mount.Service != "komga" || mount.Target != "/config" || mount.Ephemeral {
				continue
			}
			if found || mount.Source == "" {
				return errors.New("Komga has an ambiguous config mount")
			}
			found = true
			entries, err := os.ReadDir(mount.Source)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("cannot inspect Komga config mount: %w", err)
			}
			if len(entries) != 0 {
				return errors.New("Komga config mount already contains data; Homestead refuses an automated recovery claim")
			}
		}
		if !found {
			return errors.New("Komga has no inspectable config mount")
		}
		return nil
	}}
}
