package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func operationsStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/operations/7" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":7,"type":"test.op","status":"succeeded","steps":[]}`))
		case r.URL.Path == "/api/v1/operations/7/events":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "no flush", http.StatusInternalServerError)
				return
			}
			for _, status := range []string{"running", "succeeded"} {
				fmt.Fprintf(w, "event: state\ndata: {\"id\":7,\"type\":\"test.op\",\"status\":%q,\"steps\":[]}\n\n", status)
				flusher.Flush()
			}
		case r.URL.Path == "/api/v1/operations/8/events":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			fmt.Fprint(w, "event: state\ndata: {\"id\":8,\"type\":\"test.op\",\"status\":\"failed\",\"steps\":[]}\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestOperationsGet(t *testing.T) {
	url := operationsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "operations", "get", "7"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "test.op") || !strings.Contains(stdout, "succeeded") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestOperationsGetFailures(t *testing.T) {
	url := operationsStub(t)
	code, _, stderr := invoke(t, []string{"--server", url, "operations", "get", "999"})
	if code != exitError || !strings.Contains(stderr, "operation not found") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	for _, args := range [][]string{
		{"operations"},
		{"operations", "bogus"},
		{"operations", "get"},
		{"operations", "get", "abc"},
	} {
		full := append([]string{"--server", url}, args...)
		if code, _, _ := invoke(t, full); code != exitUsage {
			t.Fatalf("args %q exit = %d, want 2", args, code)
		}
	}
}

func TestOperationsFollow(t *testing.T) {
	url := operationsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "operations", "follow", "7"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "running") || !strings.Contains(stdout, "succeeded") {
		t.Fatalf("stdout = %q, want transitions", stdout)
	}
}

func TestOperationsFollowFailed(t *testing.T) {
	url := operationsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "operations", "follow", "8"})
	if code != exitError {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stdout, "failed") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestOperationsFollowMissing(t *testing.T) {
	url := operationsStub(t)
	code, _, stderr := invoke(t, []string{"--server", url, "operations", "follow", "999"})
	if code != exitError || !strings.Contains(stderr, "operation not found") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestOperationsListStubbed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/operations" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":2,"type":"b.two","status":"running","steps":[]},{"id":1,"type":"a.one","status":"succeeded","steps":[]}]`))
	}))
	defer ts.Close()
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "operations", "list"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "b.two running") || !strings.Contains(stdout, "a.one succeeded") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, _ = invoke(t, []string{"--server", ts.URL, "operations", "list", "5"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	code, _, _ = invoke(t, []string{"--server", ts.URL, "operations", "list", "bogus"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
}
