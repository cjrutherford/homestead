package cert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"homestead/internal/store"
	"homestead/internal/wizard"
)

// Unattended re-issue (S272, ADR-024). The scheduler asks Issuing which
// certificates are due; each is re-issued through the same steps S278's
// first issuance runs (IssueFor, validated placement, route refresh,
// forced reload), but only when the issuance is unchanged from the
// owner's approval and only against the CA directory (and class) the
// owner approved.

// Backoff bounds (ADR-024): the first retry waits an hour, each
// consecutive failure doubles it, capped at a day. A CA's Retry-After is
// honoured beyond the cap, up to MaxRetryAfter, so a hostile or broken
// header cannot silence renewal indefinitely.
const (
	RenewalBackoffBase = time.Hour
	RenewalBackoffCap  = 24 * time.Hour
	MaxRetryAfter      = 7 * 24 * time.Hour
)

// RenewalBackoff is the delay after the attempts-th consecutive failure.
func RenewalBackoff(attempts int) time.Duration {
	if attempts < 1 {
		return 0
	}
	delay := RenewalBackoffBase
	for n := 1; n < attempts; n++ {
		delay *= 2
		if delay >= RenewalBackoffCap {
			return RenewalBackoffCap
		}
	}
	return delay
}

// RenewalState is the persisted retry state of one certificate's renewal.
type RenewalState struct {
	Certificate   string
	Attempts      int
	NextAttemptAt time.Time
	LastAttemptAt time.Time
	LastSuccessAt time.Time
	LastError     string
	PendingReload bool
	// AccountRecovery is set when the CA forgot the account and the
	// directory is production: attempts stop until the owner re-registers.
	AccountRecovery bool
}

// RenewalStates persists renewal retry state in SQLite (migration 37).
type RenewalStates struct{ db *sql.DB }

// NewRenewalStates returns RenewalStates over an open store.
func NewRenewalStates(st *store.Store) *RenewalStates { return &RenewalStates{db: st.DB()} }

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

// Save stores (replacing) one certificate's state.
func (r *RenewalStates) Save(s RenewalState) error {
	_, err := r.db.Exec(`INSERT INTO cert_renewal_state
(certificate, attempts, next_attempt_at, last_attempt_at, last_success_at, last_error, pending_reload, account_recovery)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(certificate) DO UPDATE SET attempts = excluded.attempts,
	next_attempt_at = excluded.next_attempt_at, last_attempt_at = excluded.last_attempt_at,
	last_success_at = excluded.last_success_at, last_error = excluded.last_error,
	pending_reload = excluded.pending_reload, account_recovery = excluded.account_recovery`,
		s.Certificate, s.Attempts, fmtTime(s.NextAttemptAt), fmtTime(s.LastAttemptAt), fmtTime(s.LastSuccessAt),
		s.LastError, boolInt(s.PendingReload), boolInt(s.AccountRecovery))
	if err != nil {
		return fmt.Errorf("cert: save renewal state: %w", err)
	}
	return nil
}

// Clear forgets a certificate's failure state (a new approval or a
// manual issuance settles it); the last success time is kept.
func (r *RenewalStates) Clear(name string) error {
	_, err := r.db.Exec(`UPDATE cert_renewal_state SET attempts = 0, next_attempt_at = '', last_error = '',
pending_reload = 0, account_recovery = 0 WHERE certificate = ?`, name)
	if err != nil {
		return fmt.Errorf("cert: clear renewal state: %w", err)
	}
	return nil
}

