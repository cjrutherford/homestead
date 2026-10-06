// Package schema defines Homestead's typed configuration metadata (S015).
//
// A Schema describes the settings a domain owns: each Field carries its
// type, description, defaults, choices, and UX hints (sensitive/advanced)
// so homesteadctl and the dashboard render and validate from the same
// source (ADR-004). Schemas are code-defined and ship with the daemon;
// domain slices register their own fields, the dynamic renderer (S034)
// consumes them, and the serving API (S016) validates through them.
//
// This package owns the framework only. It defines no appliance settings
// itself and performs no I/O: storage stays in internal/desired (S014).
package schema

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Type is a setting value type.
type Type string

const (
	TypeString  Type = "string"
	TypeInteger Type = "integer"
	TypeBoolean Type = "boolean"
	// TypeChoice is a string restricted to Choices.
	TypeChoice Type = "choice"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Int returns a pointer for optional integer bounds (Min/Max).
func Int(v int) *int {
	return &v
}

// Field describes one configuration setting.
type Field struct {
	// Name is the settings-map key: lowercase letters, digits, underscores.
	Name string
	Type Type
	// Description is the human-readable help shown in CLI and dashboard.
	Description string
	// Group optionally groups fields for UI rendering; "" leaves ungrouped.
	Group string
	// Required fails validation when the setting is absent.
	Required bool
	// Default applies when the setting is absent; must itself validate.
	Default any
	// Choices lists allowed values; TypeChoice only, must be non-empty.
	Choices []string
	// Min/Max bound TypeInteger values when non-nil.
	Min *int
	Max *int
	// Sensitive marks secrets: UIs mask the value, logs redact it.
	Sensitive bool
	// Advanced hides the field from simple views; wizards still offer it.
	Advanced bool
}

// FieldError describes one validation failure. JSON tags serve the S016 API.
type FieldError struct {
	Field  string `json:"field"`
	Value  any    `json:"value,omitempty"`
	Reason string `json:"reason"`
}

func (e FieldError) Error() string {
	return fmt.Sprintf("setting %q: %s (got %v)", e.Field, e.Reason, e.Value)
}

// Schema is an ordered set of configuration fields.
type Schema struct {
	Fields []Field
}

// ValidateDefinition checks the schema itself: names, types, choices,
// bounds, and defaults. Call it in tests for every domain schema.
func (s Schema) ValidateDefinition() error {
	seen := map[string]bool{}
	for i, f := range s.Fields {
		where := fmt.Sprintf("field #%d", i)
		if f.Name != "" {
			where = fmt.Sprintf("field %q", f.Name)
		}
		if !validName.MatchString(f.Name) {
			return fmt.Errorf("schema: %s: invalid name (want %s)", where, validName)
		}
		if seen[f.Name] {
			return fmt.Errorf("schema: %s: duplicate name", where)
		}
		seen[f.Name] = true
		switch f.Type {
		case TypeString, TypeInteger, TypeBoolean, TypeChoice:
		default:
			return fmt.Errorf("schema: %s: unknown type %q", where, f.Type)
		}
		if f.Description == "" {
			return fmt.Errorf("schema: %s: missing description", where)
		}
		if f.Type == TypeChoice {
			if len(f.Choices) == 0 {
				return fmt.Errorf("schema: %s: choice needs options", where)
			}
		} else if len(f.Choices) > 0 {
			return fmt.Errorf("schema: %s: choices only apply to choice fields", where)
		}
		if (f.Min != nil || f.Max != nil) && f.Type != TypeInteger {
			return fmt.Errorf("schema: %s: min/max only apply to integer fields", where)
		}
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			return fmt.Errorf("schema: %s: min %d exceeds max %d", where, *f.Min, *f.Max)
		}
		if f.Default != nil {
			if err := f.checkValue(f.Default); err != nil {
				return fmt.Errorf("schema: %s: invalid default: %w", where, err)
			}
		}
	}
	return nil
}

// Field returns the field definition for name.
func (s Schema) Field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Defaults returns the default for every field that defines one.
func (s Schema) Defaults() map[string]any {
	out := map[string]any{}
	for _, f := range s.Fields {
		if f.Default != nil {
			out[f.Name] = f.Default
		}
	}
	return out
}

// Validate checks settings against the schema: unknown keys, missing
// required fields, and per-field type/bounds/choice violations. It returns
// nil when valid; errors sort by field name for stable output.
func (s Schema) Validate(settings map[string]any) []FieldError {
	var errs []FieldError
	for key, value := range settings {
		f, ok := s.Field(key)
		if !ok {
			errs = append(errs, FieldError{Field: key, Value: value, Reason: "unknown setting"})
			continue
		}
		if err := f.checkValue(value); err != nil {
			errs = append(errs, FieldError{Field: key, Value: value, Reason: err.Error()})
		}
	}
	for _, f := range s.Fields {
		if f.Required {
			if _, ok := settings[f.Name]; !ok {
				errs = append(errs, FieldError{Field: f.Name, Reason: "required setting missing"})
			}
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	return errs
}

func (f Field) checkValue(value any) error {
	switch f.Type {
	case TypeString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("want string")
		}
	case TypeInteger:
		n, ok := toInt(value)
		if !ok {
			return fmt.Errorf("want integer")
		}
		if f.Min != nil && n < *f.Min {
			return fmt.Errorf("want >= %d", *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return fmt.Errorf("want <= %d", *f.Max)
		}
	case TypeBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("want boolean")
		}
	case TypeChoice:
		str, ok := value.(string)
		if !ok {
			return fmt.Errorf("want one of %s", strings.Join(f.Choices, ", "))
		}
		for _, c := range f.Choices {
			if str == c {
				return nil
			}
		}
		return fmt.Errorf("want one of %s", strings.Join(f.Choices, ", "))
	}
	return nil
}

func toInt(value any) (int, bool) {
	switch n := value.(type) {
	case int:
		return n, true
	case int64:
		if n > int64(math.MaxInt) || n < int64(math.MinInt) {
			return 0, false
		}
		return int(n), true
	case float64:
		// JSON numbers decode as float64; integral ones are integers.
		// math.MaxInt is not float64-representable, so >= excludes 2^63
		// (not a valid int) while losing nothing representable.
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return 0, false
		}
		if n >= float64(math.MaxInt) || n < float64(math.MinInt) {
			return 0, false
		}
		return int(n), true
	}
	return 0, false
}
