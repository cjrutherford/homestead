package setup

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/store"
	"homestead/internal/wizard"
)

type fakes struct {
	exported, acked bool
	sso             SSOFacts
	domain          DomainFacts
	tunnels, exp    int
	apps            int
}

func newSvc(t *testing.T) (*Service, *fakes, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fakes{}
	svc := New(wizard.New(st), Sources{
		Recovery:  func() (bool, bool, error) { return f.exported, f.acked, nil },
		SignIn:    func() (SSOFacts, error) { return f.sso, nil },
		Domain:    func() (DomainFacts, error) { return f.domain, nil },
		Tunnels:   func() (int, error) { return f.tunnels, nil },
		Exposures: func() (int, error) { return f.exp, nil },
		Apps: func() ([]StarterApp, int, error) {
			return []StarterApp{{Name: "jellyfin", Title: "Jellyfin"}}, f.apps, nil
		},
	})
	return svc, f, st
}

func states(t *testing.T, s Status) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, st := range s.Steps {
		m[st.Name] = st.State
	}
	return m
}

func str(s string) *string { return &s }

func TestFreshIsGatedAtAccount(t *testing.T) {
	svc, _, _ := newSvc(t)
	s, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.Origin != OriginFresh || s.Legacy || !s.Gated || s.Complete || s.Current != StepAccount {
		t.Fatalf("status = %+v", s)
	}
	for n, v := range states(t, s) {
		if v != Pending {
			t.Fatalf("%s = %s", n, v)
		}
	}
	if len(s.Providers) != 4 || len(s.Access) != 3 {
		t.Fatalf("providers %d access %d", len(s.Providers), len(s.Access))
	}
}

func TestDerivationFromRealState(t *testing.T) {
	svc, f, _ := newSvc(t)
	f.exported = true
	s, _ := svc.Status()
	if states(t, s)[StepAccount] != Pending || !strings.Contains(s.Steps[0].Detail, "Confirm") {
		t.Fatalf("exported only: %+v", s.Steps[0])
	}
	f.acked = true
	s, _ = svc.Status()
	if states(t, s)[StepAccount] != Done || s.Current != StepSignIn {
		t.Fatalf("acked: %+v", s)
	}
	f.sso = SSOFacts{Installed: true, Configured: true}
	s, _ = svc.Status()
	if states(t, s)[StepSignIn] != Pending {
		t.Fatalf("configured, unlinked should be pending")
	}
	f.sso.Linked = true
	s, _ = svc.Status()
	if states(t, s)[StepSignIn] != Done {
		t.Fatalf("linked should be done")
	}
	f.sso.Dormant = true
	s, _ = svc.Status()
	if states(t, s)[StepSignIn] != Pending {
		t.Fatalf("dormant should not be done")
	}
	f.sso.Dormant = false
	f.domain = DomainFacts{Configured: true, Provider: "cloudflare"}
	s, _ = svc.Status()
	if states(t, s)[StepDomain] != Pending || !s.Facts.ReviewExisting {
		t.Fatalf("configured domain: pending + review, got %+v", s.Steps[2])
	}
	f.domain.Onboarded = true
	s, _ = svc.Status()
	if states(t, s)[StepDomain] != Done {
		t.Fatalf("onboarded should be done")
	}
	f.apps = 2
	s, _ = svc.Status()
	if !s.Complete || s.Gated || s.Current != "" {
		t.Fatalf("all done: %+v", s)
	}
}

func TestOutOfBandCompletionBeatsSkip(t *testing.T) {
	svc, f, _ := newSvc(t)
	if _, err := svc.Update(StepApps, Update{State: str(Skipped)}); err != nil {
		t.Fatal(err)
	}
	s, _ := svc.Status()
	if states(t, s)[StepApps] != Skipped || !s.Steps[3].Skipped {
		t.Fatalf("skip not recorded: %+v", s.Steps[3])
	}
	f.apps = 1 // installed from the CLI
	s, _ = svc.Status()
	if states(t, s)[StepApps] != Done {
		t.Fatalf("real state should win over skip: %+v", s.Steps[3])
	}
}

