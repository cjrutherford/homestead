package desired

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/version"
)

// ExportVersion is the export document version. Import (S232)
// refuses newer documents; older ones migrate there, never here.
const ExportVersion = 1

// Export is the portable configuration document (S231): desired
// state, provenance metadata, and the secret-reference manifest.
// Secret VALUES never appear — the manifest names references only,
// and this package only ever calls List (metadata without values),
// so leakage is structurally impossible, not merely avoided.
type Export struct {
	Version        int            `yaml:"version" json:"version"`
	ExportedAt     time.Time      `yaml:"exported_at" json:"exported_at"`
	Homestead      string         `yaml:"homestead" json:"homestead"`
	StoreSchema    int            `yaml:"store_schema" json:"store_schema"`
	DesiredVersion int            `yaml:"desired_version" json:"desired_version"`
	Settings       map[string]any `yaml:"settings,omitempty" json:"settings"`
	Secrets        []string       `yaml:"secrets,omitempty" json:"secrets"`
}

// Validate rejects documents this binary cannot interpret: missing
// or newer versions fail with upgrade guidance, never silent
// misreading.
func (e Export) Validate() error {
	if e.Version <= 0 {
		return fmt.Errorf("desired: export version missing (want %d)", ExportVersion)
	}
	if e.Version > ExportVersion {
		return fmt.Errorf("desired: export version %d newer than supported %d (upgrade homestead)", e.Version, ExportVersion)
	}
	return nil
}

// ExportState bundles the desired-state file with provenance and
// secret references into an Export. Values stay in the secret store:
// the manifest carries names for S232 to demand, never contents.
func ExportState(desiredPath string, secrets *secret.Store) (Export, error) {
	if secrets == nil {
		return Export{}, fmt.Errorf("desired: export needs a secret store")
	}
	state, err := Load(desiredPath)
	if err != nil {
		return Export{}, err
	}
	infos, err := secrets.List()
	if err != nil {
		return Export{}, fmt.Errorf("desired: list secrets: %w", err)
	}
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		if secret.IsReserved(info.Name) {
			continue
		}
		names = append(names, info.Name)
	}
	return Export{
		Version: ExportVersion, ExportedAt: time.Now().UTC(),
		Homestead:   version.Version,
		StoreSchema: store.CurrentVersion, DesiredVersion: CurrentVersion,
		Settings: state.Settings, Secrets: names,
	}, nil
}

// WriteExport validates, marshals, and atomically replaces the file
// at path via temp-file + rename: 0644-safe by construction, since
// Export cannot carry secret values.
func WriteExport(path string, export Export) error {
	if err := export.Validate(); err != nil {
		return err
	}
	var body bytes.Buffer
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(export); err != nil {
		return fmt.Errorf("desired: render export: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("desired: render export: %w", err)
	}
	tmp, err := os.CreateTemp("", "homestead-export-*.yaml")
	if err != nil {
		return fmt.Errorf("desired: temp export: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(body.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("desired: write export: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("desired: write export: %w", err)
	}
	if err := os.Chmod(tmpName, fileMode); err != nil {
		return fmt.Errorf("desired: mode export: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("desired: replace %s: %w", path, err)
	}
	return nil
}

// ReadExport reads and strictly parses the file at path. Unknown
// fields are rejected so hand-edited typos fail loudly; version
// bounds apply. Import (S232) builds on this reader.
func ReadExport(path string) (Export, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Export{}, fmt.Errorf("desired: read %s: %w", path, ErrNotFound)
		}
		return Export{}, fmt.Errorf("desired: read %s: %w", path, err)
	}
	var export Export
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&export); err != nil {
		return Export{}, fmt.Errorf("desired: parse %s: %w", path, err)
	}
	if err := export.Validate(); err != nil {
		return Export{}, err
	}
	return export, nil
}
