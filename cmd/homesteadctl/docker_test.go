package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func dockerStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/docker/health" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reachable":true,"version":"29.1.2","api_version":"1.52","storage_driver":"overlayfs","containers":[{"id":"abc123def456","names":["web"],"image":"nginx:latest","state":"running","status":"Up"}],"images":[{"id":"sha256:1","repo_tags":["nginx:latest"],"size":188000000,"created":1700000000}],"networks":[{"id":"n1","name":"bridge","driver":"bridge","scope":"local"}]}`))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func dockerStubUnreachable(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reachable":false,"containers":[],"images":[],"networks":[]}`))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestDockerHealth(t *testing.T) {
	url := dockerStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "docker", "health"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"engine: 29.1.2", "containers: 1 (1 running)", "abc123def456 web", "images: 1", "networks: bridge"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestDockerHealthUnreachable(t *testing.T) {
	url := dockerStubUnreachable(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "docker", "health"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0 (unreachable is data)", code)
	}
	if !strings.Contains(stdout, "unreachable") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestDockerUsage(t *testing.T) {
	if code, _, _ := invoke(t, []string{"docker"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	if code, _, _ := invoke(t, []string{"docker", "bogus"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
}
