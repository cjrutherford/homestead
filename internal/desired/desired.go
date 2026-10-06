// Package desired owns Homestead's human-readable desired configuration
// storage (S014; see ADR-003).
//
// Desired state is what the owner requested; generated state (concrete
// configs Homestead produces) and observed state (what actually runs) are
// separate concerns owned by later slices. This package provides the
// versioned envelope and the atomic file mechanics. The settings schema
// inside the envelope arrives with S015, and the read/write API serving
// CLI and dashboard arrives with S016 — both extend State, neither
// reimplements storage.
//
// Secret values never live here: credentials stay in the secret store (S096)
// and are referenced from YAML by secret ID, so the file remains safely
// customer-readable at 0644.
package desired

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"homestead/internal/layout"

	"gopkg.in/yaml.v3"
)

// FileName is the desired-state file inside EtcDir.
const FileName = "desired.yaml"

// CurrentVersion is the latest envelope version.
const CurrentVersion = 1

// fileMode keeps desired state customer-readable (ADR-003).
const fileMode = 0o644

// ErrNotFound reports a missing desired-state file. Service layers use
// errors.Is to distinguish "unconfigured yet" from real I/O failures.
var ErrNotFound = errors.New("desired state not found")

// DefaultPath returns the production desired-state path. The parent
// directory is provisioned by S012; like the store, Load/Save fail loudly
// instead of scattering state.
func DefaultPath() string {
	return filepath.Join(layout.EtcDir.Path, FileName)
}

// State is the versioned desired-configuration envelope.
//
// Settings holds raw setting values; their meaning and validation come from
// the configuration schema (S015). Strict file parsing still applies to the
// envelope; per-setting validation is the schema's job.
type State struct {
	Version  int            `yaml:"version" json:"version"`
	Settings map[string]any `yaml:"settings,omitempty" json:"settings"`
}

// New returns a current-version State.
func New() State {
	return State{Version: CurrentVersion}
}

// Validate rejects envelopes this binary cannot interpret.
func (s State) Validate() error {
	if s.Version <= 0 {
		return fmt.Errorf("desired: missing version (want %d)", CurrentVersion)
	}
	if s.Version > CurrentVersion {
		return fmt.Errorf("desired: version %d newer than supported %d (upgrade homestead)", s.Version, CurrentVersion)
	}
	return nil
}

// Load reads and strictly parses the file at path. Unknown fields are
// rejected so typos fail loudly instead of silently doing nothing.
func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, fmt.Errorf("desired: read %s: %w", path, ErrNotFound)
		}
		return State{}, fmt.Errorf("desired: read %s: %w", path, err)
	}
	var s State
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&s); err != nil {
		return State{}, fmt.Errorf("desired: parse %s: %w", path, err)
	}
	if err := s.Validate(); err != nil {
		return State{}, err
	}
	return s, nil
}

// Save validates, marshals, and atomically replaces the file at path via
// temp-file + rename, so readers never observe a half-written document.
func Save(path string, s State) error {
	if err := s.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(s); err != nil {
		return fmt.Errorf("desired: encode: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("desired: encode: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".desired-*.tmp")
	if err != nil {
		return fmt.Errorf("desired: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("desired: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("desired: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("desired: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, fileMode); err != nil {
		return fmt.Errorf("desired: chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("desired: replace %s: %w", path, err)
	}
	return nil
}
