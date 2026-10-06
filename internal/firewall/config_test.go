package firewall

import (
	"testing"
)

func TestSchemaFieldsValid(t *testing.T) {
	schema := Schema()
	if err := schema.ValidateDefinition(); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
	field, ok := schema.Field(ModeField)
	if !ok || len(field.Choices) != 2 {
		t.Fatalf("mode field = %+v, %v", field, ok)
	}
	defaults := schema.Defaults()
	if defaults[ModeField] != ModeManaged {
		t.Fatalf("defaults = %v, want managed", defaults)
	}
	if errs := schema.Validate(map[string]any{ModeField: "bogus"}); len(errs) == 0 {
		t.Fatal("bogus mode accepted, want error")
	}
}

func TestModeOf(t *testing.T) {
	if got := ModeOf(nil); got != ModeManaged {
		t.Fatalf("nil = %q", got)
	}
	if got := ModeOf(map[string]any{}); got != ModeManaged {
		t.Fatalf("empty = %q", got)
	}
	if got := ModeOf(map[string]any{ModeField: ModeExternal}); got != ModeExternal {
		t.Fatalf("external = %q", got)
	}
	if got := ModeOf(map[string]any{ModeField: "bogus"}); got != ModeManaged {
		t.Fatalf("bogus = %q, want managed default", got)
	}
	if got := ModeOf(map[string]any{ModeField: 42}); got != ModeManaged {
		t.Fatalf("non-string = %q, want managed default", got)
	}
}