// List returns every stored state keyed by certificate.
func (r *RenewalStates) List() (map[string]RenewalState, error) {
	rows, err := r.db.Query(`SELECT certificate, attempts, next_attempt_at, last_attempt_at, last_success_at,
last_error, pending_reload, account_recovery FROM cert_renewal_state`)
	if err != nil {
		return nil, fmt.Errorf("cert: list renewal state: %w", err)
	}
	defer rows.Close()
	out := map[string]RenewalState{}
	for rows.Next() {
		var s RenewalState
		var next, last, ok string
		var reload, recovery int
		if err := rows.Scan(&s.Certificate, &s.Attempts, &next, &last, &ok, &s.LastError, &reload, &recovery); err != nil {
			return nil, err
		}
		s.NextAttemptAt, s.LastAttemptAt, s.LastSuccessAt = parseTime(next), parseTime(last), parseTime(ok)
		s.PendingReload, s.AccountRecovery = reload != 0, recovery != 0
		out[s.Certificate] = s
	}
	return out, rows.Err()
}

// Get returns one certificate's state (zero value when none).
func (r *RenewalStates) Get(name string) (RenewalState, error) {
	all, err := r.List()
	if err != nil {
		return RenewalState{}, err
	}
	s, ok := all[name]
	if !ok {
		s = RenewalState{Certificate: name}
	}
	return s, nil
}

// Failing reports state that wants the hourly retry evaluation: a failed
// renewal or a renewed certificate the edge has not adopted, and not
// waiting on the owner.
func (s RenewalState) Failing() bool {
	return (s.Attempts > 0 || s.PendingReload) && !s.AccountRecovery
}

// RenewalGate is the ADR-024 verdict for one certificate.
type RenewalGate struct {
	// Eligible is false for a certificate this runner never issues
	// (not ACME dns-01), which keeps its S263 status unchanged.
	Eligible bool
	// Reason is non-empty when an eligible certificate needs the plan,
	// preview, approve flow instead of unattended re-issue.
	Reason string
}

// Unchanged reports an eligible certificate that may be re-issued.
func (g RenewalGate) Unchanged() bool { return g.Eligible && g.Reason == "" }

// approvalNamed reads the recorded approval of a certificate.
func (i *Issuing) approvalNamed(name string) (Approval, bool, error) {
	list, err := i.Approvals()
	if err != nil {
		return Approval{}, false, err
	}
	for _, a := range list {
		if a.Certificate == name {
			return a, true, nil
		}
	}
	return Approval{}, false, nil
}

// Gate decides whether certificate c may be re-issued unattended from
// its recorded issuance rec. Beyond the ADR-024 fingerprint (names,
// provider, challenge) it refuses any change of CA directory (and so
// class), DNS zone, or production confirmation relative to the owner's
// approval: renewal never reaches a CA, class, or zone the owner did
// not approve.
func (i *Issuing) Gate(c Certificate, rec Issuance, approval Approval, hasApproval bool) RenewalGate {
	if !isDNS01ACME(c) {
		return RenewalGate{}
	}
	g := RenewalGate{Eligible: true}
	desired := NewFingerprint(c.DNSNames, c.Issuer.DNSProvider, string(c.Issuer.Challenge))
	switch {
	case !hasApproval:
		g.Reason = fmt.Sprintf("%s has no owner approval on record for unattended re-issue; plan, approve, and apply it first", c.Name)
		return g
	case len(desired.InvalidNames()) > 0:
		g.Reason = fmt.Sprintf("%s has invalid desired names %v; fix the strategy and re-plan", c.Name, desired.InvalidNames())
		return g
	case !desired.Equal(rec.Fingerprint):
		g.Reason = fmt.Sprintf("%s desired issuance differs from the issued one (issued %s; desired %s); re-plan and approve", c.Name, rec.Fingerprint, desired)
		return g
	}
	approved := NewFingerprint(approval.Plan.Names, approval.Plan.Provider, approval.Plan.Challenge)
	if !desired.Equal(approved) {
		g.Reason = fmt.Sprintf("%s desired issuance differs from the approved one (approved %s; desired %s); re-plan and approve", c.Name, approved, desired)
		return g
	}
	if rec.Directory != "" && rec.Directory != approval.Plan.Directory {
		g.Reason = fmt.Sprintf("%s was issued from %s but the approval is for %s; re-plan and approve; renewal never reaches a different CA",
			c.Name, rec.Directory, approval.Plan.Directory)
		return g
	}
	if c.Issuer.Directory != approval.Plan.Directory {
		g.Reason = fmt.Sprintf("%s CA directory changed since approval (approved %s, now %s); re-plan and approve; renewal never reaches a different CA",
			c.Name, approval.Plan.Directory, c.Issuer.Directory)
		return g
	}
	if ClassifyDirectory(approval.Plan.Directory) == DirectoryProduction && !approval.ConfirmedProduction {
		g.Reason = fmt.Sprintf("%s approval did not confirm production issuance; re-approve with the production confirmation", c.Name)
		return g
	}
	if _, zone := i.domainZone(); zone != approval.Plan.Zone {
		g.Reason = fmt.Sprintf("%s DNS zone changed since approval (approved %q, now %q); re-plan and approve", c.Name, approval.Plan.Zone, zone)
		return g
	}
	return g
}

