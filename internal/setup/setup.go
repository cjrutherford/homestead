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
	"log"
	"slices"
	"strings"
	"sync"

	"homestead/internal/wizard"
)

// WizardID is the wizard-store id of the walkthrough.
const WizardID = "setup"

// Step names, in walkthrough order. They are the path segment of
// PUT /api/v1/setup/steps/{name} and the argument of `homesteadctl setup`.
const (
	StepAccount = "account"
	StepStorage = "storage"
	StepSignIn  = "sign-in"
	StepDomain  = "domain"
	StepApps    = "apps"
)

// StepNames lists the steps in order.
var StepNames = []string{StepAccount, StepStorage, StepSignIn, StepDomain, StepApps}

var titles = map[string]string{
	StepAccount: "Secure the owner account",
	StepStorage: "Storage",
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
	// ErrNotReady: the request is well formed but the appliance is not in
	// the state it needs (a tunnel or approved exposure plan is missing).
	// The API answers 422 with the message, which names the page to use.
	ErrNotReady = errors.New("setup step is not ready")
	// ErrRefused is a well-formed choice that cannot be used, such as a
	// local-only domain (422).
	ErrRefused = errors.New("setup choice refused")
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
	// store, for the provider chosen in setup, else the recorded one.
	// Names and presence only; values are never read here.
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
	RecoveryExported     bool `json:"recovery_exported"`
	RecoveryAcknowledged bool `json:"recovery_acknowledged"`
	// StorageAppAssigned: the application storage class is assigned to a
	// registered mount (S291). The storage step derives from it.
	StorageAppAssigned bool        `json:"storage_app_assigned"`
	SignIn             SSOFacts    `json:"sign_in"`
	Domain             DomainFacts `json:"domain"`
	Tunnels            int         `json:"tunnels"`
	Exposures          int         `json:"exposures"`
	// PasswordConfirmed: the owner confirmed their password in step 1. The
	// daemon has no temporary-password state, so this is an explicit
	// acknowledgement (see docs/DEFERRED.md).
	PasswordConfirmed bool `json:"password_confirmed"`
	// ReviewExisting: a domain was onboarded, or a tunnel route or approved
	// exposure plan exists, so step 3 opens as a review of it, never a redo.
	// A saved but never-onboarded domain wizard payload does not count.
	ReviewExisting bool `json:"review_existing"`
	AppsInstalled  int  `json:"apps_installed"`
}

// Sources reads the facts. Each is a seam: production wires the real
// stores, tests wire fakes.
type Sources struct {
	Recovery func() (exported, acknowledged bool, err error)
	// Storage reports whether the application storage class is assigned.
	Storage func() (bool, error)
	SignIn  func() (SSOFacts, error)
	// Domain reads the domain facts. chosen is the provider picked in the
	// setup step (may be empty); the sealed-credentials fact is about it
	// first, since that is the form the step offers (S292).
	Domain  func(chosen string) (DomainFacts, error)
	Tunnels func() (int, error)
	// Exposures counts approved exposure plans.
	Exposures func() (int, error)
	// Apps lists the starter apps and how many catalog apps (other than
	// Authentik) are installed.
	Apps func() (starter []StarterApp, installed int, err error)
}

// stored is the whole persisted payload. Keep it to choices and progress:
// a field added here is a field the wizard store holds in clear text.
type stored struct {
	// Version 2 (S291) added the storage step. A payload without it was
	// written before the step existed; see migrate.
	Version int      `json:"version,omitempty"`
	Origin  string   `json:"origin,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	// Waived lists steps that were never required of this appliance: setup
	// was finished before the step existed (see migrate). It is not an owner
	// skip, so it cannot be undone and never re-gates the appliance.
	Waived       []string `json:"waived,omitempty"`
	Acknowledged []string `json:"acknowledged,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Access       string   `json:"access,omitempty"`
	Domain       string   `json:"domain,omitempty"`
}

// storedVersion is the current payload version.
const storedVersion = 2

