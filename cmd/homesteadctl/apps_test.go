package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/apiclient"
	"homestead/internal/appsso"
)

func appsStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/nextcloud/secrets":
			_, _ = w.Write([]byte(`[{"name":"admin_password","store_name":"app_nextcloud_admin_password","present":true,"immutable":true,"services":["nextcloud"],"reveal":true,"note":"Password for the Nextcloud administrator account \"admin\"."}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog":
			_, _ = w.Write([]byte(`[{"name":"jellyfin","version":"12.2","title":"Jellyfin","blurb":"Media.","category":"media","icon":"mdi:plex"}]`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/plan"):
			_, _ = w.Write([]byte(`{"Package":"jellyfin","Requirements":{"Passed":true},"Routes":[{"Host":"jellyfin.lan","Exposure":"local"}],"DNS":[],"TLS":[],"Firewall":[],"Directories":[],"Mounts":[{"Service":"jellyfin","Target":"/config","Source":"/mnt/app/jellyfin/config","Class":"application","Declared":true},{"Service":"cache","Target":"/data","Ephemeral":true,"Declared":true}],"BrowserAdvisories":[{"Capability":"webcodecs","Label":"Video codecs (WebCodecs)","Reason":"Hardware video decoding."}],"Notes":["owner volume ignored"],"Actions":["provision","up"]}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/secrets"):
			_, _ = w.Write([]byte(`[{"name":"db_password","store_name":"app_authentik_db_password","present":false,"immutable":false,"services":["postgresql","server"],"database_password":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/nextcloud/secrets/admin_password/reveal":
			var body struct {
				Confirm bool `json:"confirm"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !body.Confirm {
				http.Error(w, "confirm", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"name":"admin_password","value":"revealed-pw","note":"Password for the Nextcloud administrator account \"admin\"."}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/secrets/"):
			var body struct {
				Value      string `json:"value"`
				SkipVerify bool   `json:"skip_verify"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Value != "s3cret-value" || !body.SkipVerify {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"store_name":"app_authentik_db_password"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/install"):
			_, _ = w.Write([]byte(`{"id":7}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/configure"):
			_, _ = w.Write([]byte(`{"id":9}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/uninstall"):
			_, _ = w.Write([]byte(`{"id":11}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func writeInput(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestAppsList(t *testing.T) {
	url := appsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "list"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "jellyfin 12.2 — Jellyfin") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAppsPlan(t *testing.T) {
	url := appsStub(t)
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "plan", "jellyfin", "-f", file})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"package: jellyfin", "route: jellyfin.lan (local)", "actions: provision, up", "mount: jellyfin /config <- /mnt/app/jellyfin/config (application)", "mount: cache /data <- tmpfs (ephemeral)", "note: owner volume ignored", "browser: Video codecs (WebCodecs) [webcodecs] (advisory) - Hardware video decoding."} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "plan", "jellyfin"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestAppsPlanWithoutAdvisoriesPrintsNoBrowserLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Package":"jellyfin","Requirements":{"Passed":true},"Routes":[],"BrowserAdvisories":null,"Notes":[],"Actions":["up"]}`))
	}))
	defer srv.Close()
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, _ := invoke(t, []string{"--server", srv.URL, "apps", "plan", "jellyfin", "-f", file})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout, "browser:") {
		t.Fatalf("no advisories must print no browser line:\n%s", stdout)
	}
}

func TestAppsInstall(t *testing.T) {
	url := appsStub(t)
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "install", "jellyfin", "-f", file})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "operation: 7") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAppsInstallApprovesLocalAddressSetup(t *testing.T) {
	var install map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/catalog/jellyfin/plan":
			_, _ = w.Write([]byte(`{"Package":"jellyfin","Routes":[{"Host":"jellyfin.lan","Exposure":"local"}],"digest":"reviewed"}`))
		case "/api/v1/catalog/jellyfin/install":
			if err := json.NewDecoder(r.Body).Decode(&install); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"id":7}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	file := writeInput(t, `{"domain":"lan","setup":{"sign_in":{"digest":"signin-reviewed"},"people":[42]}}`)
	code, _, stderr := invoke(t, []string{"--server", srv.URL, "apps", "install", "jellyfin", "-f", file})
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, ok := install["setup"].(map[string]any); !ok || install["digest"] != "reviewed" {
		t.Fatalf("install must approve its reviewed address setup: %#v", install)
	}
	setup := install["setup"].(map[string]any)
	if setup["sign_in"] == nil || setup["people"] == nil {
		t.Fatalf("install lost approved sign-in or people: %#v", setup)
	}
}

