package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exposureStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/exposure/review":
			_, _ = w.Write([]byte(`{"Host":"app.example.com","From":"local","To":"public",
				"Assessment":{"Host":"app.example.com","Selected":"public","Recommended":"public","Aligned":true},
				"DNS":[],"Firewall":[],"Edge":{}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/exposure/approvals":
			_, _ = w.Write([]byte(`[{"host":"app.example.com","actor":"owner"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/exposure/approvals":
			_, _ = w.Write([]byte(`{"host":"app.example.com","actor":"owner"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/exposure/apply":
			_, _ = w.Write([]byte(`{"id":17}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestExposureReview(t *testing.T) {
	url := exposureStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "exposure", "review"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "app.example.com: local -> public") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestExposureApprovals(t *testing.T) {
	url := exposureStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "exposure", "approvals"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "app.example.com by owner") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestExposureApprove(t *testing.T) {
	url := exposureStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "exposure", "approve",
		"--actor", "owner", "--network", "203.0.113.0/24"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "approved app.example.com by owner") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, _ = invoke(t, []string{"--server", url, "exposure", "approve", "--actor", "owner"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "exposure", "approve"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestExposureApply(t *testing.T) {
	url := exposureStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "exposure", "apply", "app.example.com"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "operation: 17") {
		t.Fatalf("stdout = %q", stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "exposure", "apply"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "exposure", "bogus"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}
