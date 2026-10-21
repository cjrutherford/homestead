package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/backup"
	"homestead/internal/scheduler"
	"homestead/internal/store"
)

// policyRig is an appliance with a stateful app, a stateless app and a
// stateful app on its own schedule, all installed.
func policyRig(t *testing.T, backupStorage bool) (*store.Store, *Server, string) {
	t.Helper()
	st := statelessStore(t)
	dir := statelessCatalog(t)
	scheduled := strings.ReplaceAll(statefulEntry, "statefulapp", "scheduledapp")
	if err := os.WriteFile(filepath.Join(dir, "scheduledapp.yaml"), []byte(scheduled), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, stack := range []string{"caddy", "scheduledapp", "statefulapp", "statelessapp"} {
		if _, err := st.DB().Exec(`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES (?, 'compose.yaml', 'x', ?)`,
			stack, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := backup.NewSchedules(st).Save(backup.Schedule{Package: "scheduledapp", IntervalSeconds: 21600, Retain: 3, Enabled: true, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	if backupStorage {
		if _, err := st.DB().Exec(`INSERT INTO storage_assignments (class, mountpoint, actor, created_at) VALUES ('backup', '/mnt/backup', 'owner', ?)`,
			time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	srv := New("127.0.0.1:0")
	srv.MountBackupPolicy(st, dir)
	return st, srv, dir
}

func decodeAs[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

func TestBackupPolicyProposesAndCoversStatefulApps(t *testing.T) {
	_, srv, _ := policyRig(t, true)
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/backups/policy", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	status := decodeAs[policyStatus](t, rec.Body.Bytes())
	if status.Policy != nil || status.Proposed.TimeOfDay != "02:00" || status.Proposed.Retain != 7 || !status.Proposed.Enabled || !status.BackupStorage {
		t.Fatalf("status: %+v", status)
	}
	if strings.Join(status.Covered, ",") != "statefulapp" {
		t.Fatalf("covered: %v", status.Covered)
	}
	skipped := map[string]string{}
	for _, s := range status.Skipped {
		skipped[s.Package] = s.Reason
	}
	if !strings.Contains(skipped["statelessapp"], "no data") || !strings.Contains(skipped["scheduledapp"], "own schedule") || len(skipped) != 2 {
		t.Fatalf("skipped: %v", skipped)
	}
}

func TestBackupPolicyPlanApproveAndAudit(t *testing.T) {
	st, srv, _ := policyRig(t, true)
	body := map[string]any{"time_of_day": "3:30", "retain": 10, "enabled": true}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/policy/plan", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	plan := decodeAs[policyPlan](t, rec.Body.Bytes())
	if plan.Policy.TimeOfDay != "03:30" || plan.Digest == "" || len(plan.Changes) == 0 {
		t.Fatalf("plan: %+v", plan)
	}
	if _, ok, _ := backup.NewPolicies(st).Get(); ok {
		t.Fatal("planning stored a policy")
	}
	body["digest"] = "stale"
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/policy/apply", body); rec.Code != http.StatusConflict {
		t.Fatalf("stale digest: %d", rec.Code)
	}
	body["digest"] = plan.Digest
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/backups/policy/apply", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	status := decodeAs[policyStatus](t, rec.Body.Bytes())
	if status.Policy == nil || status.Policy.TimeOfDay != "03:30" || status.Policy.Retain != 10 {
		t.Fatalf("applied: %+v", status.Policy)
	}
	entries, _ := audit.New(st).List(10)
	if len(entries) == 0 || entries[0].Action != "backup.policy.set" || !strings.Contains(entries[0].Detail, "on at 03:30, keep 10; covers 1 apps") {
		t.Fatalf("audit: %+v", entries)
	}
	// The same plan again changes nothing.
	again := decodeAs[policyPlan](t, serveRequest(t, srv, http.MethodPost, "/api/v1/backups/policy/plan", map[string]any{"time_of_day": "03:30", "retain": 10, "enabled": true}).Body.Bytes())
	if !strings.Contains(again.Changes[0], "Nothing changes") {
		t.Fatalf("replan: %v", again.Changes)
	}
}

func TestBackupPolicyRefusals(t *testing.T) {
	_, srv, _ := policyRig(t, false)
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/policy/plan", map[string]any{"enabled": true})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Storage page") {
		t.Fatalf("no backup storage: %d %s", rec.Code, rec.Body)
	}
	// Turning it off needs no storage.
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/policy/plan", map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []map[string]any{{"time_of_day": "24:00", "enabled": false}, {"retain": -1, "enabled": false}, {"enabled": false, "extra": 1}} {
		rec := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/policy/plan", bad)
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Fatalf("%v: %d", bad, rec.Code)
		}
	}
}

func TestBackupPolicyBecomesNightlyJobs(t *testing.T) {
	st, _, dir := policyRig(t, true)
	if jobs, err := policyJobs(st, dir, nil, time.Now); err != nil || len(jobs) != 0 {
		t.Fatalf("before approval: %v %v", jobs, err)
	}
	if _, err := backup.NewPolicies(st).Save(backup.ProposedPolicy(), "owner"); err != nil {
		t.Fatal(err)
	}
	jobs, err := policyJobs(st, dir, nil, time.Now)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	job := jobs[0]
	if job.ID != "backup:statefulapp" || job.At == nil || job.At.String() != "02:00" || !job.Enabled {
		t.Fatalf("job: %+v", job)
	}
	// A schedule overrides the policy for its app; the IDs never clash.
	sched := NewScheduler(st, dir, nil)
	status, err := sched.Status()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]scheduler.Status{}
	for _, s := range status {
		byID[s.ID] = s
	}
	if byID["backup:statefulapp"].At != "02:00" || byID["backup:scheduledapp"].At != "" || byID["backup:scheduledapp"].IntervalSeconds != 21600 {
		t.Fatalf("scheduler status: %+v", status)
	}
	// A later install waits for the next policy time.
	if _, err := st.DB().Exec(`UPDATE generated_fingerprints SET updated_at = ? WHERE stack = 'statefulapp'`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	jobs, _ = policyJobs(st, dir, nil, time.Now)
	if !jobs[0].Since.After(time.Now()) {
		t.Fatalf("since: %s", jobs[0].Since)
	}
}

func TestBackupRetainFollowsScheduleThenPolicy(t *testing.T) {
	st, _, _ := policyRig(t, true)
	if got := backupRetain(st, "statefulapp"); got != defaultBackupRetain {
		t.Fatalf("no policy: %d", got)
	}
	policies := backup.NewPolicies(st)
	if _, err := policies.Save(backup.Policy{TimeOfDay: "02:00", Retain: 14, Enabled: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := backupRetain(st, "statefulapp"); got != 14 {
		t.Fatalf("policy: %d", got)
	}
	if got := backupRetain(st, "scheduledapp"); got != 3 {
		t.Fatalf("schedule wins: %d", got)
	}
	if _, err := policies.Save(backup.Policy{TimeOfDay: "02:00", Retain: 14, Enabled: false}, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := backupRetain(st, "statefulapp"); got != defaultBackupRetain {
		t.Fatalf("policy off: %d", got)
	}
}
