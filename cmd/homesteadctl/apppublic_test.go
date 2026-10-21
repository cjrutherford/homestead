package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func appPublicStub(t *testing.T, bodies *[]map[string]any) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			*bodies = append(*bodies, body)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/jellyfin/public":
			_, _ = w.Write([]byte(`{"app":"jellyfin","available":true,"tunnel":"homestead","routes":[{"route":"jellyfin.lan","public":[]}],"proposed":"jellyfin.example.com","sign_in":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/public/plan":
			_, _ = w.Write([]byte(`{"app":"jellyfin","action":"add","hostname":"jellyfin.example.com","route":"jellyfin.lan","tunnel":"homestead","zone":"example.com",
				"dns":[{"name":"jellyfin.example.com","type":"CNAME","content":"tid.cfargotunnel.com","proxied":true,"action":"create","replaces":[]}],
				"connector_before":["auth.example.com"],"connector_after":["auth.example.com","jellyfin.example.com"],
				"warnings":["Anyone on the internet can reach Jellyfin."],"digest":"p1g"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/public/apply":
			_, _ = w.Write([]byte(`{"id":51}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestAppsPublicStatusPlanApply(t *testing.T) {
	var bodies []map[string]any
	url := appPublicStub(t, &bodies)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "public", "status", "jellyfin"})
	if code != exitOK || !strings.Contains(stdout, "public: none (proposed jellyfin.example.com)") {
		t.Fatalf("status: %d %s", code, stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "apps", "public", "plan", "jellyfin"})
	for _, want := range []string{"plan: add public name jellyfin.example.com for jellyfin (route jellyfin.lan, tunnel homestead)",
		"dns create: CNAME jellyfin.example.com -> tid.cfargotunnel.com", "connector after: auth.example.com, jellyfin.example.com",
		"warning: Anyone on the internet", "apps public apply jellyfin --hostname jellyfin.example.com --route jellyfin.lan --digest p1g"} {
		if code != exitOK || !strings.Contains(stdout, want) {
			t.Fatalf("plan missing %q: %d %s", want, code, stdout)
		}
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "apps", "public", "apply", "jellyfin", "--off", "--hostname", "jellyfin.example.com", "--digest", "p1g"})
	if code != exitOK || !strings.Contains(stdout, "operation: 51") {
		t.Fatalf("apply: %d %s", code, stdout)
	}
	last := bodies[len(bodies)-1]
	if last["enabled"] != false || last["digest"] != "p1g" || last["hostname"] != "jellyfin.example.com" {
		t.Fatalf("apply body = %v", last)
	}
	for _, args := range [][]string{
		{"apps", "public", "apply", "jellyfin"},
		{"apps", "public", "plan", "jellyfin", "--digest", "x"},
		{"apps", "public", "status"},
		{"apps", "public", "status", "jellyfin", "--off"},
		{"apps", "public", "nope", "jellyfin"},
	} {
		if code, _, _ := invoke(t, append([]string{"--server", url}, args...)); code != exitUsage {
			t.Fatalf("%v: exit = %d, want usage", args, code)
		}
	}
}
