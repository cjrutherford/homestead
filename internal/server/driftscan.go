package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/store"
)

// driftScanJobID is the scheduler job for the periodic scan (S261).
const driftScanJobID = "drift:scan"

// driftSource declares the periodic drift scan (S261, ADR-023). The
// cadence is the owner's saved schedule, read every tick so changes
// apply without restart. The scan is detection only: it records, and
// never accepts or restores.
func driftSource(st *store.Store, now func() time.Time) scheduler.Source {
	return scheduler.SourceFunc(func() ([]scheduler.Job, error) {
		schedule, err := catalog.GetScanSchedule(st)
		if err != nil {
			return nil, err
		}
		if schedule.UpdatedAt.IsZero() {
			// Never saved: persist the default so its cadence has a real
			// anchor (first scan one interval after first run) instead of
			// scanning immediately on every fresh install.
			if schedule, err = catalog.SaveScanSchedule(st, schedule.IntervalSeconds, schedule.Enabled, "default", now()); err != nil {
				return nil, err
			}
		}
		return []scheduler.Job{{
			ID:       driftScanJobID,
			Interval: time.Duration(schedule.IntervalSeconds) * time.Second,
			Enabled:  schedule.Enabled,
			Since:    schedule.UpdatedAt,
			Build: func() (operation.Definition, error) {
				return driftScanDefinition(st, now), nil
			},
		}}, nil
	})
}

// driftScanDefinition is one scan as an operation: scan, store the
// latest result, and audit it. A clean scan audits one short line; a
// drifted or unreadable one names packages and files.
func driftScanDefinition(st *store.Store, now func() time.Time) operation.Definition {
	return operation.Definition{
		Type: "catalog.drift-scan",
		Steps: []operation.StepDef{{
			Name: "scan",
			Run: func(ctx context.Context) error {
				result, err := catalog.ScanWithLive(ctx, st, driftDirFor, now(), driftLiveSource())
				if err != nil {
					return err
				}
				if err := catalog.SaveScan(st, result); err != nil {
					return err
				}
				outcome := audit.OutcomeOK
				if len(result.Errors) > 0 {
					outcome = audit.OutcomeError
				}
				_, err = audit.New(st).Record(audit.Entry{
					Actor: scheduler.Actor, Action: "drift.scan", Outcome: outcome,
					Detail: scanSummary(result),
				})
				if err != nil {
					return err
				}
				if len(result.Errors) > 0 {
					return fmt.Errorf("%d package(s) unreadable: %s", len(result.Errors), strings.Join(result.Errors, "; "))
				}
				return nil
			},
		}},
	}
}

// scanNotes renders informational scan state: stopped stacks and
// skipped live comparisons. Neither is drift.
func scanNotes(r catalog.ScanResult) string {
	out := ""
	if len(r.Stopped) > 0 {
		out += "; stopped (not drift): " + strings.Join(r.Stopped, ", ")
	}
	if len(r.LiveNotes) > 0 {
		out += "; live comparison limits: " + strings.Join(r.LiveNotes, "; ")
	}
	return out
}

