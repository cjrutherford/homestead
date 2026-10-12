package cert

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/domain"
	"homestead/internal/secret"
	"homestead/internal/wizard"
)

// TLSWizardID identifies the TLS setup flow in the generic wizard store
// (S036). Like the domain wizard (S097), secrets never touch the
// payload; this flow only names credentials, which live in the secret
// store under the shared convention.
const TLSWizardID = "tls"

// TLSSteps orders the flow: choose certificate strategy, prove
// prerequisites, validate issued material, done. Issuance itself runs
// between prerequisites and validation through the S103/S104
// mechanics at the caller's layer — the wizard guides and verifies,
// never fetches.
var TLSSteps = []string{"strategy", "prerequisites", "validation", "done"}

// tlsStepIndex orders steps; -1 means unknown.
func tlsStepIndex(step string) int {
	for i, name := range TLSSteps {
		if name == step {
			return i
		}
	}
	return -1
}

// Prereq reports one certificate's issuance readiness: whether anything
// blocks fetching its next certificate, and what.
type Prereq struct {
	Certificate string
	Ready       bool
	Detail      string
}

// TLSPlan is the wizard outcome: the strategy set plus per-certificate
// readiness. Callers issue from it, then validate the material back
// through ValidateIssued.
type TLSPlan struct {
	Certificates []Certificate
	Prereqs      []Prereq
}

// Wizard guides TLS setup over the wizard store, secret store, provider
// registry, and optionally the internal authority. It owns no HTTP
// surface: the generic wizard endpoints and later slices drive it.
type Wizard struct {
	wizards   *wizard.Service
	secrets   *secret.Store
	providers *dns.Registry
	authority *Authority
	newClient func(string) (*Client, error)
}

// NewWizard returns a Wizard. Authority may be nil, in which case
// internal-issuer prerequisites report unconfigured rather than failing
// construction — the CA can arrive after strategy.
func NewWizard(wizards *wizard.Service, secrets *secret.Store, providers *dns.Registry, authority *Authority) *Wizard {
	return &Wizard{wizards: wizards, secrets: secrets, providers: providers, authority: authority, newClient: NewClient}
}

// decodeCertificates renders the strategy payload back into a validated
// set: JSON round-trip keeps the wizard store format-agnostic while the
// model stays authoritative.
func decodeCertificates(payload map[string]any) (Set, error) {
	raw, ok := payload["certificates"]
	if !ok {
		return Set{}, fmt.Errorf("cert: strategy needs certificates")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return Set{}, fmt.Errorf("cert: strategy encode: %w", err)
	}
	var certificates []Certificate
	if err := json.Unmarshal(encoded, &certificates); err != nil {
		return Set{}, fmt.Errorf("cert: strategy decode: %w", err)
	}
	set := Set{Certificates: certificates}
	if len(set.Certificates) == 0 {
		return Set{}, fmt.Errorf("cert: strategy needs at least one certificate")
	}
	if err := set.Validate(); err != nil {
		return Set{}, err
	}
	return set, nil
}

// strategyCoherence enforces name-issuer compatibility beyond shape:
// private names terminate internally or by owner hand (no public CA can
// issue for them, S101), while public names accept any issuer.
func strategyCoherence(set Set) error {
	for _, certificate := range set.Certificates {
		for _, name := range certificate.DNSNames {
			if isPrivateCertName(name) &&
				certificate.Issuer.Type != IssuerInternal &&
				certificate.Issuer.Type != IssuerManual {
				return fmt.Errorf("cert: %q is private, needs internal or manual issuance", name)
			}
		}
	}
	return nil
}

// isPrivateCertName applies the shared automation boundary (S101):
// single labels, private/reserved suffixes, and IP literals never face
// public ACME. Wildcards strip to base for the test.
func isPrivateCertName(name string) bool {
	base := name
	if len(base) > 2 && base[:2] == "*." {
		base = base[2:]
	}
	return docker.IsPrivateName(docker.Hostname(base))
}

