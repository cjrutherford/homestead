package desired

import (
	"encoding/json"
	"errors"
	"sort"
)

// ImportChange is one settings key an import would add, change or
// remove. Values are settings, never secrets.
type ImportChange struct {
	Key    string `json:"key"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// ImportPreview is the read-only answer to "what would importing this
// document do" (S483). It never writes. Secrets lists the names the
// import will demand; their values are never part of a document.
type ImportPreview struct {
	Valid   bool           `json:"valid"`
	Error   string         `json:"error,omitempty"`
	Changes []ImportChange `json:"changes"`
	Secrets []string       `json:"secrets"`
	// Document is the parsed document, handed back so a client can
	// confirm without parsing YAML itself.
	Document *Export `json:"document,omitempty"`
}

// PreviewImport parses and validates document text (YAML or JSON)
// and diffs its settings against the current desired state at
// desiredPath. Settings are checked against the config schema so
// the browser form shows the same errors an import would refuse
// with. Nothing is written.
func PreviewImport(text []byte, desiredPath string, validate SettingsValidator) ImportPreview {
	out := ImportPreview{Changes: []ImportChange{}, Secrets: []string{}}
	export, err := ParseExport(text)
	if err == nil {
		err = ValidateImport(export)
	}
	if err == nil && validate != nil {
		err = validate(export.Settings)
	}
	if err != nil {
		out.Error = err.Error()
		return out
	}
	current := map[string]any{}
	state, err := Load(desiredPath)
	if err != nil && !errors.Is(err, ErrNotFound) {
		out.Error = err.Error()
		return out
	}
	if err == nil && state.Settings != nil {
		current = state.Settings
	}
	keys := map[string]bool{}
	for k := range current {
		keys[k] = true
	}
	for k := range export.Settings {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		before, had := current[k]
		after, has := export.Settings[k]
		if had && has && sameValue(before, after) {
			continue
		}
		out.Changes = append(out.Changes, ImportChange{Key: k, Before: before, After: after})
	}
	if export.Secrets != nil {
		out.Secrets = append(out.Secrets, export.Secrets...)
	}
	out.Valid = true
	out.Document = &export
	return out
}

func sameValue(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
