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

func TestDoctorGroupedWithFix(t *testing.T) {
	url := doctorStub(t, `{"overall":"fail","live":false,"checks":[
{"name":"memory","status":"ok","detail":"8 GiB","group":"System"},
{"name":"backup-identity","status":"fail","detail":"not saved","group":"Storage","why":"Without it backups cannot be restored.","fix":{"text":"Save the identity.","link":"/backups","command":"homesteadctl backup identity ack"}}]}`)
	code, stdout, _ := invoke(t, []string{"--server", url, "doctor"})
	if code != exitError {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"== System", "== Storage", "why: Without it", "fix: Save the identity.", "page: /backups",
		"run: homesteadctl backup identity ack", "live checks not run: add --live", "overall: fail"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in %q", want, stdout)
		}
	}
	if strings.Index(stdout, "== System") > strings.Index(stdout, "== Storage") {
		t.Errorf("groups out of order: %q", stdout)
	}
}

func TestDoctorLiveFlag(t *testing.T) {
	var query string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"overall":"warn","live":true,"checks":[{"name":"tunnel-connector","status":"unknown","detail":"timed out","group":"Tunnel"}]}`))
	}))
	defer ts.Close()
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "doctor", "--live"})
	if code != exitOK || query != "live=1" {
		t.Fatalf("exit %d query %q", code, query)
	}
	if !strings.Contains(stdout, "== Tunnel") || strings.Contains(stdout, "live checks not run") {
		t.Fatalf("stdout = %q", stdout)
	}
	if _, _, _ = invoke(t, []string{"--server", ts.URL, "doctor"}); query != "" {
		t.Fatalf("plain doctor sent %q", query)
	}
}
