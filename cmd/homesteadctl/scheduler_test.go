package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchedulerJobs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scheduler/jobs" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"id":"backup:jellyfin","interval_seconds":86400,"enabled":true,"last_run_at":"2026-10-11T00:00:00Z","next_due":"2026-10-12T00:00:00Z","last_operation_id":5,"running":false}]`))
	}))
	defer ts.Close()
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "scheduler", "jobs"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "backup:jellyfin every 86400s") || !strings.Contains(stdout, "next=2026-10-12T00:00:00Z") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, _ = invoke(t, []string{"--server", ts.URL, "scheduler"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}
