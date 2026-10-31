package main

import (
	"encoding/json"
	"fmt"
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

// installPublicStub is an appliance for `apps install --public` (S313):
// it records each call so the test can check the order and the bodies.
func installPublicStub(t *testing.T, calls *[]string, bodies map[string]map[string]any, publicDigest string, publicStatus int) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		*calls = append(*calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies[r.URL.Path] = body
		}
		switch r.URL.Path {
		case "/api/v1/people":
			_, _ = w.Write([]byte(`{"token_kept":true,"people":[{"id":42,"state":"active","username":"reader","display_name":"Reader"}]}`))
		case "/api/v1/catalog/nextcloud/plan":
			mode := "open"
			if _, ok := bodies[r.URL.Path]["choices"]; ok {
				mode = "managed"
			}
			_, _ = fmt.Fprintf(w, `{"Package":"nextcloud","access":{"mode":%q,"people":[{"id":42,"state":"active"}]},"Requirements":{"Passed":true},"Routes":[{"Host":"nextcloud.lan","Target":{"Host":"nextcloud","Port":80},"Exposure":"local","TLS":"internal"}],"digest":"i1d","sign_in":{"offered":true,"token_kept":true,"plan":{"package":"nextcloud","title":"Nextcloud","enabled":true,"digest":"s1d"}}}`, mode)
		case "/api/v1/catalog/nextcloud/access-plan":
			_, _ = w.Write([]byte(`{"digest":"a1d"}`))
		case "/api/v1/catalog/nextcloud/public/plan":
			_, _ = w.Write([]byte(`{"app":"nextcloud","action":"add","hostname":"nextcloud.homestead.example.com","route":"nextcloud.lan","tunnel":"homestead","zone":"example.com",
				"dns":[{"name":"nextcloud.homestead.example.com","type":"CNAME","content":"tid.cfargotunnel.com","proxied":true,"action":"create","replaces":[]}],
				"connector_after":["nextcloud.homestead.example.com"],"app_settings":["Nextcloud trusts nextcloud.homestead.example.com"],
				"warnings":["No certificate covers nextcloud.homestead.example.com"],"digest":"` + publicDigest + `"}`))
		case "/api/v1/catalog/nextcloud/install":
			_, _ = w.Write([]byte(`{"id":7}`))
		case "/api/v1/edge/routes":
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id":7}`))
				return
			}
			_, _ = w.Write([]byte(`{"routes":[]}`))
		case "/api/v1/catalog/nextcloud/setup":
			if publicStatus != http.StatusOK {
				_, _ = w.Write([]byte(`{"app":"nextcloud","status":"stopped","operation":7,"steps":[{"name":"address","state":"done","digest":"a"},{"name":"publish","state":"changed","digest":"p2d","reason":"The plan changed since it was approved."}],"review":{"step":"publish","digest":"p9d"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"app":"nextcloud","status":"done","operation":7,"steps":[{"name":"address","state":"done","digest":"a"},{"name":"publish","state":"done","digest":"p2d"}]}`))
		case "/api/v1/catalog/nextcloud/public/apply":
			if publicStatus != http.StatusOK {
				http.Error(w, "the plan changed", publicStatus)
				return
			}
			_, _ = w.Write([]byte(`{"id":7}`))
		case "/api/v1/catalog/nextcloud/reach":
			_, _ = w.Write([]byte(`{"app":"nextcloud","resolver":false,"addresses":[
				{"address":"https://nextcloud.lan","host":"nextcloud.lan","kind":"local","answers":true,"reason":"Your devices cannot resolve nextcloud.lan while the appliance resolver is off.","fix":"Turn on the resolver on the Names page, or add a router DNS entry for nextcloud.lan."},
				{"address":"https://nextcloud.homestead.example.com","host":"nextcloud.homestead.example.com","kind":"public","answers":false,"reason":"No certificate covers it."}]}`))
		case "/api/v1/operations/7/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: state\ndata: {\"id\":7,\"type\":\"test.op\",\"status\":\"succeeded\",\"steps\":[]}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestAppsInstallPublicPlansBoth(t *testing.T) {
	var calls []string
	bodies := map[string]map[string]any{}
	url := installPublicStub(t, &calls, bodies, "p2d", http.StatusOK)
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, stderr := invoke(t, []string{"--server", url, "apps", "install", "nextcloud", "-f", file, "--public"})
	for _, want := range []string{"install digest: i1d", "public: https://nextcloud.homestead.example.com -> nextcloud.lan",
		"app setting: Nextcloud trusts", "warning: No certificate covers", "public digest: p2d",
		"--public nextcloud.homestead.example.com --approve-public p2d --approve-sign-in s1d"} {
		if code != exitOK || !strings.Contains(stdout, want) {
			t.Fatalf("missing %q: %d %s %s", want, code, stdout, stderr)
		}
	}
	for _, call := range calls {
		if strings.HasSuffix(call, "/install") || strings.HasSuffix(call, "/apply") || strings.HasPrefix(call, "POST /api/v1/edge") {
			t.Fatalf("planning changed something: %v", calls)
		}
	}
	plan := bodies["/api/v1/catalog/nextcloud/public/plan"]
	if plan["before_install"] != true || plan["route"] != "nextcloud.lan" {
		t.Fatalf("public plan body = %v", plan)
	}
	if bodies["/api/v1/catalog/nextcloud/plan"]["public_name"] != "nextcloud.homestead.example.com" {
		t.Fatalf("install plan body = %v", bodies["/api/v1/catalog/nextcloud/plan"])
	}
}

func TestAppsInstallPublicPlanBindsManagedAccessAndAnticipatedSignIn(t *testing.T) {
	var calls []string
	bodies := map[string]map[string]any{}
	url := installPublicStub(t, &calls, bodies, "p2d", http.StatusOK)
	file := writeInput(t, `{"domain":"lan","choices":[{"person_id":42,"role":"user"}]}`)
	code, stdout, stderr := invoke(t, []string{"--server", url, "apps", "install", "nextcloud", "-f", file, "--public"})
	if code != exitOK || !strings.Contains(stdout, "--approve-access a1d") {
		t.Fatalf("managed public review: %d %s %s", code, stdout, stderr)
	}
	if got := bodies["/api/v1/catalog/nextcloud/public/plan"]["anticipate_sign_in"]; got != true {
		t.Fatalf("public review did not anticipate sign-in: %v", got)
	}
	code, stdout, stderr = invoke(t, []string{"--server", url, "apps", "install", "nextcloud", "-f", file, "--public", "--approve-public", "p2d", "--approve-sign-in", "s1d", "--approve-access", "a1d"})
	if code != exitOK {
		t.Fatalf("managed public install: %d %s %s", code, stdout, stderr)
	}
	setup := bodies["/api/v1/catalog/nextcloud/install"]["setup"].(map[string]any)
	if setup["public"].(map[string]any)["digest"] != "p2d" || setup["access"] == nil || setup["sign_in"] == nil {
		t.Fatalf("missing original approvals: %v", setup)
	}
}

func TestAppsInstallPublicApproved(t *testing.T) {
	var calls []string
	bodies := map[string]map[string]any{}
	url := installPublicStub(t, &calls, bodies, "p2d", http.StatusOK)
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, stderr := invoke(t, []string{"--server", url, "apps", "install", "nextcloud", "-f", file,
		"--public", "Nextcloud.homestead.example.com", "--approve-public", "p2d"})
	if code != exitOK {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	var changes []string
	for _, call := range calls {
		if strings.HasPrefix(call, "POST") && !strings.HasSuffix(call, "/plan") {
			changes = append(changes, call)
		}
	}
	// The install carries the approval; the appliance does the rest (S324).
	if strings.Join(changes, "|") != "POST /api/v1/catalog/nextcloud/install" {
		t.Fatalf("changes = %v", changes)
	}
	installBody := bodies["/api/v1/catalog/nextcloud/install"]
	if installBody["digest"] != "i1d" {
		t.Fatalf("install body = %v", installBody)
	}
	setup, _ := installBody["setup"].(map[string]any)
	public, _ := setup["public"].(map[string]any)
	if public["digest"] != "p2d" || public["route"] != "nextcloud.lan" || public["hostname"] != "nextcloud.homestead.example.com" {
		t.Fatalf("install setup = %v", installBody["setup"])
	}
	for _, want := range []string{"https://nextcloud.lan: answers", "fix: Turn on the resolver",
		"https://nextcloud.homestead.example.com: does not answer yet"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("missing %q: %s", want, stdout)
		}
	}
}

func TestAppsInstallPublicApprovesSignInAndPeople(t *testing.T) {
	var calls []string
	bodies := map[string]map[string]any{}
	url := installPublicStub(t, &calls, bodies, "p2d", http.StatusOK)
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, stderr := invoke(t, []string{"--server", url, "apps", "install", "nextcloud", "-f", file,
		"--public", "nextcloud.homestead.example.com", "--approve-public", "p2d", "--approve-sign-in", "s1d", "--person", "42"})
	if code != exitOK {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	setup, _ := bodies["/api/v1/catalog/nextcloud/install"]["setup"].(map[string]any)
	si, _ := setup["sign_in"].(map[string]any)
	people, _ := setup["people"].([]any)
	if si["digest"] != "s1d" || len(people) != 1 || people[0] != float64(42) {
		t.Fatalf("setup = %#v", setup)
	}
}

func TestAppsInstallPublicDigestBinding(t *testing.T) {
	var calls []string
	bodies := map[string]map[string]any{}
	url := installPublicStub(t, &calls, bodies, "p3d", http.StatusOK)
	file := writeInput(t, `{"domain":"lan"}`)
	code, _, stderr := invoke(t, []string{"--server", url, "apps", "install", "nextcloud", "-f", file, "--public", "--approve-public", "p2d"})
	if code != exitError || !strings.Contains(stderr, "review it again") {
		t.Fatalf("stale approve: %d %s", code, stderr)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "POST") && !strings.HasSuffix(call, "/plan") {
			t.Fatalf("a stale approval changed something: %v", calls)
		}
	}
	// The plan changing after the install: installed, the setup stopped.
	calls = nil
	url = installPublicStub(t, &calls, bodies, "p2d", http.StatusConflict)
	code, _, stderr = invoke(t, []string{"--server", url, "apps", "install", "nextcloud", "-f", file, "--public", "--approve-public", "p2d"})
	if code == exitOK || !strings.Contains(stderr, "its setup stopped, so nothing more was published") {
		t.Fatalf("conflict: %d %s", code, stderr)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "apps", "install", "nextcloud", "-f", file, "--approve-public", "p2d"}); code != exitUsage {
		t.Fatalf("--approve-public without --public: %d", code)
	}
}

func TestAppsReach(t *testing.T) {
	var calls []string
	url := installPublicStub(t, &calls, map[string]map[string]any{}, "p2d", http.StatusOK)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "reach", "nextcloud"})
	if code != exitOK || !strings.Contains(stdout, "https://nextcloud.lan: answers") || !strings.Contains(stdout, "does not answer yet") {
		t.Fatalf("reach: %d %s", code, stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "apps", "reach"}); code != exitUsage {
		t.Fatalf("usage: %d", code)
	}
}
