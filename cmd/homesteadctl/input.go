package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// loadSettingsFile reads typed settings from a JSON or YAML file (chosen by
// extension). It accepts either a bare settings object or the API envelope
// {"settings": {...}}; --set key=value flags are deliberately unsupported —
// flat strings cannot carry typed values without schema-guided coercion,
// which would contradict the schema's non-coercing validation.
func loadSettingsFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc map[string]any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("unsupported extension %q (want .json, .yaml, .yml)", filepath.Ext(path))
	}
	if doc == nil {
		return nil, fmt.Errorf("parse %s: empty document", path)
	}
	if inner, ok := doc["settings"]; ok {
		envelope, ok := inner.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("parse %s: \"settings\" must be an object", path)
		}
		if envelope == nil {
			return map[string]any{}, nil
		}
		return envelope, nil
	}
	return doc, nil
}
