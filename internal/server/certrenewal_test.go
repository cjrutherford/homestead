package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/cert"
	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

func renewalFixture(t *testing.T, issuedAt time.Time) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	issuer := cert.Issuer{Name: "le", Type: cert.IssuerACME, Directory: "https://ca.example/directory",
		Challenge: cert.ChallengeDNS01, DNSProvider: "cloudflare"}
	certificates := []cert.Certificate{{
		Name: "www", DNSNames: []string{"www.example.com"}, Issuer: issuer,
		State: cert.StateIssued, Renewal: cert.Renewal{Auto: true},
	}}
	raw, _ := json.Marshal(certificates)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	if _, err := wizard.New(st).Put(cert.TLSWizardID, "strategy", map[string]any{"certificates": decoded}); err != nil {
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
	fp := cert.NewFingerprint([]string{"www.example.com"}, "cloudflare", "dns-01")
	if err := cert.NewIssuances(st).Record("www", fp, chain, issuedAt); err != nil {
		t.Fatal(err)
	}
	return st
}

func waitTerminalOps(t *testing.T, st *store.Store, want int) []operation.Operation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		all, _ := operation.New(st).List(0)
		var ops []operation.Operation // other sources (drift) share the store
		for _, op := range all {
			if op.Type == "cert.renewal-check" {
				ops = append(ops, op)
			}
		}
		done := 0
		for _, op := range ops {
			if operation.Terminal(op.Status) {
				done++
			}
		}
		if len(ops) == want && done == want {
			return ops
		}
		if time.Now().After(deadline) {
			t.Fatalf("operations did not finish: %+v", ops)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func certAuditEntries(t *testing.T, st *store.Store) []audit.Entry {
	t.Helper()
	entries, err := audit.New(st).List(100)
	if err != nil {
		t.Fatal(err)
	}
	var out []audit.Entry
	for _, entry := range entries {
		if entry.Action == "cert.renewal.check" {
			out = append(out, entry)
		}
	}
	return out
}

func TestRenewalCheckSchedulesQuietlyAndReports(t *testing.T) {
	issuedAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	st := renewalFixture(t, issuedAt)
	now := issuedAt.Add(time.Hour)
	sched := NewScheduler(st, t.TempDir(), scheduler.WithClock(func() time.Time { return now }))
	srv := New("127.0.0.1:0")
	srv.MountTLSRenewal(st, sched.Now)
	srv.MountDoctor(certDoctorChecks(st, sched.Now))

	// Clean: the check runs as an operation but leaves no audit entry.
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	ops := waitTerminalOps(t, st, 1)
	if ops[0].Type != "cert.renewal-check" || ops[0].Status != operation.StatusSucceeded {
		t.Fatalf("op = %+v", ops[0])
	}
	if entries := certAuditEntries(t, st); len(entries) != 0 {
		t.Fatalf("clean check was noisy: %+v", entries)
	}
	var statuses []cert.RenewalStatus
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/renewal", nil)
	if err := json.Unmarshal(recorder.Body.Bytes(), &statuses); err != nil || len(statuses) != 1 ||
		statuses[0].Status != cert.RenewalOK {
		t.Fatalf("statuses = %s (%v)", recorder.Body.String(), err)
	}

	// Less than a day later nothing runs again (at most daily).
	now = now.Add(23 * time.Hour)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitTerminalOps(t, st, 1)

	// Inside the renewal window: audited, operation still succeeds, and
	// doctor warns.
	now = issuedAt.Add(40 * 24 * time.Hour)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	ops = waitTerminalOps(t, st, 2)
	entries := certAuditEntries(t, st)
	if len(entries) != 1 || entries[0].Actor != scheduler.Actor || !strings.Contains(entries[0].Detail, "renewal-due") {
		t.Fatalf("audit = %+v", entries)
	}
	var report doctor.Report
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/doctor", nil)
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, check := range report.Checks {
		if check.Name == "tls-renewal:www" {
			found = true
			if check.Status != doctor.StatusWarn {
				t.Fatalf("due check = %+v", check)
			}
		}
	}
	if !found {
		t.Fatalf("doctor lacks tls-renewal check: %+v", report.Checks)
	}

	// Expired fails the operation and the doctor.
	now = issuedAt.Add(70 * 24 * time.Hour)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	ops = waitTerminalOps(t, st, 3)
	failed := 0
	for _, op := range ops {
		if op.Status == operation.StatusFailed {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("ops = %+v", ops)
	}
	if got := certDoctorChecks(st, func() time.Time { return now })(); len(got) != 1 || got[0].Status != doctor.StatusFail {
		t.Fatalf("doctor = %+v", got)
	}
}

func TestRenewalStatusEmptyWithoutStrategy(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New("127.0.0.1:0")
	srv.MountTLSRenewal(st, time.Now)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/renewal", nil)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "[]" {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
	if err := runRenewalCheck(st, time.Now()); err != nil {
		t.Fatalf("no strategy must be quiet: %v", err)
	}
}
