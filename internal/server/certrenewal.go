package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
)

// renewalStatuses grades every certificate with a stored approved
// issuance against the saved TLS strategy at now (S263). No recorded
// issuance is an explicit state in the report, not an empty list.
func renewalStatuses(st *store.Store, now time.Time) (cert.RenewalReport, error) {
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

// certRenewalSource declares the daily renewal check as a scheduler job
// (ADR-023). The check is an operation like any scheduled run. Clean
// results stay quiet: only a non-ok status writes an audit entry, and
// only expiry fails the operation. It reports; it never issues (S272).
func certRenewalSource(st *store.Store, now func() time.Time) scheduler.Source {
	return scheduler.SourceFunc(func() ([]scheduler.Job, error) {
		return []scheduler.Job{{
			ID:       CertRenewalJobID,
			Interval: CertRenewalInterval,
			Enabled:  true,
			// Never-run anchor: due on the first tick, then daily from the
			// last run (the anchor trails the clock by one interval).
			Since: now().Add(-CertRenewalInterval),
			Build: func() (operation.Definition, error) {
				return operation.Definition{
					Type: "cert.renewal-check",
					Steps: []operation.StepDef{{
						Name: "check",
						Run:  func(context.Context) error { return runRenewalCheck(st, now()) },
					}},
				}, nil
			},
		}}, nil
	})
}

func runRenewalCheck(st *store.Store, now time.Time) error {
	report, err := renewalStatuses(st, now)
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
func (s *Server) MountTLSRenewal(st *store.Store, now func() time.Time) {
	s.mux.HandleFunc("GET /api/v1/tls/renewal", func(w http.ResponseWriter, r *http.Request) {
		report, err := renewalStatuses(st, now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, report)
	})
}

// certDoctorChecks adds one doctor check per desired certificate:
// renewal-due and plan-required warn, expired and error (corrupt stored
// chain) fail, ok and no-recorded-issuance pass. With nothing recorded
// at all, one explicit tls-renewal check says so.
func certDoctorChecks(st *store.Store, now func() time.Time) func() []doctor.Check {
	return func() []doctor.Check {
		report, err := renewalStatuses(st, now())
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
			checks = append(checks, doctor.Check{Name: "tls-renewal:" + status.Certificate,
				Status: level, Detail: status.Status + " - " + status.Detail})
		}
		return checks
	}
}
