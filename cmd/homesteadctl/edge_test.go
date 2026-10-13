package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func edgeStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/edge/deploy":
			_, _ = w.Write([]byte(`{"id":21}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/edge/routes":
			_, _ = w.Write([]byte(`{"routes":[{"Host":"app.lan","Target":{"Host":"app","Port":8080},"Exposure":"local","TLS":"internal","Health":{"Path":"","IntervalSeconds":0},"Policy":null}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/edge/routes":
			_, _ = w.Write([]byte(`{"id":22}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestEdgeDeploy(t *testing.T) {
	url := edgeStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "edge", "deploy"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "operation: 21") {
		t.Fatalf("stdout = %q", stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "edge"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestEdgeRoutesSet(t *testing.T) {
	url := edgeStub(t)
	file := writeTemp(t, "routes.json", `{"routes": [{"Host": "app.lan", "Target": {"Host": "app", "Port": 8080}, "Exposure": "local", "TLS": "internal", "Health": {"Path": "/", "IntervalSeconds": 30}}]}`)
	code, stdout, _ := invoke(t, []string{"--server", url, "edge", "routes", "set", "-f", file})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "operation: 22") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, _ = invoke(t, []string{"--server", url, "edge", "routes", "set"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
	bad := writeTemp(t, "bad.txt", `{"routes": []}`)
	code, _, _ = invoke(t, []string{"--server", url, "edge", "routes", "set", "-f", bad})
	if code != exitError {
		t.Fatalf("exit = %d, want error", code)
	}
}

func TestEdgeRoutesGet(t *testing.T) {
	url := edgeStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "edge", "routes", "get"})
	if code != exitOK || !strings.Contains(stdout, `"Host": "app.lan"`) {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
}
