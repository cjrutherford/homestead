package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func doctorStub(t *testing.T, body string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/doctor" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestDoctorOK(t *testing.T) {
	url := doctorStub(t, `{"overall":"ok","checks":[{"name":"memory","status":"ok","detail":"8 GiB"}]}`)
	code, stdout, _ := invoke(t, []string{"--server", url, "doctor"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "memory: ok") || !strings.Contains(stdout, "overall: ok") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestDoctorFailExitsOne(t *testing.T) {
	url := doctorStub(t, `{"overall":"fail","checks":[{"name":"memory","status":"fail","detail":"512 MiB"}]}`)
	code, stdout, _ := invoke(t, []string{"--server", url, "doctor"})
	if code != exitError {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stdout, "overall: fail") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestDoctorWarnExitsZero(t *testing.T) {
	url := doctorStub(t, `{"overall":"warn","checks":[{"name":"cpu","status":"warn","detail":"1 core"}]}`)
	code, _, _ := invoke(t, []string{"--server", url, "doctor"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
}
