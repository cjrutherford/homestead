package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"homestead/internal/audit"
	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// policySkip is an installed app the nightly policy leaves out, and why.
type policySkip struct {
	Package string `json:"package"`
	Reason  string `json:"reason"`
}

// policyCoverage is what the nightly policy covers right now: every
// stateful installed catalog app without its own schedule (ADR-039 §2).
type policyCoverage struct {
	Covered []string     `json:"covered"`
	Skipped []policySkip `json:"skipped"`
}

func coverage(st *store.Store, catalogDir string) (policyCoverage, error) {
	out := policyCoverage{Covered: []string{}, Skipped: []policySkip{}}
	stacks, err := catalog.InstalledStacks(st)
	if err != nil {
		return out, err
	}
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return out, fmt.Errorf("catalog unavailable: %w", err)
	}
	schedules := backup.NewSchedules(st)
	for _, name := range stacks {
		manifest, err := entries.Get(name)
		if err != nil {
			continue // not a catalog app (the edge, for one)
		}
		if manifest.Stateless() {
			out.Skipped = append(out.Skipped, policySkip{name, "Keeps no data on the appliance."})
			continue
		}
		if schedule, err := schedules.Get(name); err == nil {
			reason := fmt.Sprintf("Has its own schedule (every %s).", time.Duration(schedule.IntervalSeconds)*time.Second)
			if !schedule.Enabled {
				reason = "Has its own schedule, which is turned off."
			}
			out.Skipped = append(out.Skipped, policySkip{name, reason})
			continue
		} else if !errors.Is(err, backup.ErrScheduleNotFound) {
			return out, err
		}
		out.Covered = append(out.Covered, name)
	}
	return out, nil
}

// installedSince is when a stack's generated files were first recorded,
// so an app installed after the policy waits for the next policy time
// instead of running at the next tick.
func installedSince(st *store.Store, name string) time.Time {
	var first string
	_ = st.DB().QueryRow(`SELECT COALESCE(MIN(updated_at), '') FROM generated_fingerprints WHERE stack = ?`, name).Scan(&first)
	t, _ := time.Parse(time.RFC3339Nano, first)
	return t
}

// policyJobs turns the nightly policy into wall-clock scheduler jobs, one
// per covered app. They share the "backup:<app>" ID of a per-app schedule,
// so the last run carries over when an app moves between the two.
func policyJobs(st *store.Store, catalogDir string, secrets catalog.SecretStore, now func() time.Time) ([]scheduler.Job, error) {
	policy, ok, err := backup.NewPolicies(st).Get()
	if err != nil || !ok {
		return nil, err
	}
	hour, minute, err := policy.Clock()
	if err != nil {
		return nil, err
	}
	cov, err := coverage(st, catalogDir)
	if err != nil {
		return nil, err
	}
	at := &scheduler.Daily{Hour: hour, Minute: minute, Location: time.Local}
	jobs := make([]scheduler.Job, 0, len(cov.Covered))
	for _, name := range cov.Covered {
		since := policy.UpdatedAt
		if installed := installedSince(st, name); installed.After(since) {
			since = installed
		}
		jobs = append(jobs, scheduler.Job{
			ID: "backup:" + name, Interval: 24 * time.Hour, At: at,
			Enabled: policy.Enabled, Since: since,
			Build: func() (operation.Definition, error) {
				entries, err := catalog.Load(catalogDir)
				if err != nil {
					return operation.Definition{}, fmt.Errorf("catalog unavailable: %w", err)
				}
				manifest, err := entries.Get(name)
				if err != nil {
					return operation.Definition{}, fmt.Errorf("unknown package %q", name)
				}
				job, err := backupJob(st, manifest, secrets, "")
				if err != nil {
					return operation.Definition{}, err
				}
				return backup.JobDefinition(job, now)
			},
		})
	}
	return jobs, nil
}

// policyStatus is GET /api/v1/backups/policy.
type policyStatus struct {
	// Policy is the approved policy, absent until the first approval.
	Policy   *backup.Policy `json:"policy,omitempty"`
	Proposed backup.Policy  `json:"proposed"`
	policyCoverage
	TimeZone      string `json:"time_zone"`
	BackupStorage bool   `json:"backup_storage"`
}

// policyPlan is what approving would store; nothing changes until then.
type policyPlan struct {
	Policy backup.Policy `json:"policy"`
	policyCoverage
	TimeZone string   `json:"time_zone"`
	Changes  []string `json:"changes"`
	Digest   string   `json:"digest"`
}

