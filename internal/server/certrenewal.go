package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"homestead/internal/audit"
	"homestead/internal/cert"
	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// CertRenewalJobID is the scheduler job of the certificate renewal
// check; CertRenewalInterval is its cadence (ADR-024: at most daily).
const (
	CertRenewalJobID    = "cert:renewal-check"
	CertRenewalInterval = 24 * time.Hour
	// CertRenewalRetryJobID is the hourly evaluation that runs only while
	// a renewal is failing (ADR-024: retry cadence is separate from the
	// daily check); CertRenewalRetryInterval is its cadence. Attempts
	// still wait for the stored next-attempt time.
	CertRenewalRetryJobID    = "cert:renewal-retry"
	CertRenewalRetryInterval = time.Hour
)

// renewalStatuses grades every certificate with a stored approved
// issuance against the saved TLS strategy at now (S263). No recorded
// issuance is an explicit state in the report, not an empty list.
func renewalStatuses(st *store.Store, now time.Time) (cert.RenewalReport, error) {
	return renewalReport(st, nil, now)
}

// renewalReport is renewalStatuses with the S272 refinements when the
// issuance runner is available: changed issuances read plan-required and
// retry state is attached. Without it the report is the S263 grading.
func renewalReport(st *store.Store, issuing *cert.Issuing, now time.Time) (cert.RenewalReport, error) {
	if issuing != nil {
		return issuing.RenewalReport(now)
	}
	issuances, err := cert.NewIssuances(st).List()
	if err != nil {
		return cert.RenewalReport{}, err
	}
	set, err := cert.LoadStrategy(wizard.New(st))
	if err != nil {
		if errors.Is(err, wizard.ErrNotFound) {
			return cert.NewRenewalReport(nil, len(issuances)), nil
		}
		return cert.RenewalReport{}, err
	}
	statuses, err := cert.CheckRenewals(set, issuances, now)
	if err != nil {
		return cert.RenewalReport{}, err
	}
	return cert.NewRenewalReport(statuses, len(issuances)), nil
}

// certRenewalSource declares the renewal jobs (ADR-023, ADR-024). The
// daily job reports renewal status and, with the issuance runner
// available, re-issues every certificate that is renewal-due with an
// unchanged approved issuance. The hourly retry job is enabled only while
// a renewal is failing; both attempt only certificates whose persisted
// next-attempt time has passed. Clean results stay quiet: only a non-ok
// status writes a check audit entry, and only expiry fails the check.
func certRenewalSource(st *store.Store, now func() time.Time, issuing *cert.Issuing) scheduler.Source {
	return scheduler.SourceFunc(func() ([]scheduler.Job, error) {
		jobs := []scheduler.Job{{
			ID:       CertRenewalJobID,
			Interval: CertRenewalInterval,
			Enabled:  true,
			// Never-run anchor: due on the first tick, then daily from the
			// last run (the anchor trails the clock by one interval).
			Since: now().Add(-CertRenewalInterval),
			Build: func() (operation.Definition, error) {
				return renewalDefinition(st, issuing, now, "cert.renewal-check", true), nil
			},
		}}
		if issuing != nil {
			jobs = append(jobs, scheduler.Job{
				ID:       CertRenewalRetryJobID,
				Interval: CertRenewalRetryInterval,
				Enabled:  issuing.RetryPending(),
				Since:    now().Add(-CertRenewalRetryInterval),
				Build: func() (operation.Definition, error) {
					return renewalDefinition(st, issuing, now, "cert.renewal-retry", false), nil
				},
			})
		}
		return jobs, nil
	})
}

