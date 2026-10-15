package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/cert"
	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/store"
)

// dueIssuance approves the staging plan and records an issuance that is
// 20 days from expiry at the returned time.
func dueIssuance(t *testing.T) (*Server, *store.Store, *cert.Issuing, time.Time) {
	t.Helper()
	srv, st, issuing := issuanceServer(t)
	saveIssuanceInputs(t, st, cert.LetsEncryptStagingDirectory)
	issuedAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	clock := issuedAt.Add(40 * 24 * time.Hour)
	issuing.Now = func() time.Time { return clock }
	issuing.Audit = func(actor, action, outcome, detail string) error {
		_, err := audit.New(st).Record(audit.Entry{Actor: actor, Action: action, Outcome: outcome, Detail: detail})
		return err
	}
	plan, err := issuing.Plan("www")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuing.Approve("www", plan.Digest, "owner", true, false); err != nil {
		t.Fatal(err)
	}
	ca, err := cert.GenerateCA("Test CA", 365*24*time.Hour, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	chain, _, err := ca.IssueServerCert([]string{"www.example.com"}, 60*24*time.Hour, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	fp := cert.NewFingerprint([]string{"www.example.com"}, "fakedns", "dns-01")
	if err := cert.NewIssuances(st).Record("www", fp, chain, issuedAt); err != nil {
		t.Fatal(err)
	}
	return srv, st, issuing, clock
}

func opsOfType(t *testing.T, st *store.Store, typ string, want int) []operation.Operation {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		all, _ := operation.New(st).List(0)
		var ops []operation.Operation
		done := 0
		for _, op := range all {
			if op.Type == typ {
				ops = append(ops, op)
				if operation.Terminal(op.Status) {
					done++
				}
			}
		}
		if len(ops) == want && done == want {
			return ops
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s operations: want %d finished, have %+v", typ, want, ops)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func attemptAudits(t *testing.T, st *store.Store) []audit.Entry {
	t.Helper()
	entries, _ := audit.New(st).List(200)
	var out []audit.Entry
	for _, e := range entries {
		if e.Action == "cert.renewal.attempt" {
			out = append(out, e)
		}
	}
	return out
}

func TestScheduledReissueRetriesHourlyWithPersistedBackoff(t *testing.T) {
	srv, st, issuing, clock := dueIssuance(t)
	now := clock
	sched := NewSchedulerWith(st, t.TempDir(), nil, issuing, scheduler.WithClock(func() time.Time { return now }))
	issuing.Now = sched.Now // one injectable clock for scheduling and records
	srv.MountTLSRenewal(st, issuing, sched.Now)
	srv.MountDoctor(certDoctorChecks(st, issuing, sched.Now))

	// Healthy daemon, nothing failing: the hourly job is declared but off.
	jobs, _ := certRenewalSource(st, sched.Now, issuing).ScheduledJobs()
	if len(jobs) != 2 || jobs[1].ID != CertRenewalRetryJobID || jobs[1].Enabled || jobs[1].Interval != time.Hour ||
		jobs[0].ID != CertRenewalJobID || jobs[0].Interval != 24*time.Hour {
		t.Fatalf("jobs = %+v", jobs)
	}

	// The daily check finds renewal-due and attempts re-issue; the CA is
	// unreachable, so the attempt fails, is audited, and the run fails.
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	ops := opsOfType(t, st, "cert.renewal-check", 1)
	if ops[0].Status != operation.StatusFailed || !strings.Contains(ops[0].Error, "unattended re-issue failed") {
		t.Fatalf("daily op = %+v", ops[0])
	}
	state, _ := cert.NewRenewalStates(st).Get("www")
	if state.Attempts != 1 || !state.NextAttemptAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("state = %+v", state)
	}
	if got := attemptAudits(t, st); len(got) != 1 || got[0].Outcome != audit.OutcomeError || got[0].Actor != scheduler.Actor ||
		strings.Contains(got[0].Detail, "PRIVATE KEY") {
		t.Fatalf("audits = %+v", got)
	}

	// Now failing: the hourly job is on. Evaluated before the stored
	// next-attempt time it attempts nothing.
	jobs, _ = certRenewalSource(st, sched.Now, issuing).ScheduledJobs()
	if !jobs[1].Enabled {
		t.Fatal("hourly retry job must be enabled while failing")
	}
	now = now.Add(30 * time.Minute)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := attemptAudits(t, st); len(got) != 1 {
		t.Fatalf("attempted before the next-attempt time: %+v", got)
	}
	opsOfType(t, st, "cert.renewal-retry", 1) // evaluated hourly, attempted nothing
	// The retry job is evaluated hourly: its next run is past the stored
	// next-attempt time, so it attempts again and the counter and backoff
	// advance (1h, then 2h).
	now = clock.Add(90*time.Minute + time.Minute)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	opsOfType(t, st, "cert.renewal-retry", 2)
	state, _ = cert.NewRenewalStates(st).Get("www")
	if state.Attempts != 2 || !state.NextAttemptAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("state = %+v", state)
	}

	// Failures are on the API and the doctor.
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/renewal", nil)
	var report cert.RenewalReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil || len(report.Certificates) != 1 {
		t.Fatalf("renewal = %s (%v)", rec.Body.String(), err)
	}
	if c := report.Certificates[0]; c.Attempts != 2 || c.LastError == "" || c.NextAttemptAt.IsZero() ||
		!strings.Contains(c.Detail, "failed 2 time(s) in a row") {
		t.Fatalf("status = %+v", c)
	}
	var found bool
	for _, check := range certDoctorChecks(st, issuing, sched.Now)() {
		if check.Name == "tls-renewal:www" {
			found = true
			if check.Status != doctor.StatusWarn || !strings.Contains(check.Detail, "failed 2 time(s)") {
				t.Fatalf("doctor = %+v", check)
			}
		}
	}
	if !found {
		t.Fatal("doctor lacks the renewal check")
	}
}

func TestOwnerReregisterRouteIsAuditedAndGuarded(t *testing.T) {
	srv, st, _, _ := dueIssuance(t)
	// Unknown certificate: no approval on record.
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/account/reregister", map[string]any{"certificate": "nope"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("unapproved = %d (%s)", rec.Code, rec.Body.String())
	}
	// The CA is unreachable: the owner action fails, audited, and leaves
	// no account behind.
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/account/reregister", map[string]any{"certificate": "www"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unreachable CA = %d (%s)", rec.Code, rec.Body.String())
	}
	entries, _ := audit.New(st).List(50)
	var seen bool
	for _, e := range entries {
		if e.Action == "cert.account.reregister" && e.Outcome == audit.OutcomeError && e.Actor == "local" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("re-registration failure not audited: %+v", entries)
	}
}

func TestRenewalRunsNeverWaitOnEachOther(t *testing.T) {
	_, st, issuing, clock := dueIssuance(t)
	now := func() time.Time { return clock }
	issuing.Now = now
	def := renewalDefinition(st, issuing, now, "cert.renewal-retry", false)
	// Another run holds the renewal lock (a long issuance): this one must
	// return at once, audited as skipped, and attempt nothing.
	renewMu.Lock()
	done := make(chan error, 1)
	go func() { done <- def.Steps[0].Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("skipped run failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		renewMu.Unlock()
		t.Fatal("the retry run blocked on the renewal lock")
	}
	renewMu.Unlock()
	entries, _ := audit.New(st).List(20)
	var skipped bool
	for _, e := range entries {
		if e.Action == "cert.renewal.attempt" && e.Outcome == audit.OutcomeSkipped && e.Actor == scheduler.Actor &&
			strings.Contains(e.Detail, "still in progress") {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("overlap not audited as skipped: %+v", entries)
	}
	if s, _ := cert.NewRenewalStates(st).Get("www"); s.Attempts != 0 {
		t.Fatalf("a skipped run attempted: %+v", s)
	}
}
