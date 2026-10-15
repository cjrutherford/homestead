package cert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"homestead/internal/docker"
	"homestead/internal/store"
)

const (
	prodDirectory = "https://acme-v02.api.letsencrypt.org/directory"
	testDirectory = "https://pebble.test:14000/dir"
)

type auditEntry struct{ actor, action, outcome, detail string }

// renewRig is an issuanceRig with a first issuance in place, an audit
// sink, and helpers to move the injected clock into the renewal window.
type renewRig struct {
	*issuanceRig
	audits []auditEntry
}

func newRenewRig(t *testing.T, directory string, confirmProduction bool) *renewRig {
	t.Helper()
	return newRenewRigNames(t, directory, confirmProduction, []string{"www.example.com"})
}

func (r *issuanceRig) putStrategy(set Set) {
	r.t.Helper()
	raw, _ := json.Marshal(map[string]any{"certificates": set.Certificates})
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if _, err := r.wizards.Put(TLSWizardID, "strategy", payload); err != nil {
		r.t.Fatalf("save strategy: %v", err)
	}
}

func newRenewRigNames(t *testing.T, directory string, confirmProduction bool, names []string) *renewRig {
	t.Helper()
	rig := &renewRig{issuanceRig: newIssuanceRig(t, directory)}
	rig.saveStrategy(directory, names)
	rig.issuing.Audit = func(actor, action, outcome, detail string) error {
		rig.audits = append(rig.audits, auditEntry{actor, action, outcome, detail})
		return nil
	}
	plan, err := rig.issuing.Plan("www")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.issuing.Approve("www", plan.Digest, "owner", true, confirmProduction); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	def, err := rig.issuing.Definition("www")
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.run(def); err != nil {
		t.Fatalf("first issuance: %v", err)
	}
	rig.audits = nil
	return rig
}

// enterWindow moves the clock to 20 days before the issued certificate
// expires (the window is 30 days).
func (r *renewRig) enterWindow() { r.clock = r.clock.Add(40 * 24 * time.Hour) }

