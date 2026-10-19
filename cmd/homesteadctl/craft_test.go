package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func craftStub(t *testing.T, verdict string) (string, *[]string) {
	t.Helper()
	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/craft":
			_, _ = w.Write([]byte(`{"images":[{"app":"w","version":"1.0.0","tag":"homestead/craft-w:1.0.0","image_id":"sha256:aa","counter":3,"present":true,"live_id":"sha256:bb","match":false}],
				"verdicts":[{"app":"w","version":"1.0.0","state":"failed","refusal":"artifact-digest","detail":"mismatch"}],"baselines":{},"manifest_path":"/m.yaml","context_dir":"/c"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/craft/plan":
			bodies = append(bodies, string(raw))
			if verdict == "counter" {
				_, _ = w.Write([]byte(`{"plan":{},"verdict":{"app":"w","state":"failed","refusal":"counter","detail":"older"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"plan":{"app":"w","version":"1.0.0","counter":3,"tag":"homestead/craft-w:1.0.0","key_id":"K","artifact_name":"w.tar.gz","artifact_size":5,"artifact_sha256":"ab","source":"local","context_digest":"sha256:cd","disk_required":536870912},"verdict":{"app":"w","state":"verified"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/craft/build":
			bodies = append(bodies, string(raw))
			_, _ = w.Write([]byte(`{"id":12}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/craft/apps":
			_, _ = w.Write([]byte(`{"apps":[{"name":"gridcraft","title":"GridCraft","blurb":"Spreadsheet that runs in your browser.","version":"0.7.0","status":"installed","url":"https://gridcraft.lan/"},{"name":"pdfcraft","title":"PdfCraft","version":"1.0.0","status":"installed","url":"","url_reason":"No edge route serves this app yet."}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/craft/trust-keys":
			_, _ = w.Write([]byte(`[{"key_id":"CCCC","source":"embedded","label":"HAI craft","placeholder":true}]`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/craft/trust-keys/CCCC":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL, &bodies
}

func TestCraftStatusShowsDigestComparison(t *testing.T) {
	url, _ := craftStub(t, "")
	code, stdout, _ := invoke(t, []string{"--server", url, "craft", "status"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"homestead/craft-w:1.0.0", "DIFFERS from recorded digest (sha256:aa)", "artifact-digest check"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestCraftPlanAndBuild(t *testing.T) {
	url, bodies := craftStub(t, "")
	code, stdout, _ := invoke(t, []string{"--server", url, "craft", "plan", "w", "--artifact", "/srv/w.tar.gz", "--manifest", "/srv/m.yaml"})
	if code != exitOK || !strings.Contains(stdout, "verified with key K") || !strings.Contains(stdout, "512 MiB") {
		t.Fatalf("plan exit=%d %s", code, stdout)
	}
	if !strings.Contains((*bodies)[0], `"artifact":"/srv/w.tar.gz"`) || !strings.Contains((*bodies)[0], `"manifest":"/srv/m.yaml"`) {
		t.Fatalf("body = %s", (*bodies)[0])
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "craft", "build", "w", "--allow-older"})
	if code != exitOK || !strings.Contains(stdout, "operation: 12") || !strings.Contains((*bodies)[1], `"allow_older":true`) {
		t.Fatalf("build exit=%d %s body=%s", code, stdout, (*bodies)[1])
	}
	if code, _, _ := invoke(t, []string{"--server", url, "craft", "build"}); code != exitUsage {
		t.Fatalf("missing app exit = %d", code)
	}
}

func TestCraftPlanRefusalNamesTheCheckAndOffersOverrideOnlyForCounter(t *testing.T) {
	url, _ := craftStub(t, "counter")
	code, _, stderr := invoke(t, []string{"--server", url, "craft", "plan", "w"})
	if code != exitError || !strings.Contains(stderr, "counter check") || !strings.Contains(stderr, "--allow-older") {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
}

func TestCraftTrustUsesCraftRoutes(t *testing.T) {
	url, _ := craftStub(t, "")
	code, stdout, _ := invoke(t, []string{"--server", url, "craft", "trust", "list"})
	if code != exitOK || !strings.Contains(stdout, "CCCC") || !strings.Contains(stdout, "PLACEHOLDER") {
		t.Fatalf("exit=%d %s", code, stdout)
	}
	if code, out, _ := invoke(t, []string{"--server", url, "craft", "trust", "remove", "CCCC"}); code != exitOK || !strings.Contains(out, "removed") {
		t.Fatalf("remove exit=%d %s", code, out)
	}
}

func TestCraftAppsPrintsUrlsAndReasons(t *testing.T) {
	url, _ := craftStub(t, "")
	code, stdout, _ := invoke(t, []string{"--server", url, "craft", "apps"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"gridcraft  GridCraft  0.7.0  installed  https://gridcraft.lan/", "    Spreadsheet that runs in your browser.", "pdfcraft  PdfCraft  1.0.0  installed  (no URL) No edge route serves this app yet."} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestCraftAppsEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"apps":[]}`))
	}))
	defer ts.Close()
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "craft", "apps"})
	if code != exitOK || !strings.Contains(stdout, "no craft apps installed") {
		t.Fatalf("exit=%d %s", code, stdout)
	}
}
