package desired

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// SettingsValidator checks imported settings against the config
// schema (the same rules `config apply` enforces) and returns a
// refusal naming every bad field. The desired package cannot import
// the config service, so callers pass the check in.
type SettingsValidator func(settings map[string]any) error

// secretStore is the slice of secret.Store an import touches; a seam
// so rollback is testable with a store that fails midway.
type secretStore interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
}

// ImportState validates and imports an exported document: version
// bounds, settings against the schema, secret completeness, then
// secrets and the desired file. The write is all-or-nothing: the
// previous secrets and desired file are snapshotted first and
// restored if any step fails. Secrets arrive in values keyed by
// manifest name; missing names refuse listing every gap, extras
// refuse rather than stashing unrelated material. Re-running
// converges: sets replace, the file rewrites.
func ImportState(path string, desiredPath string, secrets *secret.Store, values map[string]string, validate SettingsValidator) (ImportSummary, error) {
	if secrets == nil {
		return ImportSummary{}, fmt.Errorf("desired: import needs a secret store")
	}
	export, err := ReadExport(path)
	if err != nil {
		return ImportSummary{}, err
	}
	return ImportExport(export, desiredPath, secrets, values, validate)
}

// ImportExport imports an already-read document: the API path for
// inline documents, sharing every rule with ImportState.
func ImportExport(export Export, desiredPath string, secrets *secret.Store, values map[string]string, validate SettingsValidator) (ImportSummary, error) {
	if secrets == nil {
		return ImportSummary{}, fmt.Errorf("desired: import needs a secret store")
	}
	return importExport(export, desiredPath, secrets, values, validate)
}

func importExport(export Export, desiredPath string, secrets secretStore, values map[string]string, validate SettingsValidator) (ImportSummary, error) {
	if err := ValidateImport(export); err != nil {
		return ImportSummary{}, err
	}
	if validate != nil {
		if err := validate(export.Settings); err != nil {
			return ImportSummary{}, err
		}
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
	snap, err := takeSnapshot(desiredPath, secrets, export.Secrets)
	if err != nil {
		return ImportSummary{}, err
	}
	for _, name := range export.Secrets {
		if err := secrets.Set(name, values[name]); err != nil {
			return ImportSummary{}, snap.rollback(desiredPath, secrets, fmt.Errorf("desired: set secret %q: %w", name, err))
		}
	}
	state := State{Version: export.DesiredVersion, Settings: export.Settings}
	if err := Save(desiredPath, state); err != nil {
		return ImportSummary{}, snap.rollback(desiredPath, secrets, err)
	}
	return ImportSummary{Settings: len(state.Settings), Secrets: len(export.Secrets)}, nil
}

// snapshot holds what an import may overwrite: the raw desired file
// and each affected secret's prior value (absent when it did not exist).
type snapshot struct {
	file       []byte
	fileExists bool
	secrets    map[string]*string
}

func takeSnapshot(desiredPath string, secrets secretStore, names []string) (snapshot, error) {
	snap := snapshot{secrets: map[string]*string{}}
	data, err := os.ReadFile(desiredPath)
	switch {
	case err == nil:
		snap.file, snap.fileExists = data, true
	case errors.Is(err, os.ErrNotExist):
	default:
		return snap, fmt.Errorf("desired: snapshot %s: %w", desiredPath, err)
	}
	for _, name := range names {
		value, err := secrets.Get(name)
		switch {
		case err == nil:
			v := value
			snap.secrets[name] = &v
		case errors.Is(err, secret.ErrNotFound):
			snap.secrets[name] = nil
		default:
			return snap, fmt.Errorf("desired: snapshot secret %q: %w", name, err)
		}
	}
	return snap, nil
}

// rollback restores every snapshotted secret and the desired file,
// then returns cause (with any restore failures appended).
func (snap snapshot) rollback(desiredPath string, secrets secretStore, cause error) error {
	var failures []string
	for name, prior := range snap.secrets {
		var err error
		if prior == nil {
			err = secrets.Delete(name)
		} else {
			err = secrets.Set(name, *prior)
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("secret %q: %v", name, err))
		}
	}
	if snap.fileExists {
		if err := restoreFile(desiredPath, snap.file); err != nil {
			failures = append(failures, err.Error())
		}
	} else if err := os.Remove(desiredPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w (rollback incomplete: %s)", cause, strings.Join(failures, "; "))
	}
	return cause
}

func restoreFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".desired-restore-*.tmp")
	if err != nil {
		return fmt.Errorf("restore %s: %w", path, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("restore %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("restore %s: %w", path, err)
	}
	if err := os.Chmod(name, fileMode); err != nil {
		return fmt.Errorf("restore %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("restore %s: %w", path, err)
	}
	return nil
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