// Choices are the owner's non-sensitive selections.
type Choices struct {
	Provider string `json:"provider,omitempty"`
	Access   string `json:"access,omitempty"`
	// Domain names public hostnames only, for example example.com
	// (ADR-037): local names stay *.lan and homestead.local.
	Domain string `json:"domain,omitempty"`
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
	Skipped bool `json:"skipped"`
	// Waived: setup was finished before this step existed, so it is not
	// required. Real state still wins (an assigned class reads done).
	Waived       bool `json:"waived"`
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
	// acknowledgement) or "done" (acknowledge; for account it means "I confirm my password").
	State    *string
	Provider *string
	Access   *string
	Domain   *string
}

// Service derives and stores the walkthrough state.
type Service struct {
	Wizards *wizard.Service
	Sources Sources
	mu      sync.Mutex
	// putErr makes save fail; tests only.
	putErr error
}

// New returns a Service.
func New(w *wizard.Service, src Sources) *Service {
	return &Service{Wizards: w, Sources: src}
}

func (s *Service) load() (stored, error) {
	state, err := s.Wizards.Get(WizardID)
	if errors.Is(err, wizard.ErrNotFound) {
		return stored{Origin: OriginFresh, Version: storedVersion}, nil
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

// migrate runs once on a payload written before the storage step existed.
// An appliance whose other steps were all done or skipped at that point had
// finished setup, so the new step must not send it back into the wizard: the
// storage step is recorded as waived, which is not an owner skip and cannot
// be undone (real assignment still reads done). An appliance still mid-setup
// gets the new step like a fresh one. The result is persisted so the rule is
// applied exactly once. A GET must not fail because of it, so on the read
// path a failed save is logged and the migrated state is used in memory; the
// next write persists it.
func (s *Service) migrate(st stored) (stored, error) {
	if st.Version >= storedVersion {
		return st, nil
	}
	if st.Origin == OriginFresh {
		f, _, err := s.facts(st)
		if err != nil {
			return st, err
		}
		finished := true
		for _, name := range StepNames {
			if name == StepStorage {
				continue
			}
			if state, _ := derive(name, st, f); state == Pending {
				finished = false
			}
		}
		if finished {
			st.Waived = add(st.Waived, StepStorage)
		}
	}
	st.Version = storedVersion
	if err := s.save(st); err != nil {
		log.Printf("setup: could not persist the storage step migration (will retry on the next write): %v", err)
	}
	return st, nil
}

func (s *Service) facts(st stored) (Facts, []StarterApp, error) {
	var f Facts
	var starter []StarterApp
	var err error
	src := s.Sources
	if src.Recovery != nil {
		if f.RecoveryExported, f.RecoveryAcknowledged, err = src.Recovery(); err != nil {
			return f, nil, fmt.Errorf("setup: recovery identity: %w", err)
		}
	}
	if src.Storage != nil {
		if f.StorageAppAssigned, err = src.Storage(); err != nil {
			return f, nil, fmt.Errorf("setup: storage: %w", err)
		}
	}
	if src.SignIn != nil {
		if f.SignIn, err = src.SignIn(); err != nil {
			return f, nil, fmt.Errorf("setup: sign-in: %w", err)
		}
	}
	if src.Domain != nil {
		if f.Domain, err = src.Domain(st.Provider); err != nil {
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
	f.PasswordConfirmed = slices.Contains(st.Acknowledged, StepAccount)
	f.ReviewExisting = f.Domain.Onboarded || f.Tunnels > 0 || f.Exposures > 0
	return f, starter, nil
}

// derive computes one step's state. Real state wins over a recorded skip:
// a step completed elsewhere reads done. Acknowledgement completes only the
// two steps that have no single observable end (domain and apps).
func derive(name string, st stored, f Facts) (state, detail string) {
	acked := slices.Contains(st.Acknowledged, name)
	skipped := slices.Contains(st.Skipped, name) || slices.Contains(st.Waived, name)
	done := false
	switch name {
	case StepAccount:
		done = f.RecoveryAcknowledged && acked
	case StepStorage:
		done = f.StorageAppAssigned
	case StepSignIn:
		// A dormant link (Authentik removed) does not re-gate the appliance;
		// the detail warns instead.
		done = f.SignIn.Configured && f.SignIn.Linked
	case StepDomain:
		done = (acked && domainMissing(st, f) == "") || f.Tunnels > 0 || f.Exposures > 0 ||
			(f.Domain.Onboarded && (st.Access == "" || st.Access == AccessHome))
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
	return state, detailFor(name, state, st, f)
}

const dormantNote = "Warning: Authentik is not installed, so sign-in with Authentik is off. Your local password still works."

func detailFor(name, state string, st stored, f Facts) string {
	switch name {
	case StepAccount:
		switch {
		case state == Done:
			return "The recovery identity is exported and you confirmed it is stored offline."
		case state == Skipped:
			return "Skipped. Backups can only be read with this appliance's own key until you export and store the recovery identity."
		case f.RecoveryAcknowledged && !f.PasswordConfirmed:
			return "The recovery identity is stored. Confirm that you have set a password only you know."
		case f.PasswordConfirmed && f.RecoveryExported:
			return "Password confirmed. Confirm that you stored the exported recovery identity offline."
		case f.PasswordConfirmed:
			return "Password confirmed. Export the recovery identity, store it offline, and confirm."
		case f.RecoveryExported:
			return "The recovery identity is exported. Confirm that you stored it offline, and confirm your password."
		}
		return "Export the recovery identity, store it offline, and confirm. Also confirm your password. The local owner account stays the way back in."
	case StepStorage:
		switch {
		case state == Done:
			return "The application storage class is assigned to a registered disk."
		case state == Skipped && slices.Contains(st.Waived, name) && !slices.Contains(st.Skipped, name):
			return "Not required: setup was finished before this step existed. Assign storage on the Storage page when you need it."
		case state == Skipped:
			return "Skipped. Installing apps needs the application storage class; assign it on the Storage page."
		}
		return "Register a mounted disk and assign the application storage class. Homestead never formats or partitions a disk."
	case StepSignIn:
		switch {
		case state == Done && f.SignIn.Dormant:
			return "Sign in with Authentik was linked to your account. " + dormantNote
		case state == Done:
			return "Sign in with Authentik is set up and linked to your account."
		case state == Skipped:
			return "Skipped. You sign in with the local owner account."
		case f.SignIn.Dormant:
			return dormantNote
		case f.SignIn.Configured && !f.SignIn.Linked:
			return "Authentik is set up. Link your Authentik account to finish."
		case f.SignIn.Installed:
			return "Authentik is installed. Set up sign-in, then link your account."
		}
		return "Install Authentik from the catalog, then link your Authentik account. This is optional."
	case StepDomain:
		switch {
		case state == Skipped:
			return "Skipped. The appliance is reachable on your home network only."
		case state == Done && f.Tunnels > 0:
			return "A Cloudflare Tunnel is set up. Nothing was changed by this step."
		case state == Done && f.Exposures > 0:
			return "A router port forward plan is approved. Nothing was changed by this step."
		case state == Done && f.ReviewExisting:
			return "Your domain is set up and the appliance is reachable on your home network only."
		case state == Done:
			return "Home network only. Nothing is open to the internet."
		}
		if miss := domainMissing(st, f); miss != "" {
			return "Not finished yet: " + miss
		}
		if f.ReviewExisting {
			return "A domain, tunnel or exposure plan is already set up. Review it and confirm; nothing is changed."
		}
		return "Choose a DNS provider and how the appliance is reached. The default is your home network only."
	case StepApps:
		switch {
		case state == Done && f.AppsInstalled > 0:
			if f.AppsInstalled == 1 {
				return "1 app installed."
			}
			return fmt.Sprintf("%d apps installed.", f.AppsInstalled)
		case state == Done:
			return "Finished. Add apps any time from the catalog."
		case state == Skipped:
			return "Skipped. Add apps any time from the catalog."
		}
		return "Pick a few apps to start with. Installs run in the background and do not hold you here."
	}
	return ""
}

// effectiveProvider is the DNS provider the walkthrough treats as stored:
// the owner's choice here, else the one the domain wizard or onboarding saved.
func effectiveProvider(st stored, f Facts) string {
	if st.Provider != "" {
		return st.Provider
	}
	return f.Domain.Provider
}

// domainMissing says, in plain words, what a tunnel or router port forward
// still needs before the domain step may be confirmed. Empty means ready:
// always for home network only, which needs no real setup.
func domainMissing(st stored, f Facts) string {
	switch st.Access {
	case AccessTunnel:
		if effectiveProvider(st, f) != "cloudflare" {
			return "choose Cloudflare as the DNS provider on this page first."
		}
		if f.Tunnels == 0 {
			return "no tunnel exists yet. Create and approve it on the Tunnel page (/tunnel), then come back."
		}
	case AccessRouter:
		if effectiveProvider(st, f) == "" {
			return "choose a DNS provider on this page first."
		}
		if f.Exposures == 0 {
			return "no exposure plan is approved yet. Review and approve one on the Exposure page (/exposure), then come back."
		}
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
	if st, err = s.migrate(st); err != nil {
		return Status{}, err
	}
	f, starter, err := s.facts(st)
	if err != nil {
		return Status{}, err
	}
	out := Status{
		Origin: st.Origin, Legacy: st.Origin == OriginLegacy, Complete: true,
		Facts: f, StarterApps: starter, Providers: Providers(),
		Choices: Choices{Provider: st.Provider, Access: st.Access, Domain: st.Domain},
		Access:  AccessOptions(st.Provider),
	}
	for _, name := range StepNames {
		state, detail := derive(name, st, f)
		out.Steps = append(out.Steps, StepStatus{
			Name: name, Title: titles[name], State: state, Detail: detail,
			Skipped:      slices.Contains(st.Skipped, name),
			Waived:       slices.Contains(st.Waived, name),
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
		return Status{}, fmt.Errorf("%w: %q (steps: account, storage, sign-in, domain, apps)", ErrUnknownStep, step)
	}
	if u.State == nil && u.Provider == nil && u.Access == nil && u.Domain == nil {
		return Status{}, fmt.Errorf(`%w: want {"state"} and/or {"provider"}, {"access"}, {"domain"}`, ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return Status{}, err
	}
	if st, err = s.migrate(st); err != nil {
		return Status{}, err
	}
	if u.Provider != nil || u.Access != nil || u.Domain != nil {
		if step != StepDomain {
			return Status{}, fmt.Errorf("%w: a provider, access or domain choice belongs to the domain step", ErrInvalid)
		}
	}
	if u.Domain != nil {
		name, err := NormalizeDomain(*u.Domain)
		if err != nil {
			return Status{}, fmt.Errorf("%w: %v", ErrRefused, err)
		}
		st.Domain = name
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
	// A waived step was never skipped by the owner: skip and unskip leave it
	// as it is, so it can never re-gate a finished appliance.
	waived := slices.Contains(st.Waived, step) && u.State != nil && (*u.State == Skipped || *u.State == Pending)
	if u.State != nil && !waived {
		switch *u.State {
		case Skipped:
			st.Skipped = add(st.Skipped, step)
			st.Acknowledged = remove(st.Acknowledged, step)
		case Pending:
			st.Skipped = remove(st.Skipped, step)
			st.Acknowledged = remove(st.Acknowledged, step)
		case Done:
			if step == StepSignIn || step == StepStorage {
				return Status{}, fmt.Errorf("%w: the %s step completes from real state, not by confirming it", ErrInvalid, step)
			}
			if step == StepDomain {
				f, _, err := s.facts(st)
				if err != nil {
					return Status{}, err
				}
				if miss := domainMissing(st, f); miss != "" {
					return Status{}, fmt.Errorf("%w: %s", ErrNotReady, miss)
				}
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
	if s.putErr != nil {
		return s.putErr
	}
	step := StepAccount
	if f, _, err := s.facts(st); err == nil {
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

// localSuffixes never resolve on the internet, so they cannot be the
// public domain (ADR-037).
var localSuffixes = []string{"lan", "local", "home.arpa", "internal", "localhost"}

// NormalizeDomain checks a public domain such as example.com, lowercased
// and without a trailing dot. Empty clears the choice.
func NormalizeDomain(name string) (string, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" {
		return "", nil
	}
	if len(name) > 253 || !strings.Contains(name, ".") {
		return "", fmt.Errorf("the domain must be a public name with a dot, such as example.com")
	}
	for _, suffix := range localSuffixes {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			return "", fmt.Errorf("%s is a local-only name; the domain is for public names such as example.com (local names stay as they are)", name)
		}
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid domain %q", name)
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return "", fmt.Errorf("invalid domain %q", name)
			}
		}
	}
	return name, nil
}
