package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppsReadLinksStatusPlanApprove(t *testing.T) {
	var bodies []map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			bodies = append(bodies, b)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/apps/tasks/read-links":
			_, _ = w.Write([]byte(`{"app":"tasks","available":true,"exposed":true,"origin":"https://home.example.org","patterns":["https://home.example.org/apps/tasks/_homestead/read/<token>"],"links":2,"sign_in":true}`))
		case "POST /api/v1/apps/tasks/read-links/plan":
			_, _ = w.Write([]byte(`{"app":"tasks","action":"expose","origin":"https://home.example.org","route":"home.lan","patterns":["/apps/tasks/_homestead/read/<token>"],"refused":["the app"],"links":2,"expiry_cap_days":365,"warnings":["Anyone with a link can read."],"digest":"rl1"}`))
		case "POST /api/v1/apps/tasks/read-links/approve":
			_, _ = w.Write([]byte(`{"id":61}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	code, out, _ := invoke(t, []string{"--server", ts.URL, "apps", "read-links", "status", "tasks"})
	if code != exitOK || !strings.Contains(out, "public at https://home.example.org (2 links)") {
		t.Fatalf("status: %d %s", code, out)
	}
	code, out, _ = invoke(t, []string{"--server", ts.URL, "apps", "read-links", "plan", "tasks"})
	for _, want := range []string{"action: expose", "stays closed: the app", "warning: Anyone with a link", "digest: rl1"} {
		if code != exitOK || !strings.Contains(out, want) {
			t.Fatalf("plan lacks %q: %d %s", want, code, out)
		}
	}
	if len(bodies) != 1 || bodies[0]["enabled"] != true || bodies[0]["digest"] != nil {
		t.Fatalf("plan body %v", bodies)
	}
	if code, _, _ = invoke(t, []string{"--server", ts.URL, "apps", "read-links", "approve", "tasks"}); code != exitUsage {
		t.Fatalf("approve without a digest: %d", code)
	}
	code, out, _ = invoke(t, []string{"--server", ts.URL, "apps", "read-links", "approve", "tasks", "--digest", "rl1", "--off"})
	if code != exitOK || !strings.Contains(out, "operation: 61") {
		t.Fatalf("approve: %d %s", code, out)
	}
	if b := bodies[len(bodies)-1]; b["digest"] != "rl1" || b["enabled"] != false {
		t.Fatalf("approve body %v", b)
	}
}