// RenewalReport grades every desired certificate (S263) and applies the
// S272 refinements: an eligible certificate whose issuance changed is
// plan-required, and recorded retry state (attempts, last failure, next
// attempt, owner actions) is attached. Nothing is issued.
func (i *Issuing) RenewalReport(now time.Time) (RenewalReport, error) {
	issuances, err := NewIssuances(i.Store).List()
	if err != nil {
		return RenewalReport{}, err
	}
	set, err := LoadStrategy(i.Wizards)
	if err != nil {
		if errors.Is(err, wizard.ErrNotFound) {
			return NewRenewalReport(nil, len(issuances)), nil
		}
		return RenewalReport{}, err
	}
	statuses, err := CheckRenewals(set, issuances, now)
	if err != nil {
		return RenewalReport{}, err
	}
	return NewRenewalReport(i.decorate(set, issuances, statuses), len(issuances)), nil
}

func (i *Issuing) decorate(set Set, issuances []Issuance, statuses []RenewalStatus) []RenewalStatus {
	states, _ := NewRenewalStates(i.Store).List()
	approvals, _ := i.approvalMap()
	recs := map[string]Issuance{}
	for _, rec := range issuances {
		recs[rec.Certificate] = rec
	}
	byName := map[string]Certificate{}
	for _, c := range set.Certificates {
		byName[c.Name] = c
	}
	for n := range statuses {
		st := &statuses[n]
		c, rec := byName[st.Certificate], recs[st.Certificate]
		if st.Status == RenewalDue || st.Status == RenewalOK {
			approval, ok := approvals[c.Name]
			gate := i.Gate(c, rec, approval, ok)
			if gate.Eligible && ok {
				st.RenewalMode = approval.RenewalMode
			}
			if st.Status == RenewalDue && gate.Eligible {
				switch {
				case gate.Reason != "":
					st.Status = RenewalPlanRequired
					st.Detail = gate.Reason
				case approval.RenewalMode == RenewalManual:
					st.Detail += "; the approval chose manual renewal, so Homestead will not re-issue it: re-run the plan, approve, apply flow before it expires"
				default:
					st.Detail += "; Homestead re-issues it unattended while the approved issuance is unchanged"
				}
			}
		}
		state, ok := states[st.Certificate]
		if !ok {
			continue
		}
		st.Attempts, st.LastAttemptAt, st.NextAttemptAt = state.Attempts, state.LastAttemptAt, state.NextAttemptAt
		st.LastError, st.PendingReload, st.AccountRecoveryRequired = state.LastError, state.PendingReload, state.AccountRecovery
		switch {
		case state.AccountRecovery:
			st.Detail += fmt.Sprintf("; owner action required: the CA no longer knows the ACME account; re-register it (homesteadctl tls issuance reregister-account %s) - last error: %s",
				st.Certificate, state.LastError)
		case state.PendingReload:
			st.Detail += fmt.Sprintf("; the renewed certificate is placed but the edge has not adopted it (attempt %d failed: %s); next attempt at %s",
				state.Attempts, state.LastError, state.NextAttemptAt.UTC().Format(time.RFC3339))
		case state.Attempts > 0:
			st.Detail += fmt.Sprintf("; unattended re-issue failed %d time(s) in a row (last: %s); next attempt at %s",
				state.Attempts, state.LastError, state.NextAttemptAt.UTC().Format(time.RFC3339))
		}
	}
	return statuses
}

