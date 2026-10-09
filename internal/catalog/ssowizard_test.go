package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/identity"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// stubIdentityProvider speaks its capabilities and always answers.
type stubIdentityProvider struct {
	name string
	caps []identity.Capability
}

func (s stubIdentityProvider) Name() string                        { return s.name }
func (s stubIdentityProvider) Capabilities() []identity.Capability { return s.caps }
func (s stubIdentityProvider) RequiredCredentials() []identity.CredentialField {
	return nil
}
func (s stubIdentityProvider) CheckConnection(_ context.Context, _ string, _ identity.Credentials) error {
	return nil
}

const ssoPhotosManifest = `name: photos
version: 1.0.0
title: Photos
blurb: Photo app you host.
description: Photo app description.
category: media
icon: mdi:image
services:
- name: web
  image: example/photos:1.0.0
  exposes: web
requirements:
  memorymb: 64
  storage:
  - class: application
    minbytes: 1048576
  ports: [8080]
exposure:
  package: photos
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
sso:
  class: optional
  protocols: [oidc, saml]
`

const ssoPlainManifest = `name: plain
version: 1.0.0
title: Plain
blurb: Plain app you host.
description: Plain app description.
category: media
icon: mdi:image
services:
- name: web
  image: example/plain:1.0.0
  exposes: web
requirements:
  memorymb: 64
  storage:
  - class: application
    minbytes: 1048576
  ports: [8081]
exposure:
  package: plain
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
sso:
  class: unsupported
`

// openSSOWizard builds a Wizard over a scratch store, a two-package
// catalog, and a registry with a full and a narrow provider.
func openSSOWizard(t *testing.T) *Wizard {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	catalogDir := filepath.Join(t.TempDir(), "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, body := range map[string]string{"photos.yaml": ssoPhotosManifest, "plain.yaml": ssoPlainManifest} {
		if err := os.WriteFile(filepath.Join(catalogDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	registry := identity.NewRegistry()
	if err := registry.Register(stubIdentityProvider{name: "idp", caps: []identity.Capability{identity.CapabilityOIDC, identity.CapabilitySAML}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register(stubIdentityProvider{name: "narrow", caps: []identity.Capability{identity.CapabilitySAML}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return NewWizard(wizard.New(s), catalogDir, registry, s)
}

func ssoPayload(packageName, provider, protocol string) map[string]any {
	return map[string]any{"package": packageName, "provider": provider, "protocol": protocol}
}

func TestSSOWizardPackageStep(t *testing.T) {
	w := openSSOWizard(t)
	if err := w.Save("package", map[string]any{"package": "photos"}); err != nil {
		t.Fatalf("Save package: %v", err)
	}
	for step, payload := range map[string]map[string]any{
		"package": {"package": "plain"},
	} {
		if err := w.Save(step, payload); err == nil || !strings.Contains(err.Error(), "does not support SSO") {
			t.Fatalf("unsupported Save = %v", err)
		}
	}
	if err := w.Save("package", map[string]any{"package": "ghost"}); err == nil {
		t.Fatal("unknown package saved")
	}
	if err := w.Save("package", map[string]any{}); err == nil {
		t.Fatal("empty package saved")
	}
	if err := w.Save("telepathy", map[string]any{"package": "photos"}); err == nil {
		t.Fatal("unknown step saved")
	}
}

func TestSSOWizardProviderStep(t *testing.T) {
	w := openSSOWizard(t)
	full := ssoPayload("photos", "idp", "oidc")
	if err := w.Save("provider", full); err != nil {
		t.Fatalf("Save provider: %v", err)
	}
	if err := w.Save("provider", ssoPayload("photos", "ghost", "oidc")); err == nil {
		t.Fatal("unknown provider saved")
	}
	if err := w.Save("provider", ssoPayload("photos", "idp", "ldap")); err == nil {
		t.Fatal("app-unsupported protocol saved")
	}
	if err := w.Save("provider", ssoPayload("photos", "narrow", "oidc")); err == nil {
		t.Fatal("provider-unsupported protocol saved")
	}
	if err := w.Save("provider", ssoPayload("photos", "idp", "telepathy")); err == nil {
		t.Fatal("unknown protocol saved")
	}
	if err := w.Save("provider", ssoPayload("plain", "idp", "oidc")); err == nil {
		t.Fatal("unsupported package saved")
	}
}

func TestSSOWizardPlanStatesRecovery(t *testing.T) {
	w := openSSOWizard(t)
	if err := w.Save("provider", ssoPayload("photos", "idp", "oidc")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	plan, err := w.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Package != "photos" || plan.Provider != "idp" || plan.Protocol != identity.CapabilityOIDC {
		t.Fatalf("plan = %+v", plan)
	}
	if !plan.LocalFallback {
		t.Fatalf("plan = %+v, want local fallback retained", plan)
	}
	for _, want := range []string{"photos", "idp", "stay local"} {
		if !strings.Contains(plan.RecoveryNote, want) {
			t.Fatalf("recovery note %q missing %q", plan.RecoveryNote, want)
		}
	}
}

func TestSSOWizardApplyRecords(t *testing.T) {
	w := openSSOWizard(t)
	if _, err := w.Apply("owner"); err == nil {
		t.Fatal("apply without selection succeeded")
	}
	if err := w.Save("review", ssoPayload("photos", "idp", "oidc")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := w.Apply(""); err == nil {
		t.Fatal("apply without actor succeeded")
	}
	applied, err := w.Apply("owner")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Actor != "owner" || applied.Package != "photos" {
		t.Fatalf("applied = %+v", applied)
	}
	records, err := w.Integrations()
	if err != nil {
		t.Fatalf("Integrations: %v", err)
	}
	if len(records) != 1 || records[0].Protocol != identity.CapabilityOIDC || records[0].Actor != "owner" {
		t.Fatalf("records = %+v", records)
	}
	// Re-applying replaces the pairing instead of fossilizing it.
	if err := w.Save("review", ssoPayload("photos", "idp", "saml")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := w.Apply("owner"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	records, err = w.Integrations()
	if err != nil {
		t.Fatalf("Integrations: %v", err)
	}
	if len(records) != 1 || records[0].Protocol != identity.CapabilitySAML {
		t.Fatalf("records = %+v", records)
	}
}
