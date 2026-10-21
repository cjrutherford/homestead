package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func backupPolicyStub(t *testing.T, bodies *[]map[string]any) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			*bodies = append(*bodies, body)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/backups/policy":
			_, _ = w.Write([]byte(`{"proposed":{"time_of_day":"02:00","retain":7,"enabled":true},"covered":["jellyfin"],"skipped":[{"package":"wordcraft","reason":"Keeps no data on the appliance."}],"time_zone":"UTC","backup_storage":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/policy/plan":
			_, _ = w.Write([]byte(`{"policy":{"time_of_day":"03:30","retain":10,"enabled":true},"covered":["jellyfin"],"skipped":[],"time_zone":"UTC","changes":["Back up every app below each night at 03:30."],"digest":"abc"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/policy/apply":
			_, _ = w.Write([]byte(`{"policy":{"time_of_day":"03:30","retain":10,"enabled":true,"actor":"owner"},"proposed":{"time_of_day":"02:00","retain":7,"enabled":true},"covered":["jellyfin"],"skipped":[],"time_zone":"UTC","backup_storage":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestBackupPolicyStatusPlanApply(t *testing.T) {
	var bodies []map[string]any
	url := backupPolicyStub(t, &bodies)
	code, stdout, _ := invoke(t, []string{"--server", url, "backup", "policy"})
	if code != exitOK || !strings.Contains(stdout, "not approved; proposed on, nightly at 02:00, keep 7") ||
		!strings.Contains(stdout, "covers: jellyfin") || !strings.Contains(stdout, "leaves out wordcraft") ||
		!strings.Contains(stdout, "backup storage: not assigned") {
		t.Fatalf("status: %d %q", code, stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "backup", "policy", "plan", "--time", "3:30", "--retain", "10"})
	if code != exitOK || !strings.Contains(stdout, "digest: abc") ||
		!strings.Contains(stdout, "approve: homesteadctl backup policy apply --time 3:30 --retain 10 --digest abc") {
		t.Fatalf("plan: %d %q", code, stdout)
	}
	if _, has := bodies[0]["digest"]; has || bodies[0]["time_of_day"] != "3:30" || bodies[0]["enabled"] != true {
		t.Fatalf("plan body: %v", bodies[0])
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "backup", "policy", "apply", "--time", "3:30", "--retain", "10", "--digest", "abc"})
	if code != exitOK || !strings.Contains(stdout, "approved") || !strings.Contains(stdout, "approved by owner") {
		t.Fatalf("apply: %d %q", code, stdout)
	}
	if bodies[1]["digest"] != "abc" {
		t.Fatalf("apply body: %v", bodies[1])
	}
	code, _, _ = invoke(t, []string{"--server", url, "backup", "policy", "plan", "--off"})
	if code != exitOK || bodies[2]["enabled"] != false {
		t.Fatalf("off: %d %v", code, bodies[2])
	}
}

func TestBackupPolicyUsage(t *testing.T) {
	var bodies []map[string]any
	url := backupPolicyStub(t, &bodies)
	for _, args := range [][]string{
		{"backup", "policy", "apply"},
		{"backup", "policy", "plan", "--digest", "x"},
		{"backup", "policy", "nope"},
		{"backup", "policy", "plan", "--retain", "-1"},
	} {
		if code, _, _ := invoke(t, append([]string{"--server", url}, args...)); code != exitUsage {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
	if len(bodies) != 0 {
		t.Fatalf("usage errors reached the server: %v", bodies)
	}
}
