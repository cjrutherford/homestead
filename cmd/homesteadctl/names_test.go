package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func namesStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := `{"resolver":{"enabled":%s,"running":%s,"listening":["192.168.1.10:53"],"names":["homestead.lan","immich.lan"],
			"router_hint":"add a conditional forward for lan"},
			"apps":[{"host":"homestead.lan","names":["homestead.lan","homestead.local"]},{"host":"immich.lan","names":["immich.lan","immich.local"]}]}`
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/names":
			_, _ = w.Write([]byte(strings.Replace(strings.Replace(status, "%s", "false", 1), "%s", "false", 1)))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/names/resolver/plan":
			_, _ = w.Write([]byte(`{"enabled":true,"changes":["Answer DNS"],"listen":["192.168.1.10:53"],"names":["immich.lan"],
				"firewall":[{"name":"local-dns-udp-public-drop","detail":"drop udp/53 from everywhere else"}],
				"warnings":[],"router_hint":"add a conditional forward for lan","digest":"d1g"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/names/resolver/apply":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["digest"] != "d1g" || body["enabled"] != true {
				http.Error(w, "the plan changed since it was reviewed; review it again", http.StatusConflict)
				return
			}
			_, _ = w.Write([]byte(strings.Replace(strings.Replace(status, "%s", "true", 1), "%s", "true", 1)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestNamesStatusAndAppsNames(t *testing.T) {
	url := namesStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "names"})
	if code != exitOK || !strings.Contains(stdout, "immich.lan: immich.lan, immich.local") || !strings.Contains(stdout, ".lan resolver: off") {
		t.Fatalf("names = %d\n%s", code, stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "apps", "names"})
	if code != exitOK || !strings.Contains(stdout, "homestead.lan: homestead.lan, homestead.local") || strings.Contains(stdout, "resolver") {
		t.Fatalf("apps names = %d\n%s", code, stdout)
	}
}

func TestNamesResolverPlanApply(t *testing.T) {
	url := namesStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "names", "resolver", "plan", "--on"})
	if code != exitOK || !strings.Contains(stdout, "conditional forward") || !strings.Contains(stdout, "names resolver apply --on --digest d1g") ||
		!strings.Contains(stdout, "firewall local-dns-udp-public-drop") {
		t.Fatalf("plan = %d\n%s", code, stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "names", "resolver", "apply", "--on", "--digest", "d1g"})
	if code != exitOK || !strings.Contains(stdout, "on, answering on 192.168.1.10:53") || !strings.Contains(stdout, "firewall apply") {
		t.Fatalf("apply = %d\n%s", code, stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "names", "resolver", "apply", "--on", "--digest", "old"}); code == exitOK {
		t.Fatal("stale digest accepted")
	}
	for _, args := range [][]string{
		{"names", "resolver", "plan"},
		{"names", "resolver", "apply", "--on"},
		{"names", "resolver", "plan", "--on", "--upstream", "1.1.1.1"},
		{"names", "bogus"},
	} {
		if code, _, _ := invoke(t, append([]string{"--server", url}, args...)); code != exitUsage {
			t.Errorf("%v = %d, want usage", args, code)
		}
	}
}
