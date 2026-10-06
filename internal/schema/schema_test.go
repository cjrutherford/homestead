package schema

import (
	"math"
	"strings"
	"testing"
)

func exampleSchema() Schema {
	return Schema{Fields: []Field{
		{
			Name:        "hostname",
			Type:        TypeString,
			Description: "Local friendly name for the appliance.",
			Required:    true,
		},
		{
			Name:        "web_port",
			Type:        TypeInteger,
			Description: "Dashboard HTTP port.",
			Default:     8080,
			Min:         Int(1),
			Max:         Int(65535),
		},
		{
			Name:        "debug",
			Type:        TypeBoolean,
			Description: "Verbose logging.",
			Advanced:    true,
		},
		{
			Name:        "theme",
			Type:        TypeChoice,
			Description: "Dashboard theme.",
			Choices:     []string{"light", "dark", "system"},
			Default:     "system",
		},
		{
			Name:        "api_token",
			Type:        TypeString,
			Description: "Provider token.",
			Sensitive:   true,
		},
	}}
}

func TestExampleDefinitionIsValid(t *testing.T) {
	if err := exampleSchema().ValidateDefinition(); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
}

func TestDefinitionFailures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Field)
	}{
		{"bad name", func(f *Field) { f.Name = "Has-Dash" }},
		{"empty name", func(f *Field) { f.Name = "" }},
		{"unknown type", func(f *Field) { f.Type = "float" }},
		{"missing description", func(f *Field) { f.Description = "" }},
		{"choice without options", func(f *Field) {
			f.Type = TypeChoice
			f.Default = nil
		}},
		{"choices on string", func(f *Field) { f.Choices = []string{"a"} }},
		{"bounds on string", func(f *Field) { f.Min = Int(1) }},
		{"min above max", func(f *Field) {
			f.Type = TypeInteger
			f.Choices = nil
			f.Min = Int(9)
			f.Max = Int(1)
		}},
		{"bad default", func(f *Field) { f.Default = 42 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Field{Name: "probe", Type: TypeString, Description: "Probe."}
			tc.mutate(&f)
			if err := (Schema{Fields: []Field{f}}).ValidateDefinition(); err == nil {
				t.Fatal("ValidateDefinition accepted invalid field, want error")
			}
		})
	}
}

func TestDefinitionRejectsDuplicates(t *testing.T) {
	s := Schema{Fields: []Field{
		{Name: "dup", Type: TypeBoolean, Description: "First."},
		{Name: "dup", Type: TypeBoolean, Description: "Second."},
	}}
	if err := s.ValidateDefinition(); err == nil {
		t.Fatal("ValidateDefinition accepted duplicates, want error")
	}
}

func TestValidateAcceptsGoodSettings(t *testing.T) {
	settings := map[string]any{
		"hostname": "homestead",
		"web_port": 9090,
		"debug":    true,
		"theme":    "dark",
	}
	if errs := exampleSchema().Validate(settings); len(errs) != 0 {
		t.Fatalf("Validate errors = %v, want none", errs)
	}
}

func TestValidateFailures(t *testing.T) {
	errs := exampleSchema().Validate(map[string]any{
		"hostname": 42,
		"web_port": 70000,
		"debug":    "yes",
		"theme":    "neon",
		"bogus":    true,
	})
	if len(errs) != 5 {
		t.Fatalf("Validate errors = %v, want 5", errs)
	}
	byField := map[string]string{}
	for _, e := range errs {
		byField[e.Field] = e.Reason
	}
	for field, want := range map[string]string{
		"hostname": "want string",
		"web_port": "want <=",
		"debug":    "want boolean",
		"theme":    "want one of",
		"bogus":    "unknown setting",
	} {
		if reason, ok := byField[field]; !ok || !strings.Contains(reason, want) {
			t.Fatalf("field %q reason = %q, want containing %q", field, reason, want)
		}
	}
}

func TestValidateMissingRequired(t *testing.T) {
	errs := exampleSchema().Validate(map[string]any{})
	if len(errs) != 1 || errs[0].Field != "hostname" {
		t.Fatalf("Validate errors = %v, want missing hostname", errs)
	}
}

func TestValidateBelowMin(t *testing.T) {
	errs := exampleSchema().Validate(map[string]any{"hostname": "h", "web_port": 0})
	if len(errs) != 1 || !strings.Contains(errs[0].Reason, "want >=") {
		t.Fatalf("Validate errors = %v, want min violation", errs)
	}
}

func TestValidateErrorsSorted(t *testing.T) {
	errs := exampleSchema().Validate(map[string]any{"theme": "x", "bogus": 1, "debug": "y"})
	for i := 1; i < len(errs); i++ {
		if errs[i-1].Field > errs[i].Field {
			t.Fatalf("errors unsorted: %v", errs)
		}
	}
}

func TestDefaults(t *testing.T) {
	defaults := exampleSchema().Defaults()
	if defaults["web_port"] != 8080 || defaults["theme"] != "system" {
		t.Fatalf("Defaults = %v, want web_port 8080 and theme system", defaults)
	}
	if _, ok := defaults["hostname"]; ok {
		t.Fatalf("Defaults = %v, required field must have no default", defaults)
	}
}

func TestFieldLookup(t *testing.T) {
	f, ok := exampleSchema().Field("theme")
	if !ok || len(f.Choices) != 3 {
		t.Fatalf("Field(theme) = %+v, %v; want 3 choices", f, ok)
	}
	if _, ok := exampleSchema().Field("missing"); ok {
		t.Fatal("Field(missing) found, want absent")
	}
}

func TestFieldErrorMessage(t *testing.T) {
	msg := FieldError{Field: "web_port", Value: 1, Reason: "want >= 2"}.Error()
	if !strings.Contains(msg, "web_port") || !strings.Contains(msg, "want >= 2") {
		t.Fatalf("FieldError message = %q", msg)
	}
}

func TestIntegerAcceptsJSONNumbers(t *testing.T) {
	// JSON decodes numbers as float64; integral ones are integers.
	s := Schema{Fields: []Field{
		{Name: "port", Type: TypeInteger, Description: "Port.", Min: Int(1), Max: Int(65535)},
	}}
	if errs := s.Validate(map[string]any{"port": float64(8080)}); len(errs) != 0 {
		t.Fatalf("Validate float64(8080) = %v, want none", errs)
	}
	for _, bad := range []any{8080.5, "8080", true, nil} {
		if errs := s.Validate(map[string]any{"port": bad}); len(errs) == 0 {
			t.Fatalf("Validate(%v) accepted, want error", bad)
		}
	}
}

func TestIntegerRejectsOutOfRangeFloats(t *testing.T) {
	s := Schema{Fields: []Field{
		{Name: "n", Type: TypeInteger, Description: "N."},
	}}
	for _, bad := range []any{1e300, -1e300, math.Inf(1)} {
		if errs := s.Validate(map[string]any{"n": bad}); len(errs) == 0 {
			t.Fatalf("Validate(%v) accepted, want error", bad)
		}
	}
}
