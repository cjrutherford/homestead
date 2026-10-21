package setup

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
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
	storage         bool
	chosen          string
	policy          bool
	backupStorage   bool
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
		Storage:   func() (bool, error) { return f.storage, nil },
		Recovery:  func() (bool, bool, error) { return f.exported, f.acked, nil },
		SignIn:    func() (SSOFacts, error) { return f.sso, nil },
		Domain:    func(chosen string) (DomainFacts, error) { f.chosen = chosen; return f.domain, nil },
		Tunnels:   func() (int, error) { return f.tunnels, nil },
		Exposures: func() (int, error) { return f.exp, nil },
		Apps: func() ([]StarterApp, int, error) {
			return []StarterApp{{Name: "jellyfin", Title: "Jellyfin"}}, f.apps, nil
		},
		Backups: func() (bool, bool, error) { return f.policy, f.backupStorage, nil },
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
	if states(t, s)[StepAccount] != Pending {
		t.Fatalf("recovery acknowledged without password confirmation must stay pending: %+v", s.Steps[0])
	}
	if _, err := svc.Update(StepAccount, Update{State: str(Done)}); err != nil {
		t.Fatal(err)
	}
	s, _ = svc.Status()
	if states(t, s)[StepAccount] != Done || s.Current != StepStorage {
		t.Fatalf("acked: %+v", s)
	}
	f.storage = true
	s, _ = svc.Status()
	if states(t, s)[StepStorage] != Done || s.Current != StepSignIn {
		t.Fatalf("storage assigned: %+v", s)
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
	if states(t, s)[StepSignIn] != Done || !strings.Contains(s.Steps[2].Detail, "Authentik is not installed") {
		t.Fatalf("dormant link must not re-gate but must warn: %+v", s.Steps[2])
	}
	f.sso.Dormant = false
	f.domain = DomainFacts{Configured: true, Provider: "cloudflare"}
	s, _ = svc.Status()
	if states(t, s)[StepDomain] != Pending || s.Facts.ReviewExisting {
		t.Fatalf("saved but never onboarded domain: pending, no review, got %+v", s.Steps[3])
	}
	f.domain.Onboarded = true
	s, _ = svc.Status()
	if states(t, s)[StepDomain] != Done {
		t.Fatalf("onboarded should be done")
	}
	f.apps = 2
	s, _ = svc.Status()
	if s.Complete || s.Current != StepBackups || !strings.Contains(s.Steps[5].Detail, "Storage page") {
		t.Fatalf("backups without storage: %+v", s.Steps[5])
	}
	f.backupStorage = true
	if s, _ = svc.Status(); !strings.Contains(s.Steps[5].Detail, "approve it") {
		t.Fatalf("backups with storage: %+v", s.Steps[5])
	}
	f.policy = true
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
	if states(t, s)[StepApps] != Skipped || !s.Steps[4].Skipped {
		t.Fatalf("skip not recorded: %+v", s.Steps[4])
	}
	f.apps = 1 // installed from the CLI
	s, _ = svc.Status()
	if states(t, s)[StepApps] != Done {
		t.Fatalf("real state should win over skip: %+v", s.Steps[4])
	}
}

