package catalog

import (
	"testing"

	"homestead/internal/identity"
)

func TestSSODeclarationValidate(t *testing.T) {
	valid := []SSODeclaration{
		{},
		{Class: SSOUnsupported},
		{Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC}},
		{Class: SSORecommended, Protocols: []identity.Capability{identity.CapabilityOIDC, identity.CapabilitySAML}},
		{Class: SSORequired, Protocols: []identity.Capability{identity.CapabilityLDAP}},
	}
	for _, declaration := range valid {
		if err := declaration.Validate(); err != nil {
			t.Fatalf("Validate(%+v): %v", declaration, err)
		}
	}
	invalid := []SSODeclaration{
		{Class: "sometimes"},
		{Class: SSOOptional, Protocols: []identity.Capability{"telepathy"}},
		{Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC, identity.CapabilityOIDC}},
		{Class: SSOUnsupported, Protocols: []identity.Capability{identity.CapabilityOIDC}},
		{Class: SSOOptional},
		{Class: SSORequired},
	}
	for _, declaration := range invalid {
		if err := declaration.Validate(); err == nil {
			t.Fatalf("Validate(%+v) = nil", declaration)
		}
	}
}

func TestSSOClassEffective(t *testing.T) {
	if SSOClass("").Effective() != SSOUnsupported {
		t.Fatal("empty class reads otherwise")
	}
	for _, class := range []SSOClass{SSOUnsupported, SSOOptional, SSORecommended, SSORequired} {
		if class.Effective() != class {
			t.Fatalf("class %q normalizes otherwise", class)
		}
	}
}

// TestCuratedSSO pins every shipped package's posture: clients declare
// optional with the protocols their upstream supports, and the
// provider itself declares unsupported.
func TestCuratedSSO(t *testing.T) {
	catalog := repoCatalog(t)
	want := map[string]SSODeclaration{
		"authentik":     {Class: SSOUnsupported},
		"immich":        {Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC}},
		"jellyfin":      {Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC, identity.CapabilitySAML}},
		"nextcloud":     {Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC, identity.CapabilitySAML, identity.CapabilityLDAP}},
		"paperless-ngx": {Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC}},
		"vaultwarden":   {Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC}},
		"wordcraft":     {Class: SSOUnsupported},
		"gridcraft":     {Class: SSOUnsupported},
		"pdfcraft":      {Class: SSOUnsupported},
		"deckcraft":     {Class: SSOUnsupported},
		"vectorcraft":   {Class: SSOUnsupported},
		"photocraft":    {Class: SSOUnsupported},
		"lightcraft":    {Class: SSOUnsupported},
		"linkding":      {Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC}},
		"mealie":        {Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC}},
		"figcraft":      {Class: SSOUnsupported},
		"filmcraft":     {Class: SSOUnsupported},
		"freshrss":      {Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC}},
		"gitea":         {Class: SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC}},
		"effectcraft":   {Class: SSOUnsupported},
		"soundcraft":    {Class: SSOUnsupported},
		"uptime-kuma":   {Class: SSOUnsupported},
		"designcraft":   {Class: SSOUnsupported},
		"cadcraft":      {Class: SSOUnsupported},
	}
	for _, name := range catalog.Names() {
		manifest, err := catalog.Get(name)
		if err != nil {
			t.Fatalf("Get(%s): %v", name, err)
		}
		declared, ok := want[name]
		if !ok {
			t.Fatalf("package %q has no pinned SSO posture", name)
		}
		if manifest.SSO.Class.Effective() != declared.Class.Effective() {
			t.Fatalf("%s class = %q, want %q", name, manifest.SSO.Class, declared.Class)
		}
		if len(manifest.SSO.Protocols) != len(declared.Protocols) {
			t.Fatalf("%s protocols = %v, want %v", name, manifest.SSO.Protocols, declared.Protocols)
		}
		for i := range declared.Protocols {
			if manifest.SSO.Protocols[i] != declared.Protocols[i] {
				t.Fatalf("%s protocols = %v, want %v", name, manifest.SSO.Protocols, declared.Protocols)
			}
		}
	}
}
