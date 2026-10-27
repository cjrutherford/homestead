package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// S315 (ADR-044 §5): `apps move` reviews and approves moving an app to
// its path; a path app's public plan prints its URL and no DNS change.
func TestAppsMoveAndPathPublic(t *testing.T) {
	var bodies []map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies = append(bodies, body)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/catalog/nextcloud/move":
			_, _ = w.Write([]byte(`{"app":"nextcloud","offered":true,"from":"https://nextcloud.lan/","path":"/apps/nextcloud/",
				"to":["https://homestead.lan/apps/nextcloud/","https://homestead.local/apps/nextcloud/"]}`))
		case "POST /api/v1/catalog/nextcloud/move/plan":
			_, _ = w.Write([]byte(`{"app":"nextcloud","route":"nextcloud.lan","from":"https://nextcloud.lan/","path":"/apps/nextcloud/",
				"to":["https://homestead.lan/apps/nextcloud/"],"strip_prefix":true,"redirects":["/.well-known/caldav -> /apps/nextcloud/remote.php/dav/"],
				"app_settings":["nextcloud: OVERWRITEWEBROOT=/apps/nextcloud"],"sign_in":["re-plan it"],"warnings":["stops answering"],"digest":"m1"}`))
		case "POST /api/v1/catalog/nextcloud/move/apply":
			_, _ = w.Write([]byte(`{"id":61}`))
		case "GET /api/v1/catalog/nextcloud/public":
			_, _ = w.Write([]byte(`{"app":"nextcloud","available":true,"tunnel":"homestead","routes":[{"route":"nextcloud.lan","public":[],"path":"/apps/nextcloud/"}],
				"proposed":"homestead.example.com","proposed_url":"https://homestead.example.com/apps/nextcloud/"}`))
		case "POST /api/v1/catalog/nextcloud/public/plan":
			_, _ = w.Write([]byte(`{"app":"nextcloud","action":"add","hostname":"homestead.example.com","route":"nextcloud.lan","path":"/apps/nextcloud/",
				"url":"https://homestead.example.com/apps/nextcloud/","tunnel":"homestead","dns":[],"connector_before":["homestead.example.com"],
				"connector_after":["homestead.example.com"],"warnings":["No DNS record changes."],"digest":"pp"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "apps", "move", "status", "nextcloud"})
	if code != exitOK || !strings.Contains(stdout, "offered from https://nextcloud.lan/ to https://homestead.lan/apps/nextcloud/, https://homestead.local/apps/nextcloud/") {
		t.Fatalf("status: %d %s", code, stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", ts.URL, "apps", "move", "plan", "nextcloud"})
	for _, want := range []string{"plan: move nextcloud from https://nextcloud.lan/ to /apps/nextcloud/", "address: https://homestead.lan/apps/nextcloud/",
		"path prefix: taken off", "redirect: /.well-known/caldav", "app setting: nextcloud: OVERWRITEWEBROOT", "sign-in: re-plan it",
		"warning: stops answering", "approve: homesteadctl apps move apply nextcloud --digest m1"} {
		if code != exitOK || !strings.Contains(stdout, want) {
			t.Fatalf("plan missing %q: %s", want, stdout)
		}
	}
	code, stdout, _ = invoke(t, []string{"--server", ts.URL, "apps", "move", "apply", "nextcloud", "--digest", "m1"})
	if code != exitOK || !strings.Contains(stdout, "operation: 61") || bodies[len(bodies)-1]["digest"] != "m1" {
		t.Fatalf("apply: %d %s %v", code, stdout, bodies)
	}
	for _, args := range [][]string{{"apps", "move", "apply", "nextcloud"}, {"apps", "move", "plan", "nextcloud", "--digest", "x"},
		{"apps", "move", "nope", "nextcloud"}, {"apps", "move", "status"}} {
		if code, _, _ := invoke(t, append([]string{"--server", ts.URL}, args...)); code != exitUsage {
			t.Fatalf("%v: exit = %d", args, code)
		}
	}
	code, stdout, _ = invoke(t, []string{"--server", ts.URL, "apps", "public", "status", "nextcloud"})
	if code != exitOK || !strings.Contains(stdout, "public: none (proposed https://homestead.example.com/apps/nextcloud/)") {
		t.Fatalf("public status: %s", stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", ts.URL, "apps", "public", "plan", "nextcloud"})
	if code != exitOK || !strings.Contains(stdout, "plan: add https://homestead.example.com/apps/nextcloud/ for nextcloud at /apps/nextcloud/") ||
		!strings.Contains(stdout, "approve: homesteadctl apps public apply nextcloud --digest pp") {
		t.Fatalf("public plan: %s", stdout)
	}
}