// PendingRenewals lists the certificates to attempt now: renewal-due with
// an unchanged issuance whose next-attempt time has passed, plus renewed
// certificates still waiting for an edge reload. Certificates waiting on
// the owner (account recovery) are skipped.
func (i *Issuing) PendingRenewals(now time.Time) ([]string, error) {
	report, err := i.RenewalReport(now)
	if err != nil {
		return nil, err
	}
	set, err := LoadStrategy(i.Wizards)
	if err != nil {
		return nil, err
	}
	states, err := NewRenewalStates(i.Store).List()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, st := range report.Certificates {
		state := states[st.Certificate]
		if state.AccountRecovery || now.Before(state.NextAttemptAt) {
			continue
		}
		if state.PendingReload && st.Status != RenewalExpired && st.Status != RenewalError {
			out = append(out, st.Certificate)
			continue
		}
		if st.Status != RenewalDue {
			continue
		}
		for _, c := range set.Certificates {
			if c.Name == st.Certificate && isDNS01ACME(c) && st.RenewalMode == RenewalAuto {
				out = append(out, st.Certificate)
			}
		}
	}
	return out, nil
}

// RetryPending reports whether any renewal is failing and so wants the
// hourly retry evaluation (ADR-024).
func (i *Issuing) RetryPending() bool {
	states, err := NewRenewalStates(i.Store).List()
	if err != nil {
		return false
	}
	for _, s := range states {
		if s.Failing() {
			return true
		}
	}
	return false
}

// Renew makes one unattended re-issue attempt for name and records it:
// the audit entry (attempt number, outcome, next attempt; never key
// material) and the persisted retry state. Failures back off 1h, 2h, 4h
// ... capped at 24h; a success resets the counter; a CA Retry-After is
// honoured. It refuses, without contacting any CA, anything the gate
// calls changed.
func (i *Issuing) Renew(ctx context.Context, name, actor string) error {
	now := i.now()
	states := NewRenewalStates(i.Store)
	state, err := states.Get(name)
	if err != nil {
		return err
	}
	attempt := state.Attempts + 1
	run, runErr := i.renewRun(ctx, name, state)
	state.LastAttemptAt = now
	if runErr == nil || (run != nil && run.delivered && !run.reloadFailed) {
		// Re-issued, placed and (when the edge answered) served: a leftover
		// cleanup or recording warning is reported but does not retry.
		detail := fmt.Sprintf("%s re-issued unattended (attempt %d) from %s", name, attempt, run.plan.Directory)
		outcome := "ok"
		state.Attempts, state.NextAttemptAt, state.PendingReload, state.AccountRecovery = 0, time.Time{}, false, false
		state.LastSuccessAt, state.LastError = now, ""
		if runErr != nil {
			state.LastError = runErr.Error()
			detail += "; warning: " + runErr.Error()
		}
		if err := states.Save(state); err != nil {
			return err
		}
		return i.audit(actor, "cert.renewal.attempt", outcome, detail)
	}
	state.Attempts = attempt
	state.LastError = runErr.Error()
	delay := RenewalBackoff(attempt)
	var problem *ProblemError
	if errors.As(runErr, &problem) && problem.RetryAfter > delay {
		delay = problem.RetryAfter
		if delay > MaxRetryAfter {
			delay = MaxRetryAfter
		}
	}
	state.NextAttemptAt = now.Add(delay)
	state.PendingReload = run != nil && run.reloadFailed
	if errors.As(runErr, &problem) && problem.Type == ProblemAccountDoesNotExist &&
		run != nil && run.plan.DirectoryClass == DirectoryProduction {
		state.AccountRecovery = true
	}
	if err := states.Save(state); err != nil {
		return err
	}
	detail := fmt.Sprintf("%s unattended re-issue attempt %d failed: %s; next attempt at %s",
		name, attempt, runErr.Error(), state.NextAttemptAt.UTC().Format(time.RFC3339))
	if state.AccountRecovery {
		detail += "; owner action required: re-register the ACME account"
	}
	return errors.Join(runErr, i.audit(actor, "cert.renewal.attempt", "error", detail))
}

