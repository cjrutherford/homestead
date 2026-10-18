package craftfiles

import (
	"time"

	"homestead/internal/schema"
)

// Settings keys (desired configuration, ADR-004 parity: the config API,
// CLI and dashboard all render them from this schema).
const (
	SettingMaxUploadMB = "craft_files_max_upload_mb"
	SettingTimeoutMin  = "craft_files_timeout_minutes"
	SettingPerSession  = "craft_files_per_session"
)

// SchemaFields defines the proxy limits (ADR-029).
func SchemaFields() []schema.Field {
	return []schema.Field{
		{Name: SettingMaxUploadMB, Type: schema.TypeInteger, Group: "Craft file storage",
			Description: "Largest file a craft app may save to Nextcloud, in MiB. Larger uploads are refused with 413.",
			Default:     512, Min: schema.Int(1), Max: schema.Int(8192)},
		{Name: SettingTimeoutMin, Type: schema.TypeInteger, Group: "Craft file storage", Advanced: true,
			Description: "Longest one craft file request may take, in minutes.",
			Default:     30, Min: schema.Int(1), Max: schema.Int(720)},
		{Name: SettingPerSession, Type: schema.TypeInteger, Group: "Craft file storage", Advanced: true,
			Description: "Most concurrent file requests one craft session may have; more get 429.",
			Default:     8, Min: schema.Int(1), Max: schema.Int(64)},
	}
}

// LimitsOf reads the limits from settings; missing or malformed values
// take the defaults.
func LimitsOf(settings map[string]any) Limits {
	num := func(key string) int64 {
		switch v := settings[key].(type) {
		case int:
			return int64(v)
		case int64:
			return v
		case float64:
			return int64(v)
		}
		return 0
	}
	var l Limits
	if n := num(SettingMaxUploadMB); n > 0 {
		l.MaxUpload = n << 20
	}
	if n := num(SettingTimeoutMin); n > 0 {
		l.Timeout = time.Duration(n) * time.Minute
	}
	if n := num(SettingPerSession); n > 0 {
		l.PerSession = int(n)
	}
	return l.withDefaults()
}
