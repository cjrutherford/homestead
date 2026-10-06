package schema

import (
	"path/filepath"
	"testing"

	"homestead/internal/desired"
)

// TestDesiredRoundTrip proves the S014 envelope carries schema-validated
// settings end to end: save through desired, load back, validate here.
func TestDesiredRoundTrip(t *testing.T) {
	s := exampleSchema()
	if err := s.ValidateDefinition(); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
	settings := map[string]any{
		"hostname": "homestead",
		"web_port": 8080,
		"theme":    "system",
	}
	for k, v := range s.Defaults() {
		if _, ok := settings[k]; !ok {
			settings[k] = v
		}
	}
	if errs := s.Validate(settings); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}
	path := filepath.Join(t.TempDir(), desired.FileName)
	if err := desired.Save(path, desired.State{Version: desired.CurrentVersion, Settings: settings}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := desired.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if errs := s.Validate(loaded.Settings); len(errs) != 0 {
		t.Fatalf("Validate after round trip: %v", errs)
	}
}