func TestAppsInstallReviewsAndApprovesSignInAndPeople(t *testing.T) {
	var install map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/catalog/jellyfin/plan":
			_, _ = w.Write([]byte(`{"Package":"jellyfin","Routes":[{"Host":"jellyfin.lan","Exposure":"local"}],"digest":"install-d","sign_in":{"offered":true,"token_kept":true,"plan":{"package":"jellyfin","title":"Jellyfin","enabled":true,"digest":"signin-d","provider":"Jellyfin"}}}`))
		case "/api/v1/people":
			_, _ = w.Write([]byte(`{"token_kept":true,"people":[{"id":42,"state":"active","username":"reader","display_name":"Reader"}]}`))
		case "/api/v1/catalog/jellyfin/install":
			if err := json.NewDecoder(r.Body).Decode(&install); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"id":7}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, stderr := invoke(t, []string{"--server", srv.URL, "apps", "plan", "jellyfin", "-f", file})
	if code != exitOK || !strings.Contains(stdout, "signin-d") {
		t.Fatalf("plan: %d %s %s", code, stdout, stderr)
	}
	code, _, stderr = invoke(t, []string{"--server", srv.URL, "apps", "install", "jellyfin", "-f", file, "--approve-sign-in", "signin-d", "--person", "99"})
	if code != exitError || install != nil || !strings.Contains(stderr, "missing or disabled") {
		t.Fatalf("invalid person must stop before install: %d %s %#v", code, stderr, install)
	}
	code, _, stderr = invoke(t, []string{"--server", srv.URL, "apps", "install", "jellyfin", "-f", file, "--approve-sign-in", "signin-d", "--person", "42", "--person", "42"})
	if code != exitError || install != nil || !strings.Contains(stderr, "more than once") {
		t.Fatalf("duplicate person must stop before install: %d %s %#v", code, stderr, install)
	}
	code, _, stderr = invoke(t, []string{"--server", srv.URL, "apps", "install", "jellyfin", "-f", file, "--approve-sign-in", "signin-d", "--person", "42"})
	if code != exitOK {
		t.Fatalf("install: %d %s", code, stderr)
	}
	setup, _ := install["setup"].(map[string]any)
	si, _ := setup["sign_in"].(map[string]any)
	people, _ := setup["people"].([]any)
	if si["digest"] != "signin-d" || len(people) != 1 || people[0] != float64(42) {
		t.Fatalf("setup = %#v", setup)
	}
}

func TestInstallSignInApprovalRefusesUnsafeOrStaleRequests(t *testing.T) {
	ready := &apiclient.InstallSignIn{Offered: true, TokenKept: true, Plan: &appsso.Plan{Digest: "current"}}
	for _, tc := range []struct {
		name   string
		offer  *apiclient.InstallSignIn
		digest string
		people []int64
	}{
		{"people without sign-in", ready, "", []int64{1}},
		{"unavailable", nil, "current", nil},
		{"no retained token", &apiclient.InstallSignIn{Offered: true, Plan: &appsso.Plan{Digest: "current"}}, "current", nil},
		{"stale plan", ready, "old", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := map[string]any{}
			if err := approveInstallSignIn(tc.offer, tc.digest, tc.people, setup); err == nil || len(setup) != 0 {
				t.Fatalf("must reject without changing setup: %v %#v", err, setup)
			}
		})
	}
}

