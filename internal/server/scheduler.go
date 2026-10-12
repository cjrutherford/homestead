package server

import (
	"fmt"
	"net/http"
	"time"

	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/store"
)

// NewScheduler builds the daemon scheduler (S260, ADR-023) with the
// backup schedules registered. Later periodic jobs (drift, certificates)
// register more sources on the returned value. The daemon runs it; the
// status route only reads it.
func NewScheduler(st *store.Store, catalogDir string, opts ...scheduler.Option) *scheduler.Scheduler {
	s := scheduler.New(st, opts...)
	s.Register("backup", backupSource(st, catalogDir, s.Now))
	s.Register("drift", driftSource(st, s.Now))
	return s
}

// backupSource turns persisted backup schedules into scheduler jobs.
// Jobs assemble at fire time from live storage and catalog facts, via
// the same backupJob assembly pre-update backups use. Database
// passwords are never stored, so scheduled postgres backups fail with a
// recorded error until credential custody lands (docs/DEFERRED.md).
func backupSource(st *store.Store, catalogDir string, now func() time.Time) scheduler.Source {
	return scheduler.SourceFunc(func() ([]scheduler.Job, error) {
		schedules, err := backup.NewSchedules(st).List()
		if err != nil {
			return nil, err
		}
		jobs := make([]scheduler.Job, 0, len(schedules))
		for _, schedule := range schedules {
			jobs = append(jobs, scheduler.Job{
				ID:       "backup:" + schedule.Package,
				Interval: time.Duration(schedule.IntervalSeconds) * time.Second,
				Enabled:  schedule.Enabled,
				Since:    schedule.UpdatedAt,
				Build: func() (operation.Definition, error) {
					entries, err := catalog.Load(catalogDir)
					if err != nil {
						return operation.Definition{}, fmt.Errorf("catalog unavailable: %w", err)
					}
					manifest, err := entries.Get(schedule.Package)
					if err != nil {
						return operation.Definition{}, fmt.Errorf("unknown package %q", schedule.Package)
					}
					job, err := backupJob(st, manifest, "")
					if err != nil {
						return operation.Definition{}, err
					}
					return backup.JobDefinition(job, now)
				},
			})
		}
		return jobs, nil
	})
}

// MountScheduler serves scheduler state (S260): declared jobs with
// interval, last run, next due, and the operation of the last run.
func (s *Server) MountScheduler(sched *scheduler.Scheduler) {
	s.mux.HandleFunc("GET /api/v1/scheduler/jobs", func(w http.ResponseWriter, r *http.Request) {
		jobs, err := sched.Status()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, jobs)
	})
}
