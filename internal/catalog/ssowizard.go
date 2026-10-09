package catalog

import (
	"database/sql"
	"fmt"
	"time"

	"homestead/internal/identity"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// SSOWizardID identifies the per-app SSO setup flow in the generic
// wizard store (S036). Payloads carry package, provider, and protocol
// only — non-sensitive selections that revalidate against the catalog
// and registry at plan and apply time, so saved state never pins a
// package or provider that later vanishes.
const SSOWizardID = "sso"

// SSOSteps orders the flow: choose application, choose provider and
// protocol, review the integration, done. Client configuration inside
// the application belongs to actuation; the wizard records intent,
// and review states the recovery boundary out loud.
var SSOSteps = []string{"package", "provider", "review", "done"}

// ssoStepIndex orders steps; -1 means unknown.
func ssoStepIndex(step string) int {
	for i, name := range SSOSteps {
		if name == step {
			return i
		}
	}
	return -1
}

// Integration is one application's SSO intent: which provider speaks
// for it over which protocol. LocalFallback always holds —
// Homestead administrator login and socket recovery stay local and
// independent of the provider, so guiding app logins can never
// strand appliance recovery — and the recovery note says so on every
// reviewed and recorded plan.
type Integration struct {
	Package       string
	Provider      string
	Protocol      identity.Capability
	LocalFallback bool
	RecoveryNote  string
	Actor         string
}

// Wizard guides per-app SSO integration over the wizard store, the
// catalog, and the identity registry. It owns no HTTP surface: the
// generic wizard endpoints and later slices drive it. It never
// touches administrator identities or sessions — recovery
// independence holds by construction, not by caution.
type Wizard struct {
	wizards    *wizard.Service
	catalogDir string
	providers  *identity.Registry
	db         *sql.DB
}

// NewWizard returns a Wizard.
func NewWizard(wizards *wizard.Service, catalogDir string, providers *identity.Registry, s *store.Store) *Wizard {
	return &Wizard{wizards: wizards, catalogDir: catalogDir, providers: providers, db: s.DB()}
}

// decodePackage renders the package payload into a catalog
// manifest whose SSO class exceeds unsupported: unsupported apps
// refuse here, before any provider enters the picture.
func (w *Wizard) decodePackage(payload map[string]any) (Manifest, error) {
	packageName, _ := payload["package"].(string)
	if packageName == "" {
		return Manifest{}, fmt.Errorf("catalog: package step needs a package")
	}
	entries, err := Load(w.catalogDir)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := entries.Get(packageName)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.SSO.Class.Effective() == SSOUnsupported {
		return Manifest{}, fmt.Errorf("catalog: package %q does not support SSO", packageName)
	}
	return manifest, nil
}

// decodeSelection renders the payload into a validated triple: a
// supported package, a registered provider, and a protocol both
// sides speak. Every refusal names its side, so owners see whether
// the app, the provider, or the pairing needs attention.
func (w *Wizard) decodeSelection(payload map[string]any) (Manifest, string, identity.Capability, error) {
	manifest, err := w.decodePackage(payload)
	if err != nil {
		return Manifest{}, "", "", err
	}
	providerName, _ := payload["provider"].(string)
	if providerName == "" {
		return Manifest{}, "", "", fmt.Errorf("catalog: provider step needs a provider")
	}
	provider, err := w.providers.Get(providerName)
	if err != nil {
		return Manifest{}, "", "", err
	}
	rawProtocol, _ := payload["protocol"].(string)
	protocol := identity.Capability(rawProtocol)
	if !protocol.Valid() {
		return Manifest{}, "", "", fmt.Errorf("catalog: SSO protocol %q unknown", rawProtocol)
	}
	supported := false
	for _, candidate := range manifest.SSO.Protocols {
		if candidate == protocol {
			supported = true
			break
		}
	}
	if !supported {
		return Manifest{}, "", "", fmt.Errorf("catalog: package %q does not speak %s", manifest.Name, protocol)
	}
	offered := false
	for _, candidate := range provider.Capabilities() {
		if candidate == protocol {
			offered = true
			break
		}
	}
	if !offered {
		return Manifest{}, "", "", fmt.Errorf("catalog: provider %q does not speak %s", providerName, protocol)
	}
	return manifest, providerName, protocol, nil
}

// Save validates the payload for step and persists it: the package
// from the first step, provider and protocol from the second, review
// rendering the plan without writing anything further.
func (w *Wizard) Save(step string, payload map[string]any) error {
	if ssoStepIndex(step) < 0 {
		return fmt.Errorf("catalog: unknown step %q", step)
	}
	// The package step carries no provider yet: the package alone
	// validates, and later steps demand the whole triple.
	if ssoStepIndex(step) == ssoStepIndex("package") {
		if _, err := w.decodePackage(payload); err != nil {
			return err
		}
	} else if _, _, _, err := w.decodeSelection(payload); err != nil {
		return err
	}
	_, err := w.wizards.Put(SSOWizardID, step, payload)
	return err
}

// selection loads the saved payload.
func (w *Wizard) selection() (map[string]any, error) {
	state, err := w.wizards.Get(SSOWizardID)
	if err != nil {
		return nil, err
	}
	return state.Payload, nil
}

// Plan returns the reviewable outcome: the validated integration as
// it would record, without writing anything. The recovery note is
// part of the plan, not commentary beside it.
func (w *Wizard) Plan() (Integration, error) {
	payload, err := w.selection()
	if err != nil {
		return Integration{}, err
	}
	manifest, provider, protocol, err := w.decodeSelection(payload)
	if err != nil {
		return Integration{}, err
	}
	return Integration{
		Package:       manifest.Name,
		Provider:      provider,
		Protocol:      protocol,
		LocalFallback: true,
		RecoveryNote: fmt.Sprintf("Homestead administrator login and socket recovery stay local and independent of %s; only %s app logins change.",
			provider, manifest.Name),
	}, nil
}

// Apply records the saved integration under the acting owner,
// replacing any earlier intent for the package: one app, one
// provider, no fossilized pairings.
func (w *Wizard) Apply(actor string) (Integration, error) {
	if actor == "" {
		return Integration{}, fmt.Errorf("catalog: actor must not be empty")
	}
	plan, err := w.Plan()
	if err != nil {
		return Integration{}, err
	}
	plan.Actor = actor
	if _, err := w.db.Exec(
		`INSERT INTO sso_integrations (package, provider, protocol, actor, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(package) DO UPDATE SET provider = excluded.provider, protocol = excluded.protocol, actor = excluded.actor, updated_at = excluded.updated_at`,
		plan.Package, plan.Provider, string(plan.Protocol), actor,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return Integration{}, fmt.Errorf("catalog: record integration: %w", err)
	}
	return plan, nil
}

// Integrations lists recorded intents in package order: one row per
// integrated application.
func (w *Wizard) Integrations() ([]Integration, error) {
	rows, err := w.db.Query(`SELECT package, provider, protocol, actor FROM sso_integrations ORDER BY package`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list integrations: %w", err)
	}
	defer rows.Close()
	var out []Integration
	for rows.Next() {
		var record Integration
		var protocol string
		if err := rows.Scan(&record.Package, &record.Provider, &protocol, &record.Actor); err != nil {
			return nil, fmt.Errorf("catalog: scan integration: %w", err)
		}
		record.Protocol = identity.Capability(protocol)
		record.LocalFallback = true
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: list integrations: %w", err)
	}
	return out, nil
}