func TestSkipUnskipResumeAndGate(t *testing.T) {
	svc, f, st := newSvc(t)
	f.acked = true
	for _, n := range []string{StepSignIn, StepDomain, StepApps} {
		if _, err := svc.Update(n, Update{State: str(Skipped)}); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := svc.Status()
	if !s.Complete || s.Gated {
		t.Fatalf("skipping everything completes: %+v", s)
	}
	if _, err := svc.Update(StepSignIn, Update{State: str(Pending)}); err != nil {
		t.Fatal(err)
	}
	s, _ = svc.Status()
	if s.Complete || !s.Gated || s.Current != StepSignIn {
		t.Fatalf("unskip re-gates: %+v", s)
	}
	// A new service over the same store resumes.
	again := New(wizard.New(st), svc.Sources)
	s2, _ := again.Status()
	if s2.Current != StepSignIn || states(t, s2)[StepDomain] != Skipped {
		t.Fatalf("resume lost state: %+v", s2)
	}
}

func TestReviewPathForExistingTunnel(t *testing.T) {
	svc, f, _ := newSvc(t)
	f.domain = DomainFacts{Configured: true, Provider: "cloudflare", Zone: "example.com"}
	f.tunnels = 1
	s, _ := svc.Status()
	if !s.Facts.ReviewExisting {
		t.Fatal("expected review mode")
	}
	// A tunnel already exists, so the step reads done; nothing was redone.
	if states(t, s)[StepDomain] != Done {
		t.Fatalf("domain = %+v", s.Steps[2])
	}
	// Acknowledging records only the acknowledgement.
	s, err := svc.Update(StepDomain, Update{State: str(Done)})
	if err != nil || !s.Steps[2].Acknowledged {
		t.Fatalf("ack: %v %+v", err, s.Steps[2])
	}
}

func TestAcknowledgeCompletesDomainWithoutChanges(t *testing.T) {
	svc, _, _ := newSvc(t)
	s, err := svc.Update(StepDomain, Update{State: str(Done)})
	if err != nil || states(t, s)[StepDomain] != Done {
		t.Fatalf("%v %+v", err, s.Steps[2])
	}
	if !strings.Contains(s.Steps[2].Detail, "Home network only") {
		t.Fatalf("detail = %q", s.Steps[2].Detail)
	}
	if _, err := svc.Update(StepAccount, Update{State: str(Done)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("account cannot be confirmed by hand: %v", err)
	}
}

func TestChoicesAndTunnelRule(t *testing.T) {
	svc, _, _ := newSvc(t)
	if _, err := svc.Update(StepDomain, Update{Access: str(AccessTunnel)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tunnel without cloudflare: %v", err)
	}
	if _, err := svc.Update(StepDomain, Update{Provider: str("cloudflare"), Access: str(AccessTunnel)}); err != nil {
		t.Fatal(err)
	}
	s, _ := svc.Update(StepDomain, Update{Provider: str("route53")})
	if s.Choices.Access != "" || s.Choices.Provider != "route53" {
		t.Fatalf("tunnel choice should drop with provider: %+v", s.Choices)
	}
	for _, o := range s.Access {
		if o.ID == AccessTunnel && o.Available {
			t.Fatal("tunnel offered for route53")
		}
	}
	if _, err := svc.Update(StepDomain, Update{Provider: str("godaddy")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown provider: %v", err)
	}
	if _, err := svc.Update(StepApps, Update{Provider: str("manual")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("provider on wrong step: %v", err)
	}
	if _, err := svc.Update("nope", Update{State: str(Skipped)}); !errors.Is(err, ErrUnknownStep) {
		t.Fatalf("unknown step: %v", err)
	}
	if _, err := svc.Update(StepApps, Update{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty update: %v", err)
	}
}

func TestLegacyOriginIsNeverGated(t *testing.T) {
	svc, _, st := newSvc(t)
	if _, err := wizard.New(st).Put(WizardID, StepAccount, map[string]any{"origin": "legacy"}); err != nil {
		t.Fatal(err)
	}
	s, _ := svc.Status()
	if !s.Legacy || s.Origin != OriginLegacy || s.Gated || s.Complete {
		t.Fatalf("legacy = %+v", s)
	}
	// Updates keep the stamp.
	s, _ = svc.Update(StepApps, Update{State: str(Skipped)})
	if !s.Legacy {
		t.Fatal("update dropped the legacy stamp")
	}
}

func TestStoredPayloadHoldsNoSecrets(t *testing.T) {
	svc, _, st := newSvc(t)
	if _, err := svc.Update(StepDomain, Update{Provider: str("cloudflare"), Access: str(AccessTunnel)}); err != nil {
		t.Fatal(err)
	}
	state, err := wizard.New(st).Get(WizardID)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"origin": true, "skipped": true, "acknowledged": true, "provider": true, "access": true}
	for k := range state.Payload {
		if !allowed[k] {
			t.Fatalf("unexpected payload key %q", k)
		}
	}
}
