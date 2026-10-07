package domain

import (
	"context"
	"fmt"
	"strings"

	"homestead/internal/dns"
	"homestead/internal/secret"
	"homestead/internal/wizard"
)

// WizardID identifies the domain setup flow in the generic wizard store
// (S036). Secrets never touch that payload (S019/S020, S096): values go
// to the secret store under dns_<provider>_<field>, and the credentials
// step below refuses payloads carrying them.
const WizardID = "domain"

// Steps orders the flow: choose provider, name the zone, store
// credentials, prove access, plan the Homestead subdomain, done.
var Steps = []string{"provider", "zone", "credentials", "validate", "subdomain", "done"}

// Next advances one step: "" starts, "done" ends, unknown steps fail.
func Next(step string) (string, error) {
	if step == "" {
		return Steps[0], nil
	}
	for i, name := range Steps {
		if name == step {
			if i+1 == len(Steps) {
				return "", nil
			}
			return Steps[i+1], nil
		}
	}
	return "", fmt.Errorf("domain: unknown step %q", step)
}

// Plan is the wizard outcome: which provider serves which zone under
// which Homestead base name. Reconciliation (S098) and exposure (S227)
// consume it; nothing here mutates provider state.
type Plan struct {
	Provider string
	Zone     string
	Base     string
	FQDN     string
}

// secretName maps a provider credential field to its secret-store name,
// shared with every reader so values meet calls without ever surfacing.
func secretName(provider, field string) string {
	return "dns_" + provider + "_" + field
}

// Service guides the domain flow over the wizard store, the secret
// store, and the provider registry. It owns no HTTP surface: the
// generic wizard endpoints and later slices drive it.
type Service struct {
	wizards  *wizard.Service
	secrets  *secret.Store
	registry *dns.Registry
}

// New returns a Service over the wizard store, secret store, and
// provider registry (S091 names implementations; S092+ provide them).
func New(wizards *wizard.Service, secrets *secret.Store, registry *dns.Registry) *Service {
	return &Service{wizards: wizards, secrets: secrets, registry: registry}
}

// Providers lists available provider names sorted.
func (s *Service) Providers() []string {
	return s.registry.Names()
}

// provider resolves a registry entry or an unknown-provider error.
func (s *Service) provider(name string) (dns.Provider, error) {
	return s.registry.Get(name)
}

// RequiredCredentials describes the secrets a provider needs.
func (s *Service) RequiredCredentials(provider string) ([]dns.CredentialField, error) {
	p, err := s.provider(provider)
	if err != nil {
		return nil, err
	}
	return p.RequiredCredentials(), nil
}

// selection reads the saved non-sensitive payload.
func (s *Service) selection() (map[string]any, error) {
	state, err := s.wizards.Get(WizardID)
	if err != nil {
		return nil, err
	}
	return state.Payload, nil
}

// Save validates the payload for step and persists it. Credential
// values are never valid payload content: the credentials step rejects
// keys matching required field names, directing them to StoreCredentials.
func (s *Service) Save(step string, payload map[string]any) error {
	if err := s.validatePayload(step, payload); err != nil {
		return err
	}
	_, err := s.wizards.Put(WizardID, step, payload)
	return err
}

// stepIndex orders steps; -1 means unknown.
func stepIndex(step string) int {
	for i, name := range Steps {
		if name == step {
			return i
		}
	}
	return -1
}

// validatePayload enforces cumulative requirements per step: every
// known step names a registered provider, zone and later add the zone,
// subdomain and done add the base. The credentials step additionally
// refuses credential values in the payload — they belong in secrets.
func (s *Service) validatePayload(step string, payload map[string]any) error {
	idx := stepIndex(step)
	if idx < 0 {
		return fmt.Errorf("domain: unknown step %q", step)
	}
	str := func(key string) string {
		value, _ := payload[key].(string)
		return value
	}
	provider := str("provider")
	if _, err := s.provider(provider); err != nil {
		return err
	}
	if idx >= stepIndex("zone") {
		if err := validateZoneName(str("zone")); err != nil {
			return err
		}
	}
	if step == "credentials" {
		fields, err := s.RequiredCredentials(provider)
		if err != nil {
			return err
		}
		for _, field := range fields {
			if _, present := payload[field.Name]; present {
				return fmt.Errorf("domain: credential %q belongs in the secret store, not wizard state", field.Name)
			}
		}
	}
	if idx >= stepIndex("subdomain") {
		if err := validateBaseName(str("base")); err != nil {
			return err
		}
	}
	return nil
}

