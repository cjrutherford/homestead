// Package setup is the post-claim setup walkthrough's state (S287,
// ADR-034). It is orchestration only: it records the owner's choices
// (skipped steps, acknowledgements, the chosen DNS provider and access
// mode) in the resumable wizard store under the id "setup", and derives each
// step's state from what is actually true on the appliance, so a step done
// from the CLI or another page counts. Nothing here changes the appliance:
// every change goes through the existing plan, review and approve flows.
//
// The stored payload is non-sensitive by construction: it is the typed
// stored struct below and nothing else. Credentials go straight to the
// secret store (ADR-021) through the existing routes.
package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"homestead/internal/wizard"
)

// WizardID is the wizard-store id of the walkthrough.
const WizardID = "setup"

// Step names, in walkthrough order. They are the path segment of
// PUT /api/v1/setup/steps/{name} and the argument of `homesteadctl setup`.
const (
	StepAccount = "account"
	StepSignIn  = "sign-in"
	StepDomain  = "domain"
	StepApps    = "apps"
)

// StepNames lists the steps in order.
var StepNames = []string{StepAccount, StepSignIn, StepDomain, StepApps}

var titles = map[string]string{
	StepAccount: "Secure the owner account",
	StepSignIn:  "Sign-in",
	StepDomain:  "Domain and access",
	StepApps:    "First apps",
}

// Step states.
const (
	Pending = "pending"
	Done    = "done"
	Skipped = "skipped"
)

// Origins. A legacy appliance was claimed before the walkthrough shipped
// (migration 44 stamps it) and is never gated; a fresh one is.
const (
	OriginFresh  = "fresh"
	OriginLegacy = "legacy"
)

// Errors the API maps to status codes.
var (
	ErrUnknownStep = errors.New("unknown setup step")
	ErrInvalid     = errors.New("invalid setup request")
)

// SSOFacts says how far sign-in with Authentik has got.
type SSOFacts struct {
	Installed  bool `json:"authentik_installed"`
	Configured bool `json:"configured"`
	Linked     bool `json:"linked"`
	Dormant    bool `json:"dormant"`
}

// DomainFacts says what is recorded about the domain.
type DomainFacts struct {
	// Configured: a provider and zone are saved in the domain wizard.
	Configured bool   `json:"configured"`
	Provider   string `json:"provider,omitempty"`
	Zone       string `json:"zone,omitempty"`
	Base       string `json:"base,omitempty"`
	// Onboarded: the validated domain is recorded (S223).
	Onboarded bool `json:"onboarded"`
	// CredentialsSealed: every credential the provider takes is in the secret
	// store. Names and presence only; values are never read here.
	CredentialsSealed bool `json:"credentials_sealed"`
}

// StarterApp is one entry of the short curated list for step 4.
type StarterApp struct {
	Name      string `json:"name"`
	Title     string `json:"title"`
	Blurb     string `json:"blurb"`
	Installed bool   `json:"installed"`
}

// Facts are the observed facts the states derive from. Reading them never
// changes anything and never contacts a remote service.
type Facts struct {
	RecoveryExported     bool        `json:"recovery_exported"`
	RecoveryAcknowledged bool        `json:"recovery_acknowledged"`
	SignIn               SSOFacts    `json:"sign_in"`
	Domain               DomainFacts `json:"domain"`
	Tunnels              int         `json:"tunnels"`
	Exposures            int         `json:"exposures"`
	// ReviewExisting: a domain, tunnel or exposure already exists, so step 3
	// opens as a review of it, never a redo.
	ReviewExisting bool `json:"review_existing"`
	AppsInstalled  int  `json:"apps_installed"`
}

// Sources reads the facts. Each is a seam: production wires the real
// stores, tests wire fakes.
type Sources struct {
	Recovery func() (exported, acknowledged bool, err error)
	SignIn   func() (SSOFacts, error)
	Domain   func() (DomainFacts, error)
	Tunnels  func() (int, error)
	// Exposures counts approved exposure plans.
	Exposures func() (int, error)
	// Apps lists the starter apps and how many catalog apps (other than
	// Authentik) are installed.
	Apps func() (starter []StarterApp, installed int, err error)
}

