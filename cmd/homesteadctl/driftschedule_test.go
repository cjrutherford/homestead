package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDriftScheduleShowAndSave(t *testing.T) {
	var saved map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/drift/schedule" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &saved)
			_, _ = w.Write([]byte(`{"interval_seconds":21600,"enabled":true,"last_scan":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"interval_seconds":86400,"enabled":true,"last_scan":{"scanned_at":"2026-10-11T13:00:00Z","scanned":2,"drifted":[{"package":"demo","files":[{"path":"compose.yaml","kind":"modified"}]}],"errors":[]}}`))
	}))
	t.Cleanup(ts.Close)

	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "drift", "schedule"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"every 86400s enabled=true", "last scan: 2026-10-11T13:00:00Z, 2 package(s)", "drift: demo compose.yaml (modified)"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}

	code, stdout, _ = invoke(t, []string{"--server", ts.URL, "drift", "schedule", "--interval", "6h"})
	if code != exitOK || saved["interval_seconds"] != float64(21600) || saved["enabled"] != true {
		t.Fatalf("exit = %d saved = %v", code, saved)
	}
	if !strings.Contains(stdout, "last scan: never") {
		t.Fatalf("stdout = %s", stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "drift", "schedule", "--interval", "soon"}); code != exitUsage {
		t.Fatalf("bad interval exit = %d", code)
	}
	// Below the 5m API minimum is a local usage error: nothing is saved.
	saved = nil
	code, _, stderr := invoke(t, []string{"--server", ts.URL, "drift", "schedule", "--interval", "299s"})
	if code != exitUsage || saved != nil || !strings.Contains(stderr, "minimum is 5m0s") {
		t.Fatalf("299s exit = %d saved = %v stderr = %q", code, saved, stderr)
	}
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "drift", "schedule", "--interval", "5m"}); code != exitOK || saved["interval_seconds"] != float64(300) {
		t.Fatalf("5m exit = %d saved = %v", code, saved)
	}
}

func TestDriftLive(t *testing.T) {
	body := `{"checked":true,"findings":[{"service":"web","container":"demo-web","kind":"image","detail":"running nginx:1.26, compose declares nginx:1.27","resolution":"restore"}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/catalog/demo/drift/live" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "drift", "live", "demo"})
	if code != exitOK || !strings.Contains(stdout, "image demo-web: running nginx:1.26, compose declares nginx:1.27 [restore]") {
		t.Fatalf("exit = %d stdout = %s", code, stdout)
	}
	body = `{"checked":true,"findings":[]}`
	if _, stdout, _ = invoke(t, []string{"--server", ts.URL, "drift", "live", "demo"}); !strings.Contains(stdout, "match generated compose") {
		t.Fatalf("clean stdout = %s", stdout)
	}
	body = `{"checked":false,"findings":[]}`
	if _, stdout, _ = invoke(t, []string{"--server", ts.URL, "drift", "live", "demo"}); !strings.Contains(stdout, "unreachable") {
		t.Fatalf("unreachable stdout = %s", stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "drift", "live"}); code != exitUsage {
		t.Fatalf("usage exit = %d", code)
	}
}

func TestDriftScheduleShowsLiveFindings(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"interval_seconds":86400,"enabled":true,"last_scan":{"scanned_at":"2026-10-11T13:00:00Z","scanned":1,"drifted":[{"package":"demo","files":[],"live":[{"service":"db","kind":"missing","detail":"no container exists for this service","resolution":"restore"}]}],"errors":[],"live_skipped":"Docker engine unreachable"}}`))
	}))
	t.Cleanup(ts.Close)
	_, stdout, _ := invoke(t, []string{"--server", ts.URL, "drift", "schedule"})
	for _, want := range []string{"live drift: demo db missing: no container exists for this service [restore]", "live comparison skipped: Docker engine unreachable"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}