func (r *renewRig) state() RenewalState {
	r.t.Helper()
	s, err := NewRenewalStates(r.store).Get("www")
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

func (r *renewRig) pending() []string {
	r.t.Helper()
	p, err := r.issuing.PendingRenewals(r.clock)
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *renewRig) status() RenewalStatus {
	r.t.Helper()
	report, err := r.issuing.RenewalReport(r.clock)
	if err != nil || len(report.Certificates) != 1 {
		r.t.Fatalf("report = %+v err %v", report, err)
	}
	return report.Certificates[0]
}

func (r *renewRig) recordedChain() []byte {
	r.t.Helper()
	list, err := NewIssuances(r.store).List()
	if err != nil || len(list) != 1 {
		r.t.Fatalf("issuances = %v err %v", list, err)
	}
	return list[0].ChainPEM
}

func TestRenewalBackoffSequenceAndCap(t *testing.T) {
	want := []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour,
		24 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for i, w := range want {
		if got := RenewalBackoff(i + 1); got != w {
			t.Errorf("RenewalBackoff(%d) = %s, want %s", i+1, got, w)
		}
	}
	if RenewalBackoff(0) != 0 || RenewalBackoff(1000) != 24*time.Hour {
		t.Errorf("edges: %s %s", RenewalBackoff(0), RenewalBackoff(1000))
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Duration{
		"":                              0,
		"120":                           2 * time.Minute,
		"-5":                            0,
		"soon":                          0,
		"Wed, 14 Oct 2026 14:00:00 GMT": 2 * time.Hour,
		"Wed, 14 Oct 2026 10:00:00 GMT": 0,
		" 30 ":                          30 * time.Second,
	} {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestRenewSuccessPlacesAndForcesReload(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	oldChain := rig.recordedChain()
	// Outside the window nothing is due.
	rig.clock = rig.clock.Add(10 * 24 * time.Hour)
	if got := rig.pending(); len(got) != 0 {
		t.Fatalf("pending outside the window = %v", got)
	}
	rig.enterWindow()
	if st := rig.status(); st.Status != RenewalDue {
		t.Fatalf("status = %+v", st)
	}
	if got := rig.pending(); len(got) != 1 || got[0] != "www" {
		t.Fatalf("pending = %v", got)
	}
	orders, reloads := rig.double.orders, rig.reloads
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if rig.double.orders != orders+1 {
		t.Fatalf("orders = %d, want one new order", rig.double.orders-orders)
	}
	// The renewed certificate is the recorded and the placed one, with a
	// fresh validity, and the edge reload ran even though the route
	// snippet itself did not change.
	newChain := rig.recordedChain()
	if string(newChain) == string(oldChain) {
		t.Fatal("recorded chain is still the old certificate")
	}
	placed, ok := docker.ReadPlacedChain(rig.stackDir, "www", []string{"www.example.com"})
	if !ok || string(placed) != string(newChain) {
		t.Fatalf("placed chain is not the renewed one (ok=%v)", ok)
	}
	if rig.reloads != reloads+1 {
		t.Fatalf("reloads = %d, want %d", rig.reloads, reloads+1)
	}
	if st := rig.status(); st.Status != RenewalOK {
		t.Fatalf("status after renewal = %+v", st)
	}
	if got := rig.pending(); len(got) != 0 {
		t.Fatalf("pending after renewal = %v", got)
	}
	// Audited, without key material.
	if len(rig.audits) != 1 || rig.audits[0].action != "cert.renewal.attempt" || rig.audits[0].outcome != "ok" ||
		rig.audits[0].actor != "scheduler" {
		t.Fatalf("audits = %+v", rig.audits)
	}
	for _, a := range rig.audits {
		if strings.Contains(a.detail, "PRIVATE KEY") || strings.Contains(a.detail, "BEGIN") {
			t.Fatalf("audit carries key material: %s", a.detail)
		}
	}
	if s := rig.state(); s.Attempts != 0 || s.LastSuccessAt.IsZero() || !s.NextAttemptAt.IsZero() {
		t.Fatalf("state = %+v", s)
	}
}

func TestRenewRefusesChangedIssuance(t *testing.T) {
	cases := []struct {
		name    string
		initial []string
		mutate  func(r *renewRig)
	}{
		{"subset of names", []string{"www.example.com", "api.example.com"},
			func(r *renewRig) { r.saveStrategy(issuanceDirectory, []string{"www.example.com"}) }},
		{"superset of names", nil, func(r *renewRig) {
			r.saveStrategy(issuanceDirectory, []string{"www.example.com", "api.example.com"})
		}},
		{"wildcard versus apex", nil, func(r *renewRig) { r.saveStrategy(issuanceDirectory, []string{"*.example.com"}) }},
		{"provider change", nil, func(r *renewRig) {
			r.provName = "other"
			r.saveStrategy(issuanceDirectory, []string{"www.example.com"})
		}},
		{"directory change", nil, func(r *renewRig) { r.saveStrategy(prodDirectory, []string{"www.example.com"}) }},
		{"zone change", nil, func(r *renewRig) { r.saveDomain("script", "other.test") }},
		{"challenge change", nil, func(r *renewRig) {
			set := Set{Certificates: []Certificate{{
				Name: "www", DNSNames: []string{"www.example.com"}, State: StateIssued, Renewal: Renewal{Auto: true},
				Issuer: Issuer{Name: "le", Type: IssuerACME, Directory: issuanceDirectory, Challenge: ChallengeHTTP01},
			}}}
			r.putStrategy(set)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initial := tc.initial
			if initial == nil {
				initial = []string{"www.example.com"}
			}
			rig := newRenewRigNames(t, issuanceDirectory, false, initial)
			rig.enterWindow()
			tc.mutate(rig)
			if st := rig.status(); st.Status != RenewalPlanRequired {
				t.Fatalf("status = %s (%s), want plan-required", st.Status, st.Detail)
			}
			if got := rig.pending(); len(got) != 0 {
				t.Fatalf("pending = %v", got)
			}
			calls := rig.caCalls.Load()
			err := rig.issuing.Renew(context.Background(), "www", "scheduler")
			if err == nil {
				t.Fatal("Renew issued a changed issuance")
			}
			if rig.caCalls.Load() != calls || rig.reloads != 1 {
				t.Fatalf("CA calls %d -> %d, reloads %d: a refused renewal must touch nothing", calls, rig.caCalls.Load(), rig.reloads)
			}
		})
	}
}

func TestRenewGateDirectoryAndOrder(t *testing.T) {
	rig := newRenewRigNames(t, issuanceDirectory, false, []string{"www.example.com", "api.example.com"})
	rig.enterWindow()
	// Reordering names is not a change (spelling is covered by the S263
	// fingerprint tests).
	rig.saveStrategy(issuanceDirectory, []string{"api.example.com", "www.example.com"})
	if got := rig.pending(); len(got) != 1 {
		t.Fatalf("spelling-only edit must stay unchanged, pending = %v (%+v)", got, rig.status())
	}
	// A directory change is refused with its reason, even to a "better" CA.
	rig.saveStrategy(testDirectory, []string{"www.example.com", "api.example.com"})
	err := rig.issuing.Renew(context.Background(), "www", "scheduler")
	if !errors.Is(err, ErrPlanRequired) || !strings.Contains(err.Error(), "never reaches a different CA") {
		t.Fatalf("Renew = %v", err)
	}
}

func TestRenewProductionNeedsConfirmedApproval(t *testing.T) {
	rig := newRenewRig(t, prodDirectory, true)
	rig.enterWindow()
	if got := rig.pending(); len(got) != 1 {
		t.Fatalf("confirmed production approval must renew, pending = %v", got)
	}
	// Tamper with the approval so production confirmation is missing (the
	// approve path can never record that, so this proves the runner
	// re-checks it rather than trusting the row).
	if _, err := rig.store.DB().Exec(`UPDATE cert_issuance_approvals SET confirmed_production = 0`); err != nil {
		t.Fatal(err)
	}
	if st := rig.status(); st.Status != RenewalPlanRequired || !strings.Contains(st.Detail, "production") {
		t.Fatalf("status = %+v", st)
	}
	calls := rig.caCalls.Load()
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("Renew = %v", err)
	}
	if rig.caCalls.Load() != calls {
		t.Fatal("production CA contacted without confirmation")
	}
	// Restored confirmation renews.
	if _, err := rig.store.DB().Exec(`UPDATE cert_issuance_approvals SET confirmed_production = 1`); err != nil {
		t.Fatal(err)
	}
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err != nil {
		t.Fatalf("Renew = %v", err)
	}
}

func TestRenewalModeComesFromTheApprovalNotTheStrategyFlag(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	rig.enterWindow()
	// The strategy omits (or clears) its auto flag: the approval's default
	// mode, auto, still lets Homestead re-issue; a stored certificate must
	// never silently stop renewing because a flag decodes false.
	rig.putStrategy(Set{Certificates: []Certificate{{
		Name: "www", DNSNames: []string{"www.example.com"}, State: StateIssued,
		Issuer: Issuer{Name: "le", Type: IssuerACME, Directory: issuanceDirectory,
			Challenge: ChallengeDNS01, DNSProvider: "script"},
	}}})
	if st := rig.status(); st.RenewalMode != RenewalAuto || !strings.Contains(st.Detail, "re-issues it unattended") {
		t.Fatalf("status = %+v", st)
	}
	if got := rig.pending(); len(got) != 1 {
		t.Fatalf("auto approval with Auto omitted in the strategy: pending = %v", got)
	}
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err != nil {
		t.Fatalf("Renew: %v", err)
	}
}

func TestManualRenewalApprovalWarnsOnly(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	plan, _ := rig.issuing.Plan("www")
	approval, err := rig.issuing.ApproveWith("www", plan.Digest, "owner", true, false, RenewalManual)
	if err != nil || approval.RenewalMode != RenewalManual {
		t.Fatalf("approval = %+v err %v", approval, err)
	}
	if _, err := rig.issuing.ApproveWith("www", plan.Digest, "owner", true, false, "sometimes"); err == nil {
		t.Fatal("accepted an unknown renewal mode")
	}
	rig.enterWindow()
	st := rig.status()
	if st.Status != RenewalDue || st.RenewalMode != RenewalManual || !strings.Contains(st.Detail, "manual renewal") {
		t.Fatalf("status = %+v", st)
	}
	if got := rig.pending(); len(got) != 0 {
		t.Fatalf("manual approval: pending = %v", got)
	}
	calls := rig.caCalls.Load()
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err == nil || rig.caCalls.Load() != calls {
		t.Fatalf("manual approval renewed: %v", err)
	}
}

func TestApprovalsWithoutRecordedModeDefaultToAuto(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	// A row written before the mode existed carries the column default.
	if _, err := rig.store.DB().Exec(`DELETE FROM cert_issuance_approvals`); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.store.DB().Exec(`INSERT INTO cert_issuance_approvals
(certificate, digest, plan, actor, agreed_terms, confirmed_production, approved_at)
VALUES ('www', 'd', '{}', 'owner', 1, 0, '2026-10-14T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	list, err := rig.issuing.Approvals()
	if err != nil || len(list) != 1 || list[0].RenewalMode != RenewalAuto {
		t.Fatalf("approvals = %+v err %v", list, err)
	}
}

func TestRenewGateComparesTheDirectoryTheCertificateCameFrom(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	list, _ := NewIssuances(rig.store).List()
	if len(list) != 1 || list[0].Directory != issuanceDirectory {
		t.Fatalf("issuance did not record its directory: %+v", list)
	}
	rig.enterWindow()
	if _, err := rig.store.DB().Exec(`UPDATE cert_issuances SET directory = ?`, prodDirectory); err != nil {
		t.Fatal(err)
	}
	if st := rig.status(); st.Status != RenewalPlanRequired || !strings.Contains(st.Detail, "was issued from") {
		t.Fatalf("status = %+v", st)
	}
	calls := rig.caCalls.Load()
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); !errors.Is(err, ErrPlanRequired) || rig.caCalls.Load() != calls {
		t.Fatalf("Renew = %v", err)
	}
}

func TestProblemSurvivesCleanupFailure(t *testing.T) {
	// The CA refuses finalization after the TXT value was placed and the
	// provider then fails the cleanup: Retry-After and the account problem
	// must still reach the retry logic.
	t.Run("Retry-After", func(t *testing.T) {
		rig := newRenewRig(t, issuanceDirectory, false)
		rig.enterWindow()
		rig.double.finalFault = &acmeFault{status: http.StatusTooManyRequests, problem: ProblemRateLimited,
			detail: "slow down", retryAfter: "129600"}
		rig.provider.deleteErr = errors.New("provider delete refused")
		err := rig.issuing.Renew(context.Background(), "www", "scheduler")
		var problem *ProblemError
		if err == nil || !errors.As(err, &problem) || problem.RetryAfter != 36*time.Hour ||
			!strings.Contains(err.Error(), "cleanup also failed") {
			t.Fatalf("err = %v", err)
		}
		if s := rig.state(); !s.NextAttemptAt.Equal(rig.clock.Add(36 * time.Hour)) {
			t.Fatalf("state = %+v", s)
		}
	})
	t.Run("account does not exist (production)", func(t *testing.T) {
		rig := newRenewRig(t, prodDirectory, true)
		rig.enterWindow()
		rig.double.finalFault = &acmeFault{status: http.StatusBadRequest, problem: ProblemAccountDoesNotExist, detail: "gone"}
		rig.provider.deleteErr = errors.New("provider delete refused")
		if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err == nil {
			t.Fatal("expected failure")
		}
		if s := rig.state(); !s.AccountRecovery {
			t.Fatalf("state = %+v: the production account problem was lost", s)
		}
	})
}

func TestProductionReregistrationNeedsFreshTermsAgreement(t *testing.T) {
	rig := newIssuanceRig(t, prodDirectory)
	rig.double.terms = "https://ca.example/terms-v1"
	plan, _ := rig.issuing.Plan("www")
	if _, err := rig.issuing.Approve("www", plan.Digest, "owner", true, true); err != nil {
		t.Fatal(err)
	}
	def, _ := rig.issuing.Definition("www")
	if err := rig.run(def); err != nil {
		t.Fatal(err)
	}
	// The CA publishes new terms.
	rig.double.terms = "https://ca.example/terms-v2"
	terms, err := rig.issuing.AccountTerms(context.Background(), "www")
	if err != nil || !terms.Changed || !terms.AgreementRequired || terms.CurrentURL != "https://ca.example/terms-v2" ||
		terms.RecordedURL != "https://ca.example/terms-v1" || terms.DirectoryClass != DirectoryProduction {
		t.Fatalf("terms = %+v err %v", terms, err)
	}
	regs := rig.double.registrations
	err = rig.issuing.ReregisterAccount(context.Background(), "www", "owner", false)
	if !errors.Is(err, ErrTermsRequired) || !strings.Contains(err.Error(), "terms-v2") || rig.double.registrations != regs {
		t.Fatalf("without agreement = %v (registrations %d)", err, rig.double.registrations)
	}
	if err := rig.issuing.ReregisterAccount(context.Background(), "www", "owner", true); err != nil {
		t.Fatalf("with agreement: %v", err)
	}
	if rig.double.registrations != regs+1 {
		t.Fatalf("registrations = %d", rig.double.registrations)
	}
	again, _ := rig.issuing.AccountTerms(context.Background(), "www")
	if again.RecordedURL != "https://ca.example/terms-v2" || again.Changed {
		t.Fatalf("recorded terms after re-registration = %+v", again)
	}
}

func TestAutomaticRecoveryAuditsChangedTerms(t *testing.T) {
	r2 := newIssuanceRig(t, issuanceDirectory)
	r2.double.terms = "https://ca.example/terms-v1"
	var audits []auditEntry
	r2.issuing.Audit = func(actor, action, outcome, detail string) error {
		audits = append(audits, auditEntry{actor, action, outcome, detail})
		return nil
	}
	plan, _ := r2.issuing.Plan("www")
	if _, err := r2.issuing.Approve("www", plan.Digest, "owner", true, false); err != nil {
		t.Fatal(err)
	}
	def, _ := r2.issuing.Definition("www")
	if err := r2.run(def); err != nil {
		t.Fatal(err)
	}
	r2.clock = r2.clock.Add(40 * 24 * time.Hour)
	r2.double.forgotten = true
	r2.double.terms = "https://ca.example/terms-v2"
	if err := r2.issuing.Renew(context.Background(), "www", "scheduler"); err != nil {
		t.Fatalf("test/staging recovery must proceed despite changed terms: %v", err)
	}
	var found bool
	for _, a := range audits {
		if a.action == "cert.account.reregister" && strings.Contains(a.detail, "terms-v2") &&
			strings.Contains(a.detail, "changed from") && strings.Contains(a.detail, "terms-v1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestRenewFailureBackoffCapResetAndAudit(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	rig.enterWindow()
	rig.double.fault = &acmeFault{status: http.StatusInternalServerError, problem: "urn:ietf:params:acme:error:serverInternal", detail: "boom"}
	delays := []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour,
		24 * time.Hour, 24 * time.Hour}
	for n, delay := range delays {
		if got := rig.pending(); len(got) != 1 {
			t.Fatalf("attempt %d: pending = %v", n+1, got)
		}
		if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err == nil {
			t.Fatalf("attempt %d succeeded against a failing CA", n+1)
		}
		s := rig.state()
		if s.Attempts != n+1 || !s.NextAttemptAt.Equal(rig.clock.Add(delay)) || !strings.Contains(s.LastError, "boom") {
			t.Fatalf("attempt %d: state = %+v, want next %s", n+1, s, rig.clock.Add(delay))
		}
		// Nothing is attempted before the stored next-attempt time (the
		// hourly evaluation finds the job not yet due).
		rig.clock = rig.clock.Add(delay - time.Minute)
		if got := rig.pending(); len(got) != 0 {
			t.Fatalf("attempt %d: pending before next time = %v", n+1, got)
		}
		if !rig.issuing.RetryPending() {
			t.Fatal("hourly retry evaluation should be wanted while failing")
		}
		rig.clock = rig.clock.Add(time.Minute)
	}
	if len(rig.audits) != len(delays) {
		t.Fatalf("audits = %d, want one per attempt", len(rig.audits))
	}
	for _, a := range rig.audits {
		if a.outcome != "error" || a.action != "cert.renewal.attempt" || !strings.Contains(a.detail, "next attempt at") {
			t.Fatalf("audit = %+v", a)
		}
	}
	if st := rig.status(); st.Attempts != 7 || !strings.Contains(st.Detail, "failed 7 time(s) in a row") ||
		!strings.Contains(st.LastError, "boom") {
		t.Fatalf("status = %+v", st)
	}
	// Success resets the counter.
	rig.double.fault = nil
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if s := rig.state(); s.Attempts != 0 || s.LastError != "" || !s.NextAttemptAt.IsZero() {
		t.Fatalf("state after success = %+v", s)
	}
	if rig.issuing.RetryPending() {
		t.Fatal("retry evaluation still wanted after success")
	}
}

func TestRenewBackoffPersistsAcrossRestart(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	rig.enterWindow()
	rig.double.fault = &acmeFault{status: http.StatusInternalServerError, problem: "urn:ietf:params:acme:error:serverInternal", detail: "boom"}
	for i := 0; i < 3; i++ {
		_ = rig.issuing.Renew(context.Background(), "www", "scheduler")
		rig.clock = rig.state().NextAttemptAt
	}
	// A restarted daemon reopens the database and builds a new runner.
	reopened, err := store.Open(rig.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s, err := NewRenewalStates(reopened).Get("www")
	if err != nil || s.Attempts != 3 {
		t.Fatalf("restarted state = %+v err %v", s, err)
	}
	next := &Issuing{Store: reopened, Wizards: rig.issuing.Wizards, Secrets: rig.issuing.Secrets,
		Providers: rig.issuing.Providers, StackDir: rig.stackDir, NewClient: rig.issuing.NewClient,
		Probe: rig.issuing.Probe, Interval: time.Millisecond, Routes: rig.issuing.Routes,
		Reload: rig.issuing.Reload, Now: rig.issuing.Now}
	// The fourth failure continues the sequence: 8 hours, not 1.
	if err := next.Renew(context.Background(), "www", "scheduler"); err == nil {
		t.Fatal("expected failure")
	}
	s, _ = NewRenewalStates(reopened).Get("www")
	if s.Attempts != 4 || !s.NextAttemptAt.Equal(rig.clock.Add(8*time.Hour)) {
		t.Fatalf("after restart: %+v, want attempts 4 next %s", s, rig.clock.Add(8*time.Hour))
	}
	if !next.RetryPending() {
		t.Fatal("a restarted daemon must keep evaluating a failing renewal")
	}
}

func TestRenewHonoursRetryAfter(t *testing.T) {
	rate := func(retryAfter string) *acmeFault {
		return &acmeFault{status: http.StatusTooManyRequests, problem: ProblemRateLimited,
			detail: "too many certificates", retryAfter: retryAfter}
	}
	for name, tc := range map[string]struct {
		retryAfter string
		want       time.Duration
	}{
		"beyond the backoff and the cap": {"129600", 36 * time.Hour},
		"shorter than the backoff":       {"60", time.Hour},
		"absurd values are bounded":      {"99999999", MaxRetryAfter},
		"absent":                         {"", time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newRenewRig(t, issuanceDirectory, false)
			rig.enterWindow()
			rig.double.fault = rate(tc.retryAfter)
			if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err == nil {
				t.Fatal("rate-limited renewal succeeded")
			}
			s := rig.state()
			if s.Attempts != 1 || !s.NextAttemptAt.Equal(rig.clock.Add(tc.want)) {
				t.Fatalf("state = %+v, want next %s", s, rig.clock.Add(tc.want))
			}
			if !strings.Contains(s.LastError, "rateLimited") {
				t.Fatalf("last error = %q", s.LastError)
			}
			// Not attempted just before Retry-After lapses, attempted after.
			rig.clock = s.NextAttemptAt.Add(-time.Second)
			if got := rig.pending(); len(got) != 0 {
				t.Fatalf("attempted inside Retry-After: %v", got)
			}
			rig.clock = s.NextAttemptAt
			if got := rig.pending(); len(got) != 1 {
				t.Fatalf("not retried after Retry-After: %v", got)
			}
		})
	}
}

func TestRenewForgottenAccountRecoversForTestAndStaging(t *testing.T) {
	for _, directory := range []string{issuanceDirectory, testDirectory} {
		t.Run(ClassifyDirectory(directory), func(t *testing.T) {
			rig := newRenewRig(t, directory, false)
			rig.enterWindow()
			before, _ := rig.secrets.Get(AccountSecretName(directory))
			rig.double.forgotten = true
			if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err != nil {
				t.Fatalf("Renew with a forgotten account: %v", err)
			}
			after, _ := rig.secrets.Get(AccountSecretName(directory))
			if before == after || after == "" {
				t.Fatal("the account key was not replaced")
			}
			if rig.double.registrations != 2 {
				t.Fatalf("registrations = %d, want 2", rig.double.registrations)
			}
			var reregistered, renewed bool
			for _, a := range rig.audits {
				reregistered = reregistered || (a.action == "cert.account.reregister" && a.outcome == "ok")
				renewed = renewed || (a.action == "cert.renewal.attempt" && a.outcome == "ok")
				if strings.Contains(a.detail, "PRIVATE KEY") || strings.Contains(a.detail, after) {
					t.Fatalf("audit leaked the account key: %s", a.detail)
				}
			}
			if !reregistered || !renewed {
				t.Fatalf("audits = %+v", rig.audits)
			}
		})
	}
}

func TestRenewForgottenAccountProductionNeedsOwner(t *testing.T) {
	rig := newRenewRig(t, prodDirectory, true)
	rig.enterWindow()
	before, _ := rig.secrets.Get(AccountSecretName(prodDirectory))
	rig.double.forgotten = true
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err == nil {
		t.Fatal("production renewal re-registered on its own")
	}
	if rig.double.registrations != 1 {
		t.Fatalf("registrations = %d: production must not auto re-register", rig.double.registrations)
	}
	if after, _ := rig.secrets.Get(AccountSecretName(prodDirectory)); after != before {
		t.Fatal("account key replaced without the owner")
	}
	s := rig.state()
	if !s.AccountRecovery || s.Attempts != 1 {
		t.Fatalf("state = %+v", s)
	}
	// Surfaced, and no longer attempted or retried hourly: it waits.
	st := rig.status()
	if !st.AccountRecoveryRequired || !strings.Contains(st.Detail, "owner action required") ||
		!strings.Contains(st.Detail, "reregister-account www") {
		t.Fatalf("status = %+v", st)
	}
	rig.clock = rig.clock.Add(48 * time.Hour)
	if got := rig.pending(); len(got) != 0 || rig.issuing.RetryPending() {
		t.Fatalf("waiting-on-owner renewal still attempted: %v", got)
	}
	last := rig.audits[len(rig.audits)-1]
	if last.outcome != "error" || !strings.Contains(last.detail, "owner action required") {
		t.Fatalf("audit = %+v", last)
	}
	// The owner route re-registers (audited), then renewal resumes.
	if err := rig.issuing.ReregisterAccount(context.Background(), "www", "owner", true); err != nil {
		t.Fatalf("ReregisterAccount: %v", err)
	}
	if rig.double.registrations != 2 {
		t.Fatalf("registrations = %d", rig.double.registrations)
	}
	last = rig.audits[len(rig.audits)-1]
	if last.action != "cert.account.reregister" || last.outcome != "ok" || last.actor != "owner" {
		t.Fatalf("audit = %+v", last)
	}
	if got := rig.pending(); len(got) != 1 {
		t.Fatalf("pending after re-registration = %v", got)
	}
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err != nil {
		t.Fatalf("Renew after re-registration: %v", err)
	}
}

func TestFirstIssuanceForgottenProductionAccountDoesNotAutoRecover(t *testing.T) {
	rig := newIssuanceRig(t, prodDirectory)
	plan, _ := rig.issuing.Plan("www")
	if _, err := rig.issuing.Approve("www", plan.Digest, "owner", true, true); err != nil {
		t.Fatal(err)
	}
	def, _ := rig.issuing.Definition("www")
	if err := rig.run(def); err != nil {
		t.Fatal(err)
	}
	rig.double.forgotten = true
	rig.clock = rig.clock.Add(time.Hour)
	// A fresh approval orders again; the CA has forgotten the account.
	plan, _ = rig.issuing.Plan("www")
	if _, err := rig.issuing.Approve("www", plan.Digest, "owner", true, true); err != nil {
		t.Fatal(err)
	}
	def, _ = rig.issuing.Definition("www")
	err := rig.run(def)
	var problem *ProblemError
	if !errors.As(err, &problem) || problem.Type != ProblemAccountDoesNotExist {
		t.Fatalf("apply = %v, want accountDoesNotExist", err)
	}
	if rig.double.registrations != 1 {
		t.Fatalf("registrations = %d", rig.double.registrations)
	}
}

func TestReregisterAccountAnyClassAndGuards(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	if err := rig.issuing.ReregisterAccount(context.Background(), "www", "", true); err == nil {
		t.Fatal("accepted no actor")
	}
	if err := rig.issuing.ReregisterAccount(context.Background(), "nope", "owner", true); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("unapproved = %v", err)
	}
	before, _ := rig.secrets.Get(AccountSecretName(issuanceDirectory))
	if err := rig.issuing.ReregisterAccount(context.Background(), "www", "owner", true); err != nil {
		t.Fatalf("staging re-registration: %v", err)
	}
	if after, _ := rig.secrets.Get(AccountSecretName(issuanceDirectory)); after == before {
		t.Fatal("key not replaced")
	}
	// A failed registration keeps the stored account.
	rig.double.fault = nil
	keep, _ := rig.secrets.Get(AccountSecretName(issuanceDirectory))
	rig.issuing.NewClient = func(string) (*Client, error) { return NewClient("http://127.0.0.1:1/directory") }
	if err := rig.issuing.ReregisterAccount(context.Background(), "www", "owner", true); err == nil {
		t.Fatal("registered against a dead CA")
	}
	if now, _ := rig.secrets.Get(AccountSecretName(issuanceDirectory)); now != keep {
		t.Fatal("failed re-registration replaced the account")
	}
	if last := rig.audits[len(rig.audits)-1]; last.outcome != "error" || last.action != "cert.account.reregister" {
		t.Fatalf("audit = %+v", last)
	}
}

func TestRenewReloadRefusedIsRetriedWithoutOrderingAgain(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	oldChain := rig.recordedChain()
	rig.enterWindow()
	rig.reloadErr = fmt.Errorf("caddy says no")
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err == nil {
		t.Fatal("expected the refused reload to fail the attempt")
	}
	// The certificate was renewed and placed; only the edge lags.
	renewed := rig.recordedChain()
	if string(renewed) == string(oldChain) {
		t.Fatal("renewal was not recorded")
	}
	s := rig.state()
	if !s.PendingReload || s.Attempts != 1 {
		t.Fatalf("state = %+v", s)
	}
	if st := rig.status(); st.Status != RenewalOK || !st.PendingReload || !strings.Contains(st.Detail, "edge has not adopted") {
		t.Fatalf("status = %+v", st)
	}
	if !rig.issuing.RetryPending() {
		t.Fatal("pending reload must be retried")
	}
	rig.clock = s.NextAttemptAt
	if got := rig.pending(); len(got) != 1 {
		t.Fatalf("pending = %v", got)
	}
	orders := rig.double.orders
	rig.reloadErr = nil
	reloads := rig.reloads
	if err := rig.issuing.Renew(context.Background(), "www", "scheduler"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if rig.double.orders != orders {
		t.Fatal("the retry ordered another certificate instead of reloading")
	}
	if rig.reloads != reloads+1 || rig.state().PendingReload || string(rig.recordedChain()) != string(renewed) {
		t.Fatalf("retry: reloads %d state %+v", rig.reloads-reloads, rig.state())
	}
}

func TestApprovalAndManualIssuanceClearRenewalFailures(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	rig.enterWindow()
	rig.double.fault = &acmeFault{status: http.StatusInternalServerError, problem: "urn:ietf:params:acme:error:serverInternal", detail: "boom"}
	_ = rig.issuing.Renew(context.Background(), "www", "scheduler")
	if rig.state().Attempts != 1 {
		t.Fatal("expected a failure on record")
	}
	rig.double.fault = nil
	plan, _ := rig.issuing.Plan("www")
	if _, err := rig.issuing.Approve("www", plan.Digest, "owner", true, false); err != nil {
		t.Fatal(err)
	}
	if s := rig.state(); s.Attempts != 0 || s.LastError != "" {
		t.Fatalf("a new approval left stale failure state: %+v", s)
	}
}

func TestRenewalReportOnlyAttachesStateOfKnownCertificates(t *testing.T) {
	rig := newRenewRig(t, issuanceDirectory, false)
	if err := NewRenewalStates(rig.store).Save(RenewalState{Certificate: "ghost", Attempts: 4}); err != nil {
		t.Fatal(err)
	}
	if st := rig.status(); st.Attempts != 0 {
		t.Fatalf("status = %+v", st)
	}
}