// Save validates the payload for step and persists it.
func (w *Wizard) Save(step string, payload map[string]any) error {
	idx := tlsStepIndex(step)
	if idx < 0 {
		return fmt.Errorf("cert: unknown step %q", step)
	}
	set, err := decodeCertificates(payload)
	if err != nil {
		return err
	}
	if err := strategyCoherence(set); err != nil {
		return err
	}
	_, err = w.wizards.Put(TLSWizardID, step, payload)
	return err
}

// strategy loads the saved strategy set.
func (w *Wizard) strategy() (Set, error) { return LoadStrategy(w.wizards) }

// LoadStrategy reads the saved TLS strategy set from the wizard store.
// The renewal check (S263) reads it without a full Wizard.
func LoadStrategy(wizards *wizard.Service) (Set, error) {
	state, err := wizards.Get(TLSWizardID)
	if err != nil {
		return Set{}, err
	}
	set, err := decodeCertificates(state.Payload)
	if err != nil {
		return Set{}, err
	}
	if err := strategyCoherence(set); err != nil {
		return Set{}, err
	}
	return set, nil
}

// Prerequisites proves issuance readiness per certificate without
// fetching anything: dns-01 needs present credentials plus a reachable
// directory, internal needs a configured authority, manual and direct
// challenges need nothing checkable here (reachability proves at
// issuance, S101).
func (w *Wizard) Prerequisites(ctx context.Context) ([]Prereq, error) {
	set, err := w.strategy()
	if err != nil {
		return nil, err
	}
	prereqs := make([]Prereq, 0, len(set.Certificates))
	for _, certificate := range set.Certificates {
		prereqs = append(prereqs, w.prereq(ctx, certificate))
	}
	return prereqs, nil
}

// prereq checks one certificate's readiness.
func (w *Wizard) prereq(ctx context.Context, certificate Certificate) Prereq {
	prereq := Prereq{Certificate: certificate.Name}
	switch issuer := certificate.Issuer; issuer.Type {
	case IssuerInternal:
		if w.authority == nil {
			prereq.Detail = "internal CA not configured"
			return prereq
		}
		prereq.Ready = true
		prereq.Detail = "internal authority configured"
	case IssuerManual:
		prereq.Ready = true
		prereq.Detail = "owner supplies the certificate"
	case IssuerACME:
		if issuer.Challenge != ChallengeDNS01 {
			prereq.Ready = true
			prereq.Detail = "direct challenges prove at issuance"
			return prereq
		}
		provider, err := w.providers.Get(issuer.DNSProvider)
		if err != nil {
			prereq.Detail = fmt.Sprintf("unknown DNS provider %q", issuer.DNSProvider)
			return prereq
		}
		for _, field := range provider.RequiredCredentials() {
			if _, err := w.secrets.Get(domain.SecretName(provider.Name(), field.Name)); err != nil {
				prereq.Detail = fmt.Sprintf("missing credential %q", field.Name)
				return prereq
			}
		}
		client, err := w.newClient(issuer.Directory)
		if err != nil {
			prereq.Detail = fmt.Sprintf("bad directory: %v", err)
			return prereq
		}
		if _, err := client.discover(ctx); err != nil {
			prereq.Detail = fmt.Sprintf("directory unreachable: %v", err)
			return prereq
		}
		prereq.Ready = true
		prereq.Detail = "credentials present, directory reachable"
	}
	return prereq
}

// Plan returns the strategy plus current readiness.
func (w *Wizard) Plan(ctx context.Context) (TLSPlan, error) {
	set, err := w.strategy()
	if err != nil {
		return TLSPlan{}, err
	}
	prereqs, err := w.Prerequisites(ctx)
	if err != nil {
		return TLSPlan{}, err
	}
	return TLSPlan{Certificates: set.Certificates, Prereqs: prereqs}, nil
}

// ValidateIssued grades served material against the strategy (S105):
// the validation step proves what issuance produced.
func (w *Wizard) ValidateIssued(observed map[string][]byte, now time.Time) ([]Status, error) {
	set, err := w.strategy()
	if err != nil {
		return nil, err
	}
	return Check(set, observed, now)
}