// renewRun runs the issuance steps for an unchanged issuance. The
// returned run is nil when the attempt was refused before any step.
func (i *Issuing) renewRun(ctx context.Context, name string, state RenewalState) (*issueRun, error) {
	plan, err := i.Plan(name)
	if err != nil {
		return nil, err
	}
	certificate, err := i.certificateNamed(name)
	if err != nil {
		return nil, err
	}
	issuances, err := NewIssuances(i.Store).List()
	if err != nil {
		return nil, err
	}
	var rec Issuance
	found := false
	for _, r := range issuances {
		if r.Certificate == name {
			rec, found = r, true
		}
	}
	if !found {
		return nil, fmt.Errorf("cert: %s has no recorded issuance to renew", name)
	}
	approval, hasApproval, err := i.approvalNamed(name)
	if err != nil {
		return nil, err
	}
	if gate := i.Gate(certificate, rec, approval, hasApproval); !gate.Unchanged() {
		reason := gate.Reason
		if reason == "" {
			reason = name + " is not an ACME dns-01 certificate"
		}
		return nil, fmt.Errorf("%w: %s", ErrPlanRequired, reason)
	}
	if approval.RenewalMode == RenewalManual {
		return nil, fmt.Errorf("%w: the approval of %s chose manual renewal", ErrPlanRequired, name)
	}
	if !plan.Ready {
		return nil, fmt.Errorf("cert: plan is not ready: %s", strings.Join(plan.Blockers, "; "))
	}
	run := &issueRun{
		plan: plan, certificate: certificate, approval: approval,
		// Never wider than approved: only the approved routes' snippets.
		served: approval.Plan.ServedRoutes,
		// A renewal orders a new certificate; only a pending reload reuses
		// the one already placed and recorded.
		force: !state.PendingReload,
	}
	if state.PendingReload {
		run.approval.ApprovedAt = time.Time{} // the placed renewal postdates any approval
	}
	for _, step := range i.steps(run).Steps {
		if err := step.Run(ctx); err != nil {
			return run, err
		}
	}
	return run, nil
}

// ErrPlanRequired reports a renewal refused because the issuance changed
// since the owner approved it.
var ErrPlanRequired = errors.New("cert: plan required")

// ErrTermsRequired reports a production account re-registration without
// a fresh agreement to the CA's current terms of service.
var ErrTermsRequired = errors.New("cert: terms of service agreement required")

// AccountTerms is what the owner needs to re-register an account: the
// approved directory and its class, the terms URL the CA publishes now,
// and the one recorded when the account was registered.
type AccountTerms struct {
	Directory      string `json:"directory"`
	DirectoryClass string `json:"directory_class"`
	// CurrentURL comes from the directory now (empty when the CA
	// publishes none); RecordedURL from the stored account.
	CurrentURL  string `json:"current_terms_url"`
	RecordedURL string `json:"recorded_terms_url"`
	Changed     bool   `json:"changed"`
	// AgreementRequired is true for production directories: the owner
	// must send agree_terms with the re-registration.
	AgreementRequired bool `json:"agreement_required"`
}