func TestAppsConfigure(t *testing.T) {
	url := appsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "configure", "jellyfin",
		"--service", "jellyfin", "--set", "TZ=UTC", "--set", "PUID=1000", "--unset", "OLD"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "operation: 9") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "configure", "jellyfin", "--service", "jellyfin"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "configure", "jellyfin",
		"--service", "jellyfin", "--set", "NOEQUALS"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "bogus"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestAppsUninstall(t *testing.T) {
	url := appsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "uninstall", "jellyfin"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "operation: 11") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, _ = invoke(t, []string{"--server", url, "apps", "uninstall"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
	code, _, stderr := invoke(t, []string{"--server", url, "apps", "uninstall", "jellyfin", "--data-dir", "nope"})
	if code != exitUsage || !strings.Contains(stderr, "MOUNT:PATH") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestAppsSecrets(t *testing.T) {
	url := appsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "secrets", "authentik"})
	if code != exitOK || !strings.Contains(stdout, "db_password: missing (app_authentik_db_password") ||
		!strings.Contains(stdout, "database-password") {
		t.Fatalf("exit=%d stdout=%q", code, stdout)
	}
	file := writeInput(t, "s3cret-value\n")
	code, stdout, stderr := invoke(t, []string{"--server", url, "apps", "secrets", "register", "authentik", "db_password", "--value-file", file, "--skip-verify"})
	if code != exitOK || !strings.Contains(stdout, "registered: app_authentik_db_password") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "s3cret-value") {
		t.Fatal("value echoed")
	}
	if code, _, _ := invoke(t, []string{"--server", url, "apps", "secrets", "register", "authentik", "db_password"}); code != exitUsage {
		t.Fatalf("missing value file exit = %d", code)
	}
}

func TestAppsConfigureRotateSecret(t *testing.T) {
	url := appsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "configure", "authentik", "--rotate-secret", "db_password"})
	if code != exitOK || !strings.Contains(stdout, "operation: 9") {
		t.Fatalf("exit=%d stdout=%q", code, stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "apps", "configure", "authentik", "--set", "A=b"}); code != exitUsage {
		t.Fatalf("set without service exit = %d", code)
	}
}

func TestAppsAdoptAndUninstallFlags(t *testing.T) {
	var configureBody, uninstallBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/configure"):
			configureBody = string(raw)
		case strings.HasSuffix(r.URL.Path, "/uninstall"):
			uninstallBody = string(raw)
		}
		_, _ = w.Write([]byte(`{"id":5}`))
	}))
	t.Cleanup(ts.Close)
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "apps", "configure", "authentik", "--adopt-secrets"})
	if code != exitOK || !strings.Contains(stdout, "operation: 5") || !strings.Contains(configureBody, `"adopt_secrets":true`) {
		t.Fatalf("exit=%d out=%q body=%q", code, stdout, configureBody)
	}
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "apps", "configure", "authentik", "--adopt-secrets", "--rotate-secret", "x"}); code != exitUsage {
		t.Fatalf("adopt plus rotate exit = %d", code)
	}
	code, _, _ = invoke(t, []string{"--server", ts.URL, "apps", "uninstall", "authentik", "--abandon-volumes"})
	if code != exitOK || !strings.Contains(uninstallBody, `"abandon_volumes":true`) {
		t.Fatalf("exit=%d body=%q", code, uninstallBody)
	}
	code, _, _ = invoke(t, []string{"--server", ts.URL, "apps", "uninstall", "immich", "--leave-sign-in"})
	if code != exitOK || !strings.Contains(uninstallBody, `"leave_sign_in":true`) || strings.Contains(uninstallBody, "abandon") {
		t.Fatalf("exit=%d body=%q", code, uninstallBody)
	}
}

