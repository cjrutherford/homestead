package desired

import (
	"fmt"

	"homestead/internal/secret"
	"homestead/internal/store"
)

// ImportSummary reports what import wrote: settings keys restored
// and secrets set. Values arrive per call and never touch the
// document.
type ImportSummary struct {
	Settings int
	Secrets  int
}

// ImportState validates and imports an exported document: version
// bounds, schema compatibility, secret completeness, then secrets
// before the desired file — so failures leave no half-imported
// state behind (the file only lands after every secret sets).
// Secrets arrive in values keyed by manifest name; missing names
// refuse listing every gap, extras refuse rather than stashing
// unrelated material. Re-running converges: sets replace, the file
// rewrites.
func ImportState(path string, desiredPath string, secrets *secret.Store, values map[string]string) (ImportSummary, error) {
	if secrets == nil {
		return ImportSummary{}, fmt.Errorf("desired: import needs a secret store")
	}
	export, err := ReadExport(path)
	if err != nil {
		return ImportSummary{}, err
	}
	return ImportExport(export, desiredPath, secrets, values)
}

// ImportExport imports an already-read document: the API path for
// inline documents, sharing every rule with ImportState.
func ImportExport(export Export, desiredPath string, secrets *secret.Store, values map[string]string) (ImportSummary, error) {
	if secrets == nil {
		return ImportSummary{}, fmt.Errorf("desired: import needs a secret store")
	}
	if err := ValidateImport(export); err != nil {
		return ImportSummary{}, err
	}
	var missing []string
	for _, name := range export.Secrets {
		value, ok := values[name]
		if !ok || value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return ImportSummary{}, fmt.Errorf("desired: missing secret values for %q", missing)
	}
	wanted := map[string]bool{}
	for _, name := range export.Secrets {
		wanted[name] = true
	}
	for name := range values {
		if !wanted[name] {
			return ImportSummary{}, fmt.Errorf("desired: unexpected secret value %q", name)
		}
	}
	for _, name := range export.Secrets {
		if err := secrets.Set(name, values[name]); err != nil {
			return ImportSummary{}, fmt.Errorf("desired: set secret %q: %w", name, err)
		}
	}
	state := State{Version: export.DesiredVersion, Settings: export.Settings}
	if err := Save(desiredPath, state); err != nil {
		return ImportSummary{}, err
	}
	return ImportSummary{Settings: len(state.Settings), Secrets: len(export.Secrets)}, nil
}

// ValidateImport applies every compatibility rule an import needs
// before any secret is looked at: version bounds and schema levels.
func ValidateImport(export Export) error {
	if err := export.Validate(); err != nil {
		return err
	}
	if export.StoreSchema > store.CurrentVersion {
		return fmt.Errorf("desired: export needs store schema %d, this Homestead supports %d (upgrade homestead)", export.StoreSchema, store.CurrentVersion)
	}
	if export.DesiredVersion > CurrentVersion {
		return fmt.Errorf("desired: export needs desired version %d, this Homestead supports %d (upgrade homestead)", export.DesiredVersion, CurrentVersion)
	}
	if export.DesiredVersion <= 0 {
		return fmt.Errorf("desired: export desired version missing")
	}
	return nil
}