func (p policyPlan) digest() string {
	visible := p
	visible.Digest = ""
	data, _ := json.Marshal(visible)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func backupStorageAssigned(st *store.Store) bool {
	_, err := storage.NewAssignments(st).Get(storage.ClassBackup)
	return err == nil
}

// errNoBackupStorage refuses a policy that turns backups on without a
// place to write them.
var errNoBackupStorage = errors.New("no disk holds the backup storage class: assign it on the Storage page first, then plan again")

func zoneName() string {
	name, _ := time.Now().Zone()
	if loc := time.Local.String(); loc != "" && loc != "Local" {
		return loc + " (" + name + ")"
	}
	return name
}

func buildPolicyPlan(st *store.Store, catalogDir string, want backup.Policy) (policyPlan, error) {
	want = want.Normalize()
	want.Actor, want.UpdatedAt = "", time.Time{}
	if err := want.Validate(); err != nil {
		return policyPlan{}, err
	}
	if want.Enabled && !backupStorageAssigned(st) {
		return policyPlan{}, errNoBackupStorage
	}
	cov, err := coverage(st, catalogDir)
	if err != nil {
		return policyPlan{}, err
	}
	current, ok, err := backup.NewPolicies(st).Get()
	if err != nil {
		return policyPlan{}, err
	}
	p := policyPlan{Policy: want, policyCoverage: cov, TimeZone: zoneName(), Changes: []string{}}
	switch {
	case ok && current.TimeOfDay == want.TimeOfDay && current.Retain == want.Retain && current.Enabled == want.Enabled:
		p.Changes = append(p.Changes, "Nothing changes: this is the current policy.")
	case !want.Enabled:
		p.Changes = append(p.Changes, "Turn nightly backups off. Apps with their own schedule keep it; existing backups stay.")
	default:
		p.Changes = append(p.Changes,
			fmt.Sprintf("Back up every app below each night at %s, keeping the newest %d runs of each.", want.TimeOfDay, want.Retain),
			"Apps installed later are covered too. An app that misses its time while the appliance is off is backed up once when it starts.")
		if ok && current.Enabled && current.Retain > want.Retain {
			p.Changes = append(p.Changes, fmt.Sprintf("Older runs beyond the newest %d are deleted at each app's next backup.", want.Retain))
		}
	}
	p.Digest = p.digest()
	return p, nil
}

// policyRequest is the plan and apply body.
type policyRequest struct {
	TimeOfDay string `json:"time_of_day"`
	Retain    int    `json:"retain"`
	Enabled   *bool  `json:"enabled"`
	Digest    string `json:"digest,omitempty"`
}

func (r policyRequest) policy() backup.Policy {
	p := backup.ProposedPolicy()
	if r.TimeOfDay != "" {
		p.TimeOfDay = r.TimeOfDay
	}
	if r.Retain != 0 {
		p.Retain = r.Retain
	}
	if r.Enabled != nil {
		p.Enabled = *r.Enabled
	}
	return p
}

func writePolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backup.ErrInvalidPolicy), errors.Is(err, errNoBackupStorage):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func readPolicyStatus(st *store.Store, catalogDir string) (policyStatus, error) {
	out := policyStatus{Proposed: backup.ProposedPolicy(), TimeZone: zoneName(), BackupStorage: backupStorageAssigned(st)}
	policy, ok, err := backup.NewPolicies(st).Get()
	if err != nil {
		return out, err
	}
	if ok {
		out.Policy = &policy
	}
	out.policyCoverage, err = coverage(st, catalogDir)
	return out, err
}

// MountBackupPolicy serves the nightly policy (S297): status, a
// read-only plan, and apply by digest, audited.
func (s *Server) MountBackupPolicy(st *store.Store, catalogDir string) {
	s.mux.HandleFunc("GET /api/v1/backups/policy", func(w http.ResponseWriter, _ *http.Request) {
		status, err := readPolicyStatus(st, catalogDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, status)
	})
	s.mux.HandleFunc("POST /api/v1/backups/policy/plan", func(w http.ResponseWriter, r *http.Request) {
		var req policyRequest
		if !decodeResolverRequest(w, r, &req) {
			return
		}
		plan, err := buildPolicyPlan(st, catalogDir, req.policy())
		if err != nil {
			writePolicyError(w, err)
			return
		}
		writeJSON(w, plan)
	})
	s.mux.HandleFunc("POST /api/v1/backups/policy/apply", func(w http.ResponseWriter, r *http.Request) {
		var req policyRequest
		if !decodeResolverRequest(w, r, &req) {
			return
		}
		plan, err := buildPolicyPlan(st, catalogDir, req.policy())
		if err != nil {
			writePolicyError(w, err)
			return
		}
		if req.Digest == "" || plan.Digest != req.Digest {
			http.Error(w, "the plan changed since it was reviewed; review it again", http.StatusConflict)
			return
		}
		actor := operatorActor(r)
		if _, err := backup.NewPolicies(st).Save(plan.Policy, actor); err != nil {
			writePolicyError(w, err)
			return
		}
		state := "off"
		if plan.Policy.Enabled {
			state = "on"
		}
		_, _ = audit.New(st).Record(audit.Entry{
			Actor: actor, Action: "backup.policy.set", Outcome: audit.OutcomeOK,
			Detail: fmt.Sprintf("nightly backups %s at %s, keep %d; covers %d apps", state, plan.Policy.TimeOfDay, plan.Policy.Retain, len(plan.Covered)),
		})
		status, err := readPolicyStatus(st, catalogDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, status)
	})
}
