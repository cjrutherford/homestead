package firewall

import (
	"homestead/internal/schema"
)

// Firewall management modes.
const (
	// ModeManaged has Homestead own its nftables table (default).
	ModeManaged = "managed"
	// ModeExternal leaves firewalling to the administrator; Homestead
	// only diagnoses and instructs (S057).
	ModeExternal = "external"
)

// ModeField is the flat settings key for the mode. The S015 framework is
// flat, so the key carries its section as a prefix.
const ModeField = "firewall_mode"

// SchemaFields defines the firewall settings for the daemon schema: just
// the mode for now. Domain slices extend their own sections; the config
// page renders these automatically and the CLI takes them via files.
func SchemaFields() []schema.Field {
	return []schema.Field{
		{
			Name:        ModeField,
			Type:        schema.TypeChoice,
			Description: "Who manages the host firewall: Homestead (managed) or the administrator (external).",
			Group:       "Firewall",
			Default:     ModeManaged,
			Choices:     []string{ModeManaged, ModeExternal},
		},
	}
}

// Schema returns the firewall settings schema.
func Schema() schema.Schema {
	return schema.Schema{Fields: SchemaFields()}
}

// ModeOf reads the mode from settings, defaulting to managed for missing
// or malformed values: protection stays on unless explicitly opted out.
func ModeOf(settings map[string]any) string {
	if mode, ok := settings[ModeField].(string); ok && mode == ModeExternal {
		return ModeExternal
	}
	return ModeManaged
}