// AccountTerms reads the approved directory's current terms URL (one
// directory GET) next to the recorded one.
func (i *Issuing) AccountTerms(ctx context.Context, name string) (AccountTerms, error) {
	approval, ok, err := i.approvalNamed(name)
	if err != nil {
		return AccountTerms{}, err
	}
	if !ok {
		return AccountTerms{}, fmt.Errorf("%w: approve a plan for %q first", ErrNotApproved, name)
	}
	directory := approval.Plan.Directory
	out := AccountTerms{Directory: directory, DirectoryClass: ClassifyDirectory(directory)}
	out.AgreementRequired = out.DirectoryClass == DirectoryProduction
	client, err := i.client(directory)
	if err != nil {
		return AccountTerms{}, err
	}
	timeout := i.Timeout
	if timeout <= 0 {
		timeout = DefaultIssueTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dir, err := client.discover(ctx)
	if err != nil {
		return AccountTerms{}, err
	}
	out.CurrentURL = dir.Meta.TermsOfService
	if raw, err := i.Secrets.Get(AccountSecretName(directory)); err == nil {
		var stored storedAccount
		if json.Unmarshal([]byte(raw), &stored) == nil {
			out.RecordedURL = stored.Terms
		}
	}
	out.Changed = out.RecordedURL != "" && out.RecordedURL != out.CurrentURL
	return out, nil
}

// ReregisterAccount is the owner action for a CA that forgot the ACME
// account: a new account key is generated, registered with the approved
// directory, and only then replaces the stored one (a failed
// registration keeps the old). Test and staging use the terms agreement on
// the approval; production needs agreeTerms in this very request, a fresh
// acknowledgement of the CA's current terms. It never contacts a directory other than the approved
// one, and is audited. Any directory class may use it; it is the only
// way to recover a production account.
func (i *Issuing) ReregisterAccount(ctx context.Context, name, actor string, agreeTerms bool) error {
	if actor == "" {
		return fmt.Errorf("cert: re-registration needs an actor")
	}
	approval, ok, err := i.approvalNamed(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: approve a plan for %q first", ErrNotApproved, name)
	}
	directory := approval.Plan.Directory
	if ClassifyDirectory(directory) == DirectoryProduction && !approval.ConfirmedProduction {
		return fmt.Errorf("%w: production issuance was not confirmed", ErrNotApproved)
	}
	if ClassifyDirectory(directory) == DirectoryProduction && !agreeTerms {
		terms, termsErr := i.AccountTerms(ctx, name)
		if termsErr != nil {
			return termsErr
		}
		return fmt.Errorf("%w: a production re-registration needs a fresh agreement (agree_terms) to the CA's current terms %q",
			ErrTermsRequired, terms.CurrentURL)
	}
	fail := func(err error) error {
		return errors.Join(err, i.audit(actor, "cert.account.reregister", "error", fmt.Sprintf("%s: %s: %v", name, directory, err)))
	}
	client, err := i.client(directory)
	if err != nil {
		return fail(err)
	}
	key, err := GenerateAccountKey()
	if err != nil {
		return fail(err)
	}
	acct := &Account{Key: key}
	timeout := i.Timeout
	if timeout <= 0 {
		timeout = DefaultIssueTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := client.ensureAccount(ctx, acct, approval.AgreedTerms || agreeTerms); err != nil {
		return fail(err)
	}
	if err := i.saveAccount(directory, acct); err != nil {
		return fail(err)
	}
	states := NewRenewalStates(i.Store)
	state, err := states.Get(name)
	if err != nil {
		return fail(err)
	}
	state.AccountRecovery, state.NextAttemptAt, state.Attempts = false, time.Time{}, 0
	state.LastError = ""
	if err := states.Save(state); err != nil {
		return fail(err)
	}
	return i.audit(actor, "cert.account.reregister", "ok",
		fmt.Sprintf("%s: new ACME account registered with %s (%s) agreeing to terms %q; unattended renewal may resume",
			name, directory, ClassifyDirectory(directory), acct.Terms))
}