func TestAppsSecretRegisterBoundsAndForce(t *testing.T) {
	var body string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"store_name":"app_authentik_db_password"}`))
	}))
	t.Cleanup(ts.Close)
	big := writeInput(t, strings.Repeat("x", 70<<10))
	code, _, stderr := invoke(t, []string{"--server", ts.URL, "apps", "secrets", "register", "authentik", "db_password", "--value-file", big})
	if code != exitUsage || !strings.Contains(stderr, "64 KiB") || body != "" {
		t.Fatalf("oversize exit=%d stderr=%q sent=%q", code, stderr, body)
	}
	ok := writeInput(t, "pw\n")
	code, _, _ = invoke(t, []string{"--server", ts.URL, "apps", "secrets", "register", "authentik", "db_password", "--value-file", ok, "--force", "--skip-verify"})
	if code != exitOK || !strings.Contains(body, `"force":true`) {
		t.Fatalf("exit=%d body=%q", code, body)
	}
}

func TestAppsSecretsReveal(t *testing.T) {
	url := appsStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "apps", "secrets", "nextcloud"})
	if code != exitOK || !strings.Contains(stdout, "readable (apps secrets reveal nextcloud admin_password)") ||
		!strings.Contains(stdout, "administrator account") || strings.Contains(stdout, "revealed-pw") {
		t.Fatalf("list exit = %d stdout = %q", code, stdout)
	}
	code, stdout, stderr := invoke(t, []string{"--server", url, "apps", "secrets", "reveal", "nextcloud", "admin_password"})
	if code != exitOK || stdout != "revealed-pw\n" || !strings.Contains(stderr, "audited") {
		t.Fatalf("reveal exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "apps", "secrets", "reveal", "nextcloud"}); code != exitUsage {
		t.Fatalf("usage exit = %d", code)
	}
}

// S326: a craft app's install plan lists its Nextcloud link; apps plan
// prints it and apps install approves it with the install's setup.
func TestAppsInstallApprovesTheNextcloudLink(t *testing.T) {
	var installBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api/v1/catalog/cadcraft/plan":
			_, _ = w.Write([]byte(`{"Package":"cadcraft","Requirements":{"Passed":true},"Routes":[],"Actions":["up"],"digest":"inst-d",
				"files_link":{"apps":["cadcraft"],"lines":["Link CADCraft to Nextcloud for files"],"base_url":"https://homestead.lan/apps/nextcloud","account":"admin","root":"Homestead","new_link":true,"digest":"link-d"}}`))
		case "/api/v1/catalog/cadcraft/install":
			installBody = string(raw)
			_, _ = w.Write([]byte(`{"id":7}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	file := writeInput(t, `{"domain":"lan"}`)
	code, stdout, _ := invoke(t, []string{"--server", srv.URL, "apps", "plan", "cadcraft", "-f", file})
	if code != exitOK || !strings.Contains(stdout, "files: Link CADCraft to Nextcloud for files") ||
		!strings.Contains(stdout, "files: into https://homestead.lan/apps/nextcloud/Homestead as account admin") {
		t.Fatalf("plan: %d %s", code, stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", srv.URL, "apps", "install", "cadcraft", "-f", file})
	if code != exitOK || !strings.Contains(stdout, "operation: 7") || !strings.Contains(stdout, "files: Link CADCraft") {
		t.Fatalf("install: %d %s", code, stdout)
	}
	if !strings.Contains(installBody, `"setup":{"files_link":{"digest":"link-d"}}`) || !strings.Contains(installBody, `"digest":"inst-d"`) {
		t.Fatalf("install body = %s", installBody)
	}
}

func TestAppsShowIncludesContainerPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Name":"jellyfin","Version":"12.2","Blurb":"Media","Services":[{"Name":"jellyfin","Exposes":"web","ContainerPort":8096}]}`))
	}))
	defer srv.Close()
	code, stdout, stderr := invoke(t, []string{"--server", srv.URL, "apps", "show", "jellyfin"})
	if code != exitOK || !strings.Contains(stdout, "container port: jellyfin 8096") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}