// validateZoneName checks the zone origin shape against the shared DNS
// spelling (lowercase, relative).
func validateZoneName(zone string) error {
	if len(zone) == 0 || len(zone) > 253 {
		return fmt.Errorf("domain: zone must be 1-253 characters")
	}
	if strings.ToLower(zone) != zone {
		return fmt.Errorf("domain: zone must be lowercase")
	}
	if strings.HasSuffix(zone, ".") || strings.HasPrefix(zone, ".") || strings.Contains(zone, "..") {
		return fmt.Errorf("domain: zone must be a relative origin")
	}
	for _, label := range strings.Split(zone, ".") {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("domain: invalid zone label %q", label)
		}
		for _, r := range label {
			if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return fmt.Errorf("domain: invalid zone label %q", label)
			}
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("domain: invalid zone label %q", label)
		}
	}
	return nil
}

// validateBaseName checks the Homestead subdomain base: one lowercase
// label, the namespace platform services hang under.
func validateBaseName(base string) error {
	if len(base) == 0 || len(base) > 63 {
		return fmt.Errorf("domain: base must be 1-63 characters")
	}
	if strings.ToLower(base) != base {
		return fmt.Errorf("domain: base must be lowercase")
	}
	for _, r := range base {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("domain: invalid base %q", base)
		}
	}
	if strings.HasPrefix(base, "-") || strings.HasSuffix(base, "-") {
		return fmt.Errorf("domain: invalid base %q", base)
	}
	return nil
}

// StoreCredentials saves provider secret values under their conventional
// names. Every required field must be present and non-empty; extras are
// refused rather than silently kept.
func (s *Service) StoreCredentials(provider string, values map[string]string) error {
	fields, err := s.RequiredCredentials(provider)
	if err != nil {
		return err
	}
	if len(values) != len(fields) {
		return fmt.Errorf("domain: want %d credential(s) for %q", len(fields), provider)
	}
	for _, field := range fields {
		value, ok := values[field.Name]
		if !ok || value == "" {
			return fmt.Errorf("domain: missing credential %q", field.Name)
		}
		if err := s.secrets.Set(secretName(provider, field.Name), value); err != nil {
			return err
		}
	}
	return nil
}

// credentials assembles provider credentials from the secret store,
// naming the first missing field.
func (s *Service) credentials(provider string) (dns.Credentials, error) {
	fields, err := s.RequiredCredentials(provider)
	if err != nil {
		return nil, err
	}
	creds := dns.Credentials{}
	for _, field := range fields {
		value, err := s.secrets.Get(secretName(provider, field.Name))
		if err != nil {
			return nil, fmt.Errorf("domain: missing credential %q", field.Name)
		}
		creds[field.Name] = value
	}
	return creds, nil
}

// Validate proves the saved selection works: credentials assemble from
// the secret store and the provider lists the zone, confirming auth and
// access together. Manual has no API to prove against, so a valid zone
// passes with propagation checks deferred to real records (S092);
// that limitation rides in the result, never silently.
func (s *Service) Validate(ctx context.Context) error {
	payload, err := s.selection()
	if err != nil {
		return err
	}
	provider, _ := payload["provider"].(string)
	zone, _ := payload["zone"].(string)
	p, err := s.provider(provider)
	if err != nil {
		return err
	}
	if err := validateZoneName(zone); err != nil {
		return err
	}
	if provider == "manual" {
		return nil
	}
	creds, err := s.credentials(provider)
	if err != nil {
		return err
	}
	if _, err := p.ListRecords(ctx, zone, creds); err != nil {
		return fmt.Errorf("domain: validate %s/%s: %w", provider, zone, err)
	}
	return nil
}

// Plan returns the wizard outcome once provider, zone, and base are
// saved and valid. The FQDN is base.zone — the namespace platform
// services hang under.
func (s *Service) Plan() (Plan, error) {
	payload, err := s.selection()
	if err != nil {
		return Plan{}, err
	}
	provider, _ := payload["provider"].(string)
	zone, _ := payload["zone"].(string)
	base, _ := payload["base"].(string)
	if _, err := s.provider(provider); err != nil {
		return Plan{}, err
	}
	if err := validateZoneName(zone); err != nil {
		return Plan{}, err
	}
	if err := validateBaseName(base); err != nil {
		return Plan{}, err
	}
	return Plan{Provider: provider, Zone: zone, Base: base, FQDN: base + "." + zone}, nil
}