// stored is the whole persisted payload. Keep it to choices and progress:
// a field added here is a field the wizard store holds in clear text.
type stored struct {
	Origin       string   `json:"origin,omitempty"`
	Skipped      []string `json:"skipped,omitempty"`
	Acknowledged []string `json:"acknowledged,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Access       string   `json:"access,omitempty"`
}

// Choices are the owner's non-sensitive selections.
type Choices struct {
	Provider string `json:"provider,omitempty"`
	Access   string `json:"access,omitempty"`
}

// StepStatus is one step as reported.
type StepStatus struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	State string `json:"state"`
	// Detail is one plain sentence about where the step stands.
	Detail string `json:"detail"`
	// Skipped is the owner's recorded choice, which may be shadowed by real
	// state (a skipped step that was then completed elsewhere reads done).
	Skipped      bool `json:"skipped"`
	Acknowledged bool `json:"acknowledged"`
}

// Status is the derived walkthrough status (GET /api/v1/setup).
type Status struct {
	Origin string `json:"origin"`
	Legacy bool   `json:"legacy"`
	// Complete: every step is done or skipped.
	Complete bool `json:"complete"`
	// Gated: the dashboard keeps this owner in the walkthrough. It is the
	// UI router guard's input only; no API route reads it (ADR-034).
	Gated bool `json:"gated"`
	// Current is the first step still pending, or empty when complete.
	Current     string         `json:"current"`
	Steps       []StepStatus   `json:"steps"`
	Choices     Choices        `json:"choices"`
	Facts       Facts          `json:"facts"`
	Providers   []Provider     `json:"providers"`
	Access      []AccessOption `json:"access"`
	StarterApps []StarterApp   `json:"starter_apps"`
}

// Update is a change to one step: a state, and/or choices.
type Update struct {
	// State is "skipped" (skip), "pending" (unskip, or take back an
	// acknowledgement) or "done" (acknowledge; only domain and apps).
	State    *string
	Provider *string
	Access   *string
}

// Service derives and stores the walkthrough state.
type Service struct {
	Wizards *wizard.Service
	Sources Sources
	mu      sync.Mutex
}

// New returns a Service.
func New(w *wizard.Service, src Sources) *Service {
	return &Service{Wizards: w, Sources: src}
}

func (s *Service) load() (stored, error) {
	state, err := s.Wizards.Get(WizardID)
	if errors.Is(err, wizard.ErrNotFound) {
		return stored{Origin: OriginFresh}, nil
	}
	if err != nil {
		return stored{}, err
	}
	raw, err := json.Marshal(state.Payload)
	if err != nil {
		return stored{}, err
	}
	var st stored
	if err := json.Unmarshal(raw, &st); err != nil {
		return stored{}, fmt.Errorf("setup: decode state: %w", err)
	}
	if st.Origin != OriginLegacy {
		st.Origin = OriginFresh
	}
	return st, nil
}

func (s *Service) facts() (Facts, []StarterApp, error) {
	var f Facts
	var starter []StarterApp
	var err error
	src := s.Sources
	if src.Recovery != nil {
		if f.RecoveryExported, f.RecoveryAcknowledged, err = src.Recovery(); err != nil {
			return f, nil, fmt.Errorf("setup: recovery identity: %w", err)
		}
	}
	if src.SignIn != nil {
		if f.SignIn, err = src.SignIn(); err != nil {
			return f, nil, fmt.Errorf("setup: sign-in: %w", err)
		}
	}
	if src.Domain != nil {
		if f.Domain, err = src.Domain(); err != nil {
			return f, nil, fmt.Errorf("setup: domain: %w", err)
		}
	}
	if src.Tunnels != nil {
		if f.Tunnels, err = src.Tunnels(); err != nil {
			return f, nil, fmt.Errorf("setup: tunnels: %w", err)
		}
	}
	if src.Exposures != nil {
		if f.Exposures, err = src.Exposures(); err != nil {
			return f, nil, fmt.Errorf("setup: exposure: %w", err)
		}
	}
	if src.Apps != nil {
		if starter, f.AppsInstalled, err = src.Apps(); err != nil {
			return f, nil, fmt.Errorf("setup: apps: %w", err)
		}
	}
	if starter == nil {
		starter = []StarterApp{}
	}
	f.ReviewExisting = f.Domain.Configured || f.Domain.Onboarded || f.Tunnels > 0 || f.Exposures > 0
	return f, starter, nil
}

// derive computes one step's state. Real state wins over a recorded skip:
// a step completed elsewhere reads done. Acknowledgement completes only the
// two steps that have no single observable end (domain and apps).
func derive(name string, st stored, f Facts) (state, detail string) {
	acked := slices.Contains(st.Acknowledged, name)
	skipped := slices.Contains(st.Skipped, name)
	done := false
	switch name {
	case StepAccount:
		done = f.RecoveryAcknowledged
	case StepSignIn:
		done = f.SignIn.Configured && f.SignIn.Linked && !f.SignIn.Dormant
	case StepDomain:
		done = acked || f.Domain.Onboarded || f.Tunnels > 0 || f.Exposures > 0
	case StepApps:
		done = acked || f.AppsInstalled > 0
	}
	switch {
	case done:
		state = Done
	case skipped:
		state = Skipped
	default:
		state = Pending
	}
	return state, detailFor(name, state, f)
}

func detailFor(name, state string, f Facts) string {
	switch name {
	case StepAccount:
		switch {
		case state == Done:
			return "The recovery identity is exported and you confirmed it is stored offline."
		case state == Skipped:
			return "Skipped. Backups can only be read with this appliance's own key until you export and store the recovery identity."
		case f.RecoveryExported:
			return "The recovery identity is exported. Confirm that you stored it offline."
		}
		return "Export the recovery identity, store it offline, and confirm. The local owner account stays the way back in."
	case StepSignIn:
		switch {
		case state == Done:
			return "Sign in with Authentik is set up and linked to your account."
		case state == Skipped:
			return "Skipped. You sign in with the local owner account."
		case f.SignIn.Configured && !f.SignIn.Linked:
			return "Authentik is set up. Link your Authentik account to finish."
		case f.SignIn.Installed:
			return "Authentik is installed. Set up sign-in, then link your account."
		}
		return "Install Authentik from the catalog, then link your Authentik account. This is optional."
	case StepDomain:
		switch {
		case state == Done && f.ReviewExisting:
			return "Your domain and access are set up."
		case state == Done:
			return "Home network only. Nothing is open to the internet."
		case state == Skipped:
			return "Skipped. The appliance is reachable on your home network only."
		case f.ReviewExisting:
			return "A domain or tunnel is already set up. Review it and confirm; nothing is changed."
		}
		return "Choose a DNS provider and how the appliance is reached. The default is your home network only."
	case StepApps:
		switch {
		case state == Done && f.AppsInstalled > 0:
			return fmt.Sprintf("%d app(s) installed.", f.AppsInstalled)
		case state == Done:
			return "Finished. Add apps any time from the catalog."
		case state == Skipped:
			return "Skipped. Add apps any time from the catalog."
		}
		return "Pick a few apps to start with. Installs run in the background and do not hold you here."
	}
	return ""
}

// Status derives the walkthrough status.
func (s *Service) Status() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status()
}

func (s *Service) status() (Status, error) {
	st, err := s.load()
	if err != nil {
		return Status{}, err
	}
	f, starter, err := s.facts()
	if err != nil {
		return Status{}, err
	}
	out := Status{
		Origin: st.Origin, Legacy: st.Origin == OriginLegacy, Complete: true,
		Facts: f, StarterApps: starter, Providers: Providers(),
		Choices: Choices{Provider: st.Provider, Access: st.Access},
		Access:  AccessOptions(st.Provider),
	}
	for _, name := range StepNames {
		state, detail := derive(name, st, f)
		out.Steps = append(out.Steps, StepStatus{
			Name: name, Title: titles[name], State: state, Detail: detail,
			Skipped:      slices.Contains(st.Skipped, name),
			Acknowledged: slices.Contains(st.Acknowledged, name),
		})
		if state == Pending {
			out.Complete = false
			if out.Current == "" {
				out.Current = name
			}
		}
	}
	out.Gated = !out.Legacy && !out.Complete
	return out, nil
}

// Update records an owner choice for a step and returns the new status.
func (s *Service) Update(step string, u Update) (Status, error) {
	if !slices.Contains(StepNames, step) {
		return Status{}, fmt.Errorf("%w: %q (steps: account, sign-in, domain, apps)", ErrUnknownStep, step)
	}
	if u.State == nil && u.Provider == nil && u.Access == nil {
		return Status{}, fmt.Errorf(`%w: want {"state"} and/or {"provider"}, {"access"}`, ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return Status{}, err
	}
	if u.Provider != nil || u.Access != nil {
		if step != StepDomain {
			return Status{}, fmt.Errorf("%w: a provider or access choice belongs to the domain step", ErrInvalid)
		}
	}
	if u.Provider != nil {
		if *u.Provider != "" {
			if _, ok := ProviderNamed(*u.Provider); !ok {
				return Status{}, fmt.Errorf("%w: unknown DNS provider %q (cloudflare, digitalocean, route53, manual)", ErrInvalid, *u.Provider)
			}
		}
		st.Provider = *u.Provider
		// A tunnel only fits Cloudflare: changing the provider drops a
		// tunnel choice it no longer supports.
		if st.Access == AccessTunnel && st.Provider != "cloudflare" {
			st.Access = ""
		}
	}
	if u.Access != nil {
		switch *u.Access {
		case "":
		case AccessHome, AccessRouter:
		case AccessTunnel:
			if st.Provider != "cloudflare" {
				return Status{}, fmt.Errorf("%w: Cloudflare Tunnel needs Cloudflare as the DNS provider", ErrInvalid)
			}
		default:
			return Status{}, fmt.Errorf("%w: unknown access mode %q (home, tunnel, router)", ErrInvalid, *u.Access)
		}
		st.Access = *u.Access
	}
	if u.State != nil {
		switch *u.State {
		case Skipped:
			st.Skipped = add(st.Skipped, step)
			st.Acknowledged = remove(st.Acknowledged, step)
		case Pending:
			st.Skipped = remove(st.Skipped, step)
			st.Acknowledged = remove(st.Acknowledged, step)
		case Done:
			if step != StepDomain && step != StepApps {
				return Status{}, fmt.Errorf("%w: the %s step completes from real state, not by confirming it", ErrInvalid, step)
			}
			st.Acknowledged = add(st.Acknowledged, step)
			st.Skipped = remove(st.Skipped, step)
		default:
			return Status{}, fmt.Errorf(`%w: state must be "skipped", "pending" or "done"`, ErrInvalid)
		}
	}
	if err := s.save(st); err != nil {
		return Status{}, err
	}
	return s.status()
}

// save writes the stored struct. The wizard step records where the owner
// is, so a reload resumes there.
func (s *Service) save(st stored) error {
	step := StepAccount
	if f, _, err := s.facts(); err == nil {
		step = "complete"
		for _, name := range StepNames {
			if state, _ := derive(name, st, f); state == Pending {
				step = name
				break
			}
		}
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	_, err = s.Wizards.Put(WizardID, step, payload)
	return err
}

func add(list []string, name string) []string {
	if slices.Contains(list, name) {
		return list
	}
	list = append(list, name)
	slices.Sort(list)
	return list
}

func remove(list []string, name string) []string {
	out := list[:0:0]
	for _, v := range list {
		if v != name {
			out = append(out, v)
		}
	}
	return out
}