func TestSkipUnskipResumeAndGate(t *testing.T) {
	svc, f, st := newSvc(t)
	f.acked = true
	if _, err := svc.Update(StepAccount, Update{State: str(Done)}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{StepStorage, StepSignIn, StepDomain, StepApps, StepBackups} {
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
		t.Fatalf("domain = %+v", s.Steps[3])
	}
	// Acknowledging records only the acknowledgement.
	s, err := svc.Update(StepDomain, Update{State: str(Done)})
	if err != nil || !s.Steps[3].Acknowledged {
		t.Fatalf("ack: %v %+v", err, s.Steps[3])
	}
}

func TestAcknowledgeCompletesDomainWithoutChanges(t *testing.T) {
	svc, _, _ := newSvc(t)
	s, err := svc.Update(StepDomain, Update{State: str(Done)})
	if err != nil || states(t, s)[StepDomain] != Done {
		t.Fatalf("%v %+v", err, s.Steps[3])
	}
	if !strings.Contains(s.Steps[3].Detail, "Home network only") {
		t.Fatalf("detail = %q", s.Steps[3].Detail)
	}
	if _, err := svc.Update(StepSignIn, Update{State: str(Done)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sign-in cannot be confirmed by hand: %v", err)
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
	allowed := map[string]bool{"origin": true, "skipped": true, "acknowledged": true, "provider": true, "access": true, "version": true}
	for k := range state.Payload {
		if !allowed[k] {
			t.Fatalf("unexpected payload key %q", k)
		}
	}
}

func TestDomainConfirmRules(t *testing.T) {
	svc, f, _ := newSvc(t)
	// Tunnel: needs Cloudflare and a tunnel route.
	if _, err := svc.Update(StepDomain, Update{Provider: str("cloudflare"), Access: str(AccessTunnel)}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Update(StepDomain, Update{State: str(Done)})
	if !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "/tunnel") {
		t.Fatalf("tunnel without route: %v", err)
	}
	s, _ := svc.Status()
	if states(t, s)[StepDomain] != Pending || !strings.Contains(s.Steps[3].Detail, "Tunnel page") {
		t.Fatalf("detail should say what is missing: %+v", s.Steps[3])
	}
	f.tunnels = 1
	s, err = svc.Update(StepDomain, Update{State: str(Done)})
	if err != nil || states(t, s)[StepDomain] != Done || !strings.Contains(s.Steps[3].Detail, "Tunnel") || strings.Contains(s.Steps[3].Detail, "Home network only") {
		t.Fatalf("tunnel with route: %v %+v", err, s.Steps[3])
	}
	// Router: needs a provider and an approved exposure plan.
	f.tunnels = 0
	svc2, f2, _ := newSvc(t)
	if _, err := svc2.Update(StepDomain, Update{Access: str(AccessRouter)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc2.Update(StepDomain, Update{State: str(Done)}); !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("router without provider: %v", err)
	}
	if _, err := svc2.Update(StepDomain, Update{Provider: str("manual")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc2.Update(StepDomain, Update{State: str(Done)}); !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "/exposure") {
		t.Fatalf("router without plan: %v", err)
	}
	f2.exp = 1
	if s, err := svc2.Update(StepDomain, Update{State: str(Done)}); err != nil || !strings.Contains(s.Steps[3].Detail, "port forward") {
		t.Fatalf("router with plan: %v %+v", err, s.Steps[3])
	}
	// Home network only: acknowledgement is enough.
	svc3, _, _ := newSvc(t)
	if _, err := svc3.Update(StepDomain, Update{Access: str(AccessHome), State: str(Done)}); err != nil {
		t.Fatalf("home: %v", err)
	}
}

func TestChangingAccessAfterAckReopensDomain(t *testing.T) {
	svc, _, _ := newSvc(t)
	if _, err := svc.Update(StepDomain, Update{State: str(Done)}); err != nil {
		t.Fatal(err)
	}
	s, _ := svc.Update(StepDomain, Update{Provider: str("cloudflare"), Access: str(AccessTunnel)})
	if states(t, s)[StepDomain] != Pending {
		t.Fatalf("an earlier home acknowledgement must not satisfy a tunnel choice: %+v", s.Steps[3])
	}
}

func TestPasswordConfirmationNeededForAccount(t *testing.T) {
	svc, f, _ := newSvc(t)
	f.exported, f.acked = true, true
	s, _ := svc.Status()
	if states(t, s)[StepAccount] != Pending || s.Facts.PasswordConfirmed {
		t.Fatalf("%+v", s.Steps[0])
	}
	s, err := svc.Update(StepAccount, Update{State: str(Done)})
	if err != nil || states(t, s)[StepAccount] != Done || !s.Facts.PasswordConfirmed {
		t.Fatalf("%v %+v", err, s.Steps[0])
	}
	// Password confirmed alone, without the recovery acknowledgement, is not done.
	svc2, _, _ := newSvc(t)
	s, _ = svc2.Update(StepAccount, Update{State: str(Done)})
	if states(t, s)[StepAccount] != Pending {
		t.Fatalf("password alone: %+v", s.Steps[0])
	}
	// Taking it back clears the confirmation.
	s, _ = svc.Update(StepAccount, Update{State: str(Pending)})
	if states(t, s)[StepAccount] != Pending || s.Facts.PasswordConfirmed {
		t.Fatalf("%+v", s.Steps[0])
	}
}

func TestSavedUnonboardedDomainIsNotReview(t *testing.T) {
	svc, f, _ := newSvc(t)
	f.domain = DomainFacts{Configured: true, Provider: "cloudflare", Zone: "example.com"}
	if s, _ := svc.Status(); s.Facts.ReviewExisting {
		t.Fatal("a saved wizard payload alone must not open review mode")
	}
	f.domain.Onboarded = true
	if s, _ := svc.Status(); !s.Facts.ReviewExisting {
		t.Fatal("onboarded must open review mode")
	}
}

func TestDomainDetailReflectsAccess(t *testing.T) {
	svc, _, _ := newSvc(t)
	s, _ := svc.Update(StepDomain, Update{Provider: str("cloudflare"), Access: str(AccessTunnel)})
	if strings.Contains(s.Steps[3].Detail, "Home network only") {
		t.Fatalf("detail ignores the access choice: %q", s.Steps[3].Detail)
	}
}

// A reset must remove homestead.db. By construction a fresh store has no
// setup row, so a freshly claimed appliance starts the walkthrough as fresh.
func TestFreshStoreHasNoSetupRow(t *testing.T) {
	svc, _, st := newSvc(t)
	if _, err := wizard.New(st).Get(WizardID); !errors.Is(err, wizard.ErrNotFound) {
		t.Fatalf("fresh store has a setup row: %v", err)
	}
	s, _ := svc.Status()
	if s.Origin != OriginFresh || !s.Gated {
		t.Fatalf("%+v", s)
	}
}

func TestStorageStep(t *testing.T) {
	svc, f, _ := newSvc(t)
	s, _ := svc.Status()
	if s.Steps[1].Name != StepStorage || states(t, s)[StepStorage] != Pending {
		t.Fatalf("storage should be the second step and pending: %+v", s.Steps)
	}
	if _, err := svc.Update(StepStorage, Update{State: str(Done)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("storage cannot be confirmed by hand: %v", err)
	}
	s, err := svc.Update(StepStorage, Update{State: str(Skipped)})
	if err != nil || states(t, s)[StepStorage] != Skipped {
		t.Fatalf("skip: %v %+v", err, s.Steps[1])
	}
	f.storage = true
	s, _ = svc.Status()
	if states(t, s)[StepStorage] != Done || !s.Facts.StorageAppAssigned {
		t.Fatalf("real state wins over skip: %+v", s.Steps[1])
	}
	f.storage = false
	s, err = svc.Update(StepStorage, Update{State: str(Pending)})
	if err != nil || states(t, s)[StepStorage] != Pending {
		t.Fatalf("unskip: %v %+v", err, s.Steps[1])
	}
}

// An appliance that finished setup before the storage step existed (a
// payload without a version, every other step done or skipped) is not sent
// back into the wizard; one still mid-setup gets the new step.
func TestPreStorageApplianceIsNotRegated(t *testing.T) {
	old := func(t *testing.T, payload map[string]any) (*Service, *fakes) {
		svc, f, st := newSvc(t)
		if _, err := wizard.New(st).Put(WizardID, "complete", payload); err != nil {
			t.Fatal(err)
		}
		return svc, f
	}
	svc, f := old(t, map[string]any{"origin": "fresh", "acknowledged": []any{"account", "apps"}, "skipped": []any{"sign-in", "domain"}})
	f.acked = true
	s, _ := svc.Status()
	if !s.Complete || s.Gated || states(t, s)[StepStorage] != Skipped {
		t.Fatalf("finished appliance re-gated: %+v", s)
	}
	if !s.Steps[1].Waived || s.Steps[1].Skipped || !strings.Contains(s.Steps[1].Detail, "Not required") {
		t.Fatalf("storage should read as waived, not as an owner skip: %+v", s.Steps[1])
	}
	if !s.Steps[5].Waived || states(t, s)[StepBackups] != Skipped {
		t.Fatalf("backups should be waived too: %+v", s.Steps[5])
	}
	if v := persistedVersion(t, svc); v != storedVersion {
		t.Fatalf("persisted version = %d, want %d", v, storedVersion)
	}
	// The rule is persisted: it does not depend on the facts later on.
	again := New(svc.Wizards, svc.Sources)
	f.acked = false // other steps regress; storage stays waived
	s, _ = again.Status()
	if states(t, s)[StepStorage] != Skipped || !s.Steps[1].Waived {
		t.Fatalf("waiver not persisted: %+v", s.Steps[1])
	}
	f.acked = true
	// No unskip or skip attempt re-gates a waived step.
	for _, state := range []string{Pending, Skipped, Pending} {
		s, err := svc.Update(StepStorage, Update{State: str(state)})
		if err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if s.Gated || !s.Complete || states(t, s)[StepStorage] != Skipped || !s.Steps[1].Waived {
			t.Fatalf("waived step changed by %q: %+v", state, s)
		}
	}
	if _, err := svc.Update(StepStorage, Update{State: str(Done)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("done by hand: %v", err)
	}
	// Real assignment still reads done.
	f.storage = true
	if s, _ = svc.Status(); states(t, s)[StepStorage] != Done {
		t.Fatalf("assigned storage should read done: %+v", s.Steps[1])
	}

	// Mid-setup before the step existed: the new step applies.
	svc2, _ := old(t, map[string]any{"origin": "fresh", "skipped": []any{"sign-in"}})
	s, _ = svc2.Status()
	if s.Complete || states(t, s)[StepStorage] != Pending {
		t.Fatalf("mid-setup appliance should see storage: %+v", s)
	}
	// And completing it later does not waive storage retroactively.
	var err error
	for _, u := range []struct {
		step, state string
	}{{StepAccount, Done}, {StepDomain, Done}, {StepApps, Skipped}} {
		if s, err = svc2.Update(u.step, Update{State: str(u.state)}); err != nil {
			t.Fatal(u.step, err)
		}
	}
	if s.Complete || states(t, s)[StepStorage] != Pending {
		t.Fatalf("late completion must not waive storage: %+v", s)
	}
}

func persistedVersion(t *testing.T, svc *Service) int {
	t.Helper()
	state, err := svc.Wizards.Get(WizardID)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := state.Payload["version"].(float64)
	return int(v)
}

// A legacy appliance stays ungated, is stamped the current version, and the migration
// is applied exactly once: a later change to the facts does not redo it.
func TestLegacyOriginStaysUngatedAndIsStamped(t *testing.T) {
	svc, f, st := newSvc(t)
	if _, err := wizard.New(st).Put(WizardID, "complete", map[string]any{"origin": "legacy"}); err != nil {
		t.Fatal(err)
	}
	s, err := svc.Status()
	if err != nil || s.Gated || !s.Legacy {
		t.Fatalf("legacy gated: %v %+v", err, s)
	}
	if states(t, s)[StepStorage] != Pending || s.Steps[1].Waived {
		t.Fatalf("legacy appliances get no waiver: %+v", s.Steps[1])
	}
	if v := persistedVersion(t, svc); v != storedVersion {
		t.Fatalf("persisted version = %d, want %d", v, storedVersion)
	}
	f.acked = true
	f.exported = true
	if s, _ = svc.Status(); s.Gated || s.Steps[1].Waived {
		t.Fatalf("migration ran again: %+v", s)
	}
}

// A GET must not fail because the migration could not be saved; the next
// write persists it.
func TestStatusToleratesFailedMigrationSave(t *testing.T) {
	svc, f, st := newSvc(t)
	f.acked = true
	payload := map[string]any{"origin": "fresh", "acknowledged": []any{"account", "apps"}, "skipped": []any{"sign-in", "domain"}}
	if _, err := wizard.New(st).Put(WizardID, "complete", payload); err != nil {
		t.Fatal(err)
	}
	svc.putErr = errors.New("disk full")
	s, err := svc.Status()
	if err != nil {
		t.Fatalf("status failed on a failed save: %v", err)
	}
	if s.Gated || !s.Steps[1].Waived {
		t.Fatalf("waiver should hold in memory: %+v", s.Steps[1])
	}
	if v := persistedVersion(t, svc); v == storedVersion {
		t.Fatal("nothing should have been persisted")
	}
	svc.putErr = nil
	if _, err := svc.Update(StepApps, Update{State: str(Done)}); err != nil {
		t.Fatal(err)
	}
	if v := persistedVersion(t, svc); v != storedVersion {
		t.Fatalf("write did not persist: %d", v)
	}
}

// The dashboard's Cloudflare token copy is written by hand in the domain
// step; it must name exactly the permissions the provider list asks for.
func TestDashboardCloudflareCopyMatchesProviders(t *testing.T) {
	raw, err := os.ReadFile("../../dashboard/src/app/setup/steps/domain-step.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	cf, ok := ProviderNamed("cloudflare")
	if !ok {
		t.Fatal("no cloudflare provider")
	}
	want := map[string]bool{}
	for _, p := range append(append([]Permission{}, cf.Permissions...), cf.TunnelPermissions...) {
		// "Zone: DNS: Edit" is shown as "DNS: Edit" under Zone resources.
		_, name, _ := strings.Cut(p.Grant, ": ")
		want[name] = true
		if !strings.Contains(src, name) {
			t.Errorf("domain-step.ts does not mention %q", name)
		}
	}
	for _, m := range regexp.MustCompile(`[A-Z][A-Za-z ]*: (?:Read|Edit)`).FindAllString(src, -1) {
		if !want[m] {
			t.Errorf("domain-step.ts names permission %q that providers.go does not ask for", m)
		}
	}
}

func TestDomainSourceGetsChosenProvider(t *testing.T) {
	svc, f, _ := newSvc(t)
	if _, err := svc.Update(StepDomain, Update{Provider: str("cloudflare")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Status(); err != nil {
		t.Fatal(err)
	}
	if f.chosen != "cloudflare" {
		t.Fatalf("Domain source got %q, want the chosen provider", f.chosen)
	}
}

// The domain names public hostnames only (ADR-037): local-only suffixes
// and dotless names are refused; it is a domain-step choice.
func TestDomainChoice(t *testing.T) {
	svc, _, _ := newSvc(t)
	s, err := svc.Update(StepDomain, Update{Domain: str(" Example.COM. ")})
	if err != nil || s.Choices.Domain != "example.com" {
		t.Fatalf("domain = %q, %v", s.Choices.Domain, err)
	}
	for _, bad := range []string{"homestead", "homestead.lan", "homestead.local", "nas.home.arpa", "x.internal",
		"localhost", "app.localhost", "bad_name.com", "-a.com", "a..com"} {
		if _, err := svc.Update(StepDomain, Update{Domain: str(bad)}); !errors.Is(err, ErrRefused) {
			t.Fatalf("%q: %v, want refused", bad, err)
		}
	}
	if s, _ := svc.Status(); s.Choices.Domain != "example.com" {
		t.Fatalf("refused update changed the domain: %q", s.Choices.Domain)
	}
	if _, err := svc.Update(StepApps, Update{Domain: str("example.com")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("domain on wrong step: %v", err)
	}
	if s, err := svc.Update(StepDomain, Update{Domain: str("")}); err != nil || s.Choices.Domain != "" {
		t.Fatalf("clear = %q, %v", s.Choices.Domain, err)
	}
}

// An appliance that finished setup before the backups step existed (a
// version 2 payload) has it waived; one still mid-setup gets it. Approving
// a policy is the only way to complete it.
func TestPreBackupsApplianceIsNotRegated(t *testing.T) {
	old := func(t *testing.T, payload map[string]any) (*Service, *fakes) {
		svc, f, st := newSvc(t)
		if _, err := wizard.New(st).Put(WizardID, "complete", payload); err != nil {
			t.Fatal(err)
		}
		return svc, f
	}
	svc, f := old(t, map[string]any{"origin": "fresh", "version": 2, "acknowledged": []any{"account", "apps"}, "skipped": []any{"storage", "sign-in", "domain"}})
	f.acked = true
	s, _ := svc.Status()
	if !s.Complete || s.Gated || !s.Steps[5].Waived || s.Steps[1].Waived || !strings.Contains(s.Steps[5].Detail, "Not required") {
		t.Fatalf("finished appliance re-gated: %+v", s)
	}
	if _, err := svc.Update(StepBackups, Update{State: str(Done)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("done by hand: %v", err)
	}
	f.policy = true
	if s, _ = svc.Status(); states(t, s)[StepBackups] != Done {
		t.Fatalf("approved policy should read done: %+v", s.Steps[5])
	}

	svc2, _ := old(t, map[string]any{"origin": "fresh", "version": 2, "skipped": []any{"sign-in"}})
	s, _ = svc2.Status()
	if s.Complete || s.Steps[5].Waived || states(t, s)[StepBackups] != Pending {
		t.Fatalf("mid-setup appliance should see backups: %+v", s)
	}
	if s, _ = svc2.Update(StepBackups, Update{State: str(Skipped)}); states(t, s)[StepBackups] != Skipped || !strings.Contains(s.Steps[5].Detail, "Backups page") {
		t.Fatalf("skip: %+v", s.Steps[5])
	}
}