func scanSummary(r catalog.ScanResult) string {
	if r.Clean() {
		out := fmt.Sprintf("clean: %d package(s) scanned", r.Scanned)
		if r.LiveSkipped != "" {
			out += "; live comparison skipped: " + r.LiveSkipped
		}
		return out + scanNotes(r)
	}
	var parts []string
	for _, d := range r.Drifted {
		files := make([]string, 0, len(d.Files))
		for _, f := range d.Files {
			files = append(files, f.Path+" "+f.Kind)
		}
		for _, f := range d.Live {
			files = append(files, "live "+f.Kind+" "+f.Service)
		}
		if d.LiveError != "" {
			files = append(files, "live comparison unreadable: "+d.LiveError)
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", d.Package, strings.Join(files, ", ")))
	}
	out := fmt.Sprintf("%d package(s) scanned", r.Scanned)
	if len(parts) > 0 {
		out += "; drift: " + strings.Join(parts, "; ")
	}
	if len(r.Errors) > 0 {
		out += "; unreadable: " + strings.Join(r.Errors, "; ")
	}
	if r.LiveSkipped != "" {
		out += "; live comparison skipped: " + r.LiveSkipped
	}
	return out + scanNotes(r)
}

// driftScanView is the schedule plus the latest scan, so surfaces show
// both from one read. LastScan is null before the first scan.
type driftScanView struct {
	catalog.ScanSchedule
	LastScan *catalog.ScanResult `json:"last_scan"`
}

// MountDriftScan serves the periodic scan schedule and latest result
// (S261): GET reads, POST saves the owner's cadence.
func (s *Server) MountDriftScan(st *store.Store, now func() time.Time) {
	view := func(w http.ResponseWriter, schedule catalog.ScanSchedule) {
		out := driftScanView{ScanSchedule: schedule}
		latest, ok, err := catalog.LatestScan(st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if ok {
			out.LastScan = &latest
		}
		writeJSON(w, out)
	}
	s.mux.HandleFunc("GET /api/v1/drift/schedule", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := catalog.GetScanSchedule(st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		view(w, schedule)
	})
	s.mux.HandleFunc("POST /api/v1/drift/schedule", func(w http.ResponseWriter, r *http.Request) {
		op, ok := requireOperator(w, r)
		if !ok {
			return
		}
		// Both fields are required: a body that omits enabled must not
		// silently switch scans off.
		var body struct {
			IntervalSeconds *int64 `json:"interval_seconds"`
			Enabled         *bool  `json:"enabled"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.IntervalSeconds == nil || body.Enabled == nil {
			http.Error(w, "interval_seconds and enabled are both required", http.StatusBadRequest)
			return
		}
		schedule, err := catalog.SaveScanSchedule(st, *body.IntervalSeconds, *body.Enabled, op.Session.Username, now())
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, catalog.ErrInvalidScanSchedule) {
				status = http.StatusUnprocessableEntity
			}
			http.Error(w, err.Error(), status)
			return
		}
		if _, err := audit.New(st).Record(audit.Entry{
			Actor: op.Session.Username, Action: "drift.schedule", Outcome: audit.OutcomeOK,
			Detail: fmt.Sprintf("interval %ds enabled=%v", schedule.IntervalSeconds, schedule.Enabled),
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		view(w, schedule)
	})
}

// driftDoctorCheck reports the latest scan time and drift state (S261).
// Drift or unreadable packages warn; never scanned is informational.
func driftDoctorCheck(st *store.Store, now func() time.Time) func() doctor.Check {
	return func() doctor.Check {
		const name = "drift-scan"
		schedule, err := catalog.GetScanSchedule(st)
		if err != nil {
			return doctor.Check{Name: name, Status: doctor.StatusWarn, Detail: "cannot read drift scan schedule: " + err.Error()}
		}
		latest, ok, err := catalog.LatestScan(st)
		if err != nil {
			return doctor.Check{Name: name, Status: doctor.StatusWarn, Detail: "cannot read latest drift scan: " + err.Error()}
		}
		switch {
		case !ok && !schedule.Enabled:
			return doctor.Check{Name: name, Status: doctor.StatusOK, Detail: "scheduled scans are off and no scan has run"}
		case !ok:
			return doctor.Check{Name: name, Status: doctor.StatusOK, Detail: "no scan has run yet"}
		}
		when := latest.ScannedAt.UTC().Format(time.RFC3339)
		age := "age unknown"
		if d := now().Sub(latest.ScannedAt); d >= 0 {
			age = "age " + d.Round(time.Second).String()
		}
		if latest.Clean() {
			note := ""
			if latest.LiveSkipped != "" {
				note = "; live comparison skipped: " + latest.LiveSkipped
			}
			note += scanNotes(latest)
			return doctor.Check{Name: name, Status: doctor.StatusOK,
				Detail: fmt.Sprintf("clean at %s, %s (%d package(s); resolved packages are dropped until the next scan)%s", when, age, latest.Scanned, note)}
		}
		return doctor.Check{Name: name, Status: doctor.StatusWarn,
			Detail: fmt.Sprintf("at %s, %s: %s; review with homesteadctl drift review <package>", when, age, scanSummary(latest))}
	}
}