// renewalDefinition is one scheduled renewal run: re-issue what is due
// (every certificate is attempted and recorded independently; the run
// fails at the end if any attempt failed), then, for the daily check,
// report status.
func renewalDefinition(st *store.Store, issuing *cert.Issuing, now func() time.Time, typ string, check bool) operation.Definition {
	var failures []string
	def := operation.Definition{Type: typ}
	if issuing != nil {
		def.Steps = append(def.Steps, operation.StepDef{Name: "reissue", Run: func(ctx context.Context) error {
			renewMu.Lock()
			defer renewMu.Unlock()
			due, err := issuing.PendingRenewals(now())
			if err != nil {
				return err
			}
			for _, name := range due {
				if err := issuing.Renew(ctx, name, scheduler.Actor); err != nil {
					failures = append(failures, name+": "+err.Error())
				}
			}
			return nil
		}})
	}
	if check {
		def.Steps = append(def.Steps, operation.StepDef{Name: "check", Run: func(context.Context) error {
			return runRenewalCheck(st, issuing, now())
		}})
	}
	if issuing != nil {
		def.Steps = append(def.Steps, operation.StepDef{Name: "report", Run: func(context.Context) error {
			if len(failures) > 0 {
				return fmt.Errorf("unattended re-issue failed: %s", strings.Join(failures, "; "))
			}
			return nil
		}})
	}
	return def
}

// renewMu keeps the daily and hourly renewal runs from attempting the
// same certificate at once.
var renewMu sync.Mutex

func runRenewalCheck(st *store.Store, issuing *cert.Issuing, now time.Time) error {
	report, err := renewalReport(st, issuing, now)
	if err != nil {
		return err
	}
	var notable []string
	expired := false
	for _, status := range report.Certificates {
		if status.Status == cert.RenewalOK || status.Status == cert.RenewalNoIssuance {
			continue
		}
		notable = append(notable, status.Certificate+": "+status.Status+" - "+status.Detail)
		if status.Status == cert.RenewalExpired || status.Status == cert.RenewalError {
			expired = true
		}
	}
	if len(notable) == 0 {
		return nil
	}
	outcome := audit.OutcomeOK
	if expired {
		outcome = audit.OutcomeError
	}
	if _, err := audit.New(st).Record(audit.Entry{
		Actor: scheduler.Actor, Action: "cert.renewal.check", Outcome: outcome,
		Detail: strings.Join(notable, "; "),
	}); err != nil {
		return err
	}
	if expired {
		return fmt.Errorf("certificate expired or unreadable: %s", strings.Join(notable, "; "))
	}
	return nil
}

// MountTLSRenewal serves renewal status (S263): GET /api/v1/tls/renewal.
func (s *Server) MountTLSRenewal(st *store.Store, issuing *cert.Issuing, now func() time.Time) {
	s.mux.HandleFunc("GET /api/v1/tls/renewal", func(w http.ResponseWriter, r *http.Request) {
		report, err := renewalReport(st, issuing, now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, report)
	})
}

// certDoctorChecks adds one doctor check per desired certificate:
// renewal-due and plan-required warn, expired and error (corrupt stored
// chain) fail, ok and no-recorded-issuance pass; failing unattended
// re-issue (S272) warns, and an account needing the owner fails. With nothing recorded
// at all, one explicit tls-renewal check says so.
func certDoctorChecks(st *store.Store, issuing *cert.Issuing, now func() time.Time) func() []doctor.Check {
	return func() []doctor.Check {
		report, err := renewalReport(st, issuing, now())
		if err != nil {
			return []doctor.Check{{Name: "tls-renewal", Status: doctor.StatusWarn,
				Detail: "renewal status unavailable: " + err.Error()}}
		}
		if report.Recorded == 0 {
			return []doctor.Check{{Name: "tls-renewal", Status: doctor.StatusOK, Detail: report.Summary}}
		}
		checks := make([]doctor.Check, 0, len(report.Certificates))
		for _, status := range report.Certificates {
			level := doctor.StatusOK
			switch status.Status {
			case cert.RenewalDue, cert.RenewalPlanRequired:
				level = doctor.StatusWarn
			case cert.RenewalExpired, cert.RenewalError:
				level = doctor.StatusFail
			}
			// A failing unattended re-issue or a renewed certificate the edge
			// has not adopted warns even while the status is still ok; an
			// account only the owner can re-register fails (S272).
			if level == doctor.StatusOK && (status.Attempts > 0 || status.PendingReload) {
				level = doctor.StatusWarn
			}
			if status.AccountRecoveryRequired && level != doctor.StatusFail {
				level = doctor.StatusFail
			}
			checks = append(checks, doctor.Check{Name: "tls-renewal:" + status.Certificate,
				Status: level, Detail: status.Status + " - " + status.Detail})
		}
		return checks
	}
}
