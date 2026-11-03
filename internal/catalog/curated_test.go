package catalog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homestead/internal/craft"
	"homestead/internal/identity"
	"homestead/internal/storage"
)

// repoCatalog locates the curated repository catalog from the package
// directory: packages ship with the code they were reviewed against.
func repoCatalog(t *testing.T) Catalog {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller unavailable")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "catalog")
	catalog, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return catalog
}

// curatedPackages is the one place the expected shipped catalog is listed
// (sorted, as Names returns it). Adding a package to catalog/ edits this
// list and nothing else here: each craft slice appends its app.
var curatedPackages = []string{"actual-budget", "api-client", "audiobookshelf", "authentik", "bookstack", "cadcraft", "calibre-web-automated", "circuits", "contacts", "deckcraft", "designcraft", "effectcraft", "expense", "figcraft", "filmcraft", "freshrss", "gitea", "gridcraft", "immich", "inventory", "jellyfin", "komga", "lightcraft", "linkding", "mealie", "memos", "navidrome", "nextcloud", "notes", "paperless-ngx", "parametric", "pdfcraft", "photocraft", "projects", "records", "solitaire", "soundcraft", "sudoku", "tasks", "three-d", "time-tracking", "uptime-kuma", "vaultwarden", "vectorcraft", "vikunja", "wordcraft"}

// assertCuratedNames fails when the shipped catalog and curatedPackages
// differ, in either direction.
func assertCuratedNames(t *testing.T, catalog Catalog) {
	t.Helper()
	names := catalog.Names()
	if len(names) != len(curatedPackages) {
		t.Fatalf("names = %v, want %v", names, curatedPackages)
	}
	for i := range names {
		if names[i] != curatedPackages[i] {
			t.Fatalf("names = %v, want %v", names, curatedPackages)
		}
	}
}

func TestCuratedJellyfin(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("jellyfin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// S319: Homestead finishes the startup wizard, with three libraries
	// on provisioned bulk folders.
	if f := manifest.FirstRun; f == nil || f.Routine != FirstRunJellyfin || f.Account != "admin" || len(f.Libraries) != 3 ||
		!manifest.HeldSecret("admin_password") {
		t.Fatalf("first run = %+v", manifest.FirstRun)
	}
	if manifest.Version != "12.2" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].Image != "jellyfin/jellyfin:12.2" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	if manifest.Requirements.MemoryMB < 1024 || manifest.Services[0].ContainerPort != 8096 {
		t.Fatalf("requirements = %+v", manifest.Requirements)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	// A sufficient host installs cleanly from the curated entry.
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 8192, CPUCount: 4,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 10 << 30,
			storage.ClassBulk:        500 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

func TestCuratedNextcloud(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("nextcloud")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "35.0.1" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].Image != "nextcloud:35.0.1-apache" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	assertCuratedNames(t, catalog)
}

func TestCuratedImmich(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("immich")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "3.3.0" {
		t.Fatalf("version = %q", manifest.Version)
	}
	// S318: Homestead creates Immich's administrator, with a held,
	// revealable password no container reads.
	if f := manifest.FirstRun; f == nil || f.Routine != FirstRunImmich || f.Service != "server" || f.Secret != "admin_password" ||
		!manifest.HeldSecret("admin_password") || len(SecretFilesFor(manifest)) != 2 {
		t.Fatalf("first run = %+v, files %+v", manifest.FirstRun, SecretFilesFor(manifest))
	}
	if len(manifest.Services) != 4 {
		t.Fatalf("services = %+v", manifest.Services)
	}
	internal := 0
	for _, service := range manifest.Services {
		if service.Exposes == "" {
			internal++
		}
	}
	if internal != 3 {
		t.Fatalf("internal services = %d", internal)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 8192, CPUCount: 4,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 10 << 30,
			storage.ClassBulk:        500 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
	assertCuratedNames(t, catalog)
}

func TestCuratedAuthentik(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("authentik")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "2026.8.3" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 3 {
		t.Fatalf("services = %+v", manifest.Services)
	}
	internal := 0
	for _, service := range manifest.Services {
		if service.Exposes == "" {
			internal++
		}
	}
	if internal != 2 {
		t.Fatalf("internal services = %d", internal)
	}
	if manifest.Database == nil || manifest.Database.Service != "postgresql" ||
		manifest.Database.Kind != DatabasePostgres || manifest.Database.Database != "authentik" ||
		manifest.Database.Username != "authentik" {
		t.Fatalf("database = %+v", manifest.Database)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 8192, CPUCount: 4,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 10 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedVaultwarden pins the first Wave 1 package (S345): a single
// container with owner signup, no administrative surface by default, and
// local exposure.
func TestCuratedVaultwarden(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("vaultwarden")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "1.37.4" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].Image != "vaultwarden/server:1.37.4" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if service.Environment["SIGNUPS_ALLOWED"] != "true" {
		t.Fatalf("signups = %q, want owner signup open", service.Environment["SIGNUPS_ALLOWED"])
	}
	if _, enabled := service.Environment["ADMIN_TOKEN"]; enabled {
		t.Fatal("the admin page must stay off by default")
	}
	if service.Environment["DOMAIN"] != AppURLToken {
		t.Fatalf("domain = %q, want the app address (S407)", service.Environment["DOMAIN"])
	}
	if len(service.Data) != 1 || service.Data[0].Target != "/data" ||
		service.Data[0].Class != storage.ClassApplication || service.Data[0].Subpath != "data" {
		t.Fatalf("data = %+v", service.Data)
	}
	if len(manifest.BackupPaths) != 1 || manifest.BackupPaths[0] != "data" {
		t.Fatalf("backuppaths = %v", manifest.BackupPaths)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if len(manifest.Integrations) != 1 || manifest.Integrations[0].Kind != IntegrationIdentity ||
		manifest.Integrations[0].Target != "authentik" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	if manifest.Database != nil {
		t.Fatalf("database = %+v, want SQLite in the volume", manifest.Database)
	}
	// S414: sign-in is environment alone, the secret is a file Vaultwarden
	// reads itself, and local login is never turned off.
	oidc := manifest.SSO.OIDC
	if oidc == nil || oidc.Adapter != "env-oidc" || oidc.Env == nil {
		t.Fatalf("sso.oidc = %+v", oidc)
	}
	if _, off := oidc.Env.Set["SSO_ONLY"]; off || oidc.Env.Set["SSO_AUTHORITY"] != "{{issuer}}" ||
		oidc.Env.Trust != "SSL_CERT_FILE" || oidc.Env.TrustBundle != TrustBundleCombined {
		t.Fatalf("env = %+v", oidc.Env)
	}
	if len(manifest.Secrets) != 1 || manifest.Secrets[0].Name != "oidc_client_secret" ||
		len(manifest.Secrets[0].Consumers) != 1 || manifest.Secrets[0].Consumers[0].Env != "SSO_CLIENT_SECRET_FILE" ||
		manifest.Secrets[0].Consumers[0].Export {
		t.Fatalf("secrets = %+v", manifest.Secrets)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 2048, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{storage.ClassApplication: 2 << 30},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedPaperless pins the three-service document archive (S346):
// Postgres metadata, a Valkey queue, bulk documents, and supervised
// secret export for the image that reads only plaintext variables.
func TestCuratedPaperless(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("paperless-ngx")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "3.3.4" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 3 {
		t.Fatalf("services = %+v", manifest.Services)
	}
	internals := 0
	for _, service := range manifest.Services {
		if service.Exposes == "" {
			internals++
		}
	}
	if internals != 2 {
		t.Fatalf("internal services = %d, want 2", internals)
	}
	web := manifest.Services[0]
	if web.Image != "ghcr.io/paperless-ngx/paperless-ngx:3.3.4" {
		t.Fatalf("web image = %q", web.Image)
	}
	if len(web.Entrypoint) != 1 || web.Entrypoint[0] != "/init" {
		t.Fatalf("entrypoint = %v, want the s6 init the supervisor execs", web.Entrypoint)
	}
	for key, want := range map[string]string{
		"PAPERLESS_DBHOST":       "db",
		"PAPERLESS_REDIS":        "redis://broker:6379",
		"PAPERLESS_OCR_LANGUAGE": "eng",
		"PAPERLESS_ADMIN_USER":   "admin",
	} {
		if got := web.Environment[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if domain := web.Environment["PAPERLESS_URL"]; !strings.HasPrefix(domain, "https://") ||
		!strings.Contains(domain, RouteHostToken) {
		t.Fatalf("PAPERLESS_URL = %q, want the routed HTTPS host", domain)
	}
	paths := map[string]bool{}
	for _, data := range web.Data {
		paths[data.Target] = true
		if data.Class != storage.ClassApplication && data.Class != storage.ClassBulk {
			t.Fatalf("data %s class = %q", data.Target, data.Class)
		}
	}
	for _, volume := range []string{
		"/usr/src/paperless/data", "/usr/src/paperless/media",
		"/usr/src/paperless/consume", "/usr/src/paperless/export",
	} {
		if !paths[volume] {
			t.Fatalf("image VOLUME %s has no declaration", volume)
		}
	}
	var exportVars []string
	for _, decl := range manifest.Secrets {
		for _, consumer := range decl.Consumers {
			if consumer.Export {
				exportVars = append(exportVars, consumer.Env)
			}
		}
	}
	for _, want := range []string{
		"PAPERLESS_DBPASS_FILE", "PAPERLESS_SECRET_KEY_FILE", "PAPERLESS_ADMIN_PASSWORD_FILE",
	} {
		found := false
		for _, env := range exportVars {
			if env == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s is not exported through the supervisor", want)
		}
	}
	if manifest.Database == nil || manifest.Database.Kind != "postgres" ||
		manifest.Database.Database != "paperless" || manifest.Database.PasswordSecret != "db_password" {
		t.Fatalf("database = %+v", manifest.Database)
	}
	if len(manifest.BackupPaths) != 2 {
		t.Fatalf("backuppaths = %v, want the index and the library", manifest.BackupPaths)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if len(manifest.Integrations) != 2 ||
		manifest.Integrations[0].Kind != IntegrationIdentity || manifest.Integrations[0].Target != "authentik" ||
		manifest.Integrations[1].Kind != IntegrationLibrary || manifest.Integrations[1].Target != "nextcloud" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	if scripts := EntrypointScriptsFor(manifest); len(scripts) != 1 || scripts[0].Service != "webserver" {
		t.Fatalf("scripts = %+v, want one webserver supervisor", scripts)
	}
	// The supervisor replaces the image entrypoint and receives the image
	// initialization as its command: exporting must not change what runs.
	plan, err := PlanInstall(InstallRequest{
		Manifest: manifest, Host: installHost(), Domain: "lan",
		ContainerPorts: map[string]int{"webserver": 8000},
		Storage:        curatedStorage(),
	})
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	var composed struct {
		Services map[string]struct {
			Entrypoint []string `yaml:"entrypoint"`
			Command    []string `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(plan.Compose.ComposeYAML), &composed); err != nil {
		t.Fatalf("compose: %v", err)
	}
	composedWeb := composed.Services["webserver"]
	if len(composedWeb.Entrypoint) != 1 || composedWeb.Entrypoint[0] != EntrypointName {
		t.Fatalf("entrypoint = %v, want the supervisor", composedWeb.Entrypoint)
	}
	if len(composedWeb.Command) != 1 || composedWeb.Command[0] != "/init" {
		t.Fatalf("command = %v, want the image init", composedWeb.Command)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 4096, CPUCount: 4,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 4 << 30,
			storage.ClassBulk:        20 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedGitea pins the single-container forge (S347): SQLite on the
// data volume, environment-only configuration with the installer locked,
// the routed HTTPS root, and HTTPS-only clones with no SSH surface.
func TestCuratedGitea(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("gitea")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "28.2.0" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].Image != "docker.io/gitea/gitea:28.2.0" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	for key, want := range map[string]string{
		"GITEA__server__PROTOCOL":             "http",
		"GITEA__server__DISABLE_SSH":          "true",
		"GITEA__server__START_SSH_SERVER":     "false",
		"GITEA__security__INSTALL_LOCK":       "true",
		"GITEA__service__REQUIRE_SIGNIN_VIEW": "true",
		"GITEA__database__DB_TYPE":            "sqlite3",
		"GITEA__database__PATH":               "/data/gitea/gitea.db",
	} {
		if got := service.Environment[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	// S416: the app address, so Gitea's OAuth callback follows the public name.
	if root := service.Environment["GITEA__server__ROOT_URL"]; root != AppURLToken+"/" {
		t.Fatalf("ROOT_URL = %q, want the app address with a trailing slash", root)
	}
	if service.Environment["GITEA__service__DISABLE_REGISTRATION"] != "false" {
		t.Fatal("registration must stay open until the owner creates the first account")
	}
	if len(service.Data) != 1 || service.Data[0].Target != "/data" ||
		service.Data[0].Class != storage.ClassApplication || service.Data[0].Subpath != "data" ||
		service.Data[0].UID == nil || *service.Data[0].UID != 1000 {
		t.Fatalf("data = %+v", service.Data)
	}
	// Gitea generates its keys into the data volume; the only secrets are
	// the Authentik client secret and the sign-in file's values (S416).
	if len(service.Entrypoint) != 0 || len(manifest.Secrets) != 2 || manifest.Secrets[0].Name != "oidc_client_secret" ||
		manifest.Secrets[1].Name != "gitea_signin" || manifest.Secrets[1].Initial != "{}" || len(manifest.Secrets[1].Consumers) != 1 ||
		manifest.Secrets[1].Consumers[0].Env != "GITEA_SIGNIN_FILE" {
		t.Fatalf("secrets = %+v", manifest.Secrets)
	}
	oidc := manifest.SSO.OIDC
	if oidc == nil || oidc.Adapter != "env-oidc" || oidc.Env == nil || !oidc.Env.Restart || oidc.Env.ExecUser != "git" ||
		oidc.Env.Trust != "SSL_CERT_FILE" || oidc.Env.TrustBundle != TrustBundleCombined ||
		oidc.Env.SignInPath != "/user/oauth2/authentik" || len(oidc.Env.RedirectPaths) != 1 || oidc.Env.RedirectPaths[0] != "/user/oauth2/authentik/callback" ||
		oidc.Env.Set["GITEA__oauth2_client__ACCOUNT_LINKING"] != "login" || oidc.Env.Set["GITEA__oauth2_client__USERNAME"] != "preferred_username" {
		t.Fatalf("sso oidc = %+v", oidc)
	}
	if manifest.Database == nil || manifest.Database.Kind != DatabaseSQLite ||
		manifest.Database.Path != "data/gitea/gitea.db" || manifest.Database.Service != "gitea" {
		t.Fatalf("database = %+v, want the SQLite export", manifest.Database)
	}
	if len(manifest.BackupPaths) != 1 || manifest.BackupPaths[0] != "data" {
		t.Fatalf("backuppaths = %v", manifest.BackupPaths)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if len(manifest.Integrations) != 1 || manifest.Integrations[0].Kind != IntegrationIdentity ||
		manifest.Integrations[0].Target != "authentik" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 2048, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{storage.ClassApplication: 8 << 30},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedFreshRSS pins the Debian feed reader (S348): OIDC-capable
// image, background refresh, both writable directories on application
// storage as www-data, and the owner-completed web installer.
func TestCuratedFreshRSS(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("freshrss")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "1.30.4" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 ||
		manifest.Services[0].Image != "docker.io/freshrss/freshrss:1.30.4" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if strings.Contains(service.Image, "alpine") {
		t.Fatalf("image = %q, want the Debian variant that ships OIDC", service.Image)
	}
	if service.Environment["CRON_MIN"] == "" {
		t.Fatal("CRON_MIN must be set so feeds refresh without a browser")
	}
	if service.Environment["LISTEN"] != "0.0.0.0:80" {
		t.Fatalf("LISTEN = %q", service.Environment["LISTEN"])
	}
	paths := map[string]DataPath{}
	for _, data := range service.Data {
		paths[data.Target] = data
	}
	for _, target := range []string{"/var/www/FreshRSS/data", "/var/www/FreshRSS/extensions"} {
		path, ok := paths[target]
		if !ok {
			t.Fatalf("writable path %s is not declared", target)
		}
		if path.Class != storage.ClassApplication || path.UID == nil || *path.UID != 33 ||
			path.GID == nil || *path.GID != 33 {
			t.Fatalf("%s = %+v, want application storage as www-data", target, path)
		}
	}
	if len(manifest.BackupPaths) != 1 || manifest.BackupPaths[0] != "data" {
		t.Fatalf("backuppaths = %v, want the database and user configuration", manifest.BackupPaths)
	}
	// S412: the owner still completes the web installer (no database, no
	// installer credential); the two secrets are the OIDC client secret
	// and the session crypto key, exported through the supervisor, which
	// execs the image's own ENTRYPOINT and CMD.
	if manifest.Database != nil || len(manifest.Secrets) != 2 ||
		manifest.Secrets[0].Name != "oidc_client_secret" || manifest.Secrets[1].Name != "oidc_crypto_key" {
		t.Fatalf("freshrss installs through the web form; secrets = %+v, database = %+v",
			manifest.Secrets, manifest.Database)
	}
	for _, sc := range manifest.Secrets {
		if len(sc.Consumers) != 1 || !sc.Consumers[0].Export || !strings.HasSuffix(sc.Consumers[0].Env, "_FILE") {
			t.Fatalf("secret %s = %+v, want one exported *_FILE consumer", sc.Name, sc.Consumers)
		}
	}
	if len(service.Entrypoint) != 4 || service.Entrypoint[0] != "./Docker/entrypoint.sh" ||
		!strings.Contains(service.Entrypoint[3], "exec apache2 -D FOREGROUND") ||
		!strings.Contains(service.Entrypoint[3], `$$OIDC_ENABLED`) || strings.Contains(strings.ReplaceAll(service.Entrypoint[3], "$$", ""), "$") {
		t.Fatalf("entrypoint = %q, want the image start with every $ escaped for Compose", service.Entrypoint)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if len(manifest.Integrations) != 1 || manifest.Integrations[0].Kind != IntegrationIdentity ||
		manifest.Integrations[0].Target != "authentik" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 1024, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{storage.ClassApplication: 2 << 30},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedLinkding pins the smallest Wave 1 package (S349): one
// container, SQLite on application storage, and the superuser password
// exported through the supervisor because Linkding has no *_FILE input.
func TestCuratedLinkding(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("linkding")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "1.47.0" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 ||
		manifest.Services[0].Image != "docker.io/sissbruecker/linkding:1.47.0" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if len(service.Entrypoint) != 1 || service.Entrypoint[0] != "./bootstrap.sh" {
		t.Fatalf("entrypoint = %v, want the image CMD the supervisor execs", service.Entrypoint)
	}
	// S409: Linkding splits the value on commas, so the local origin and
	// the published one (the same until a public name exists) both pass.
	if origins := service.Environment["LD_CSRF_TRUSTED_ORIGINS"]; origins != RouteURLToken+","+AppURLToken {
		t.Fatalf("LD_CSRF_TRUSTED_ORIGINS = %q, want the local and published origins", origins)
	}
	if service.Environment["LD_SUPERUSER_NAME"] != "admin" {
		t.Fatalf("superuser name = %q", service.Environment["LD_SUPERUSER_NAME"])
	}
	if _, plain := service.Environment["LD_SUPERUSER_PASSWORD"]; plain {
		t.Fatal("the superuser password must not be plain environment")
	}
	var exported bool
	if len(manifest.Secrets) != 2 || manifest.Secrets[1].Name != "oidc_client_secret" ||
		len(manifest.Secrets[1].Consumers) != 1 || !manifest.Secrets[1].Consumers[0].Export ||
		manifest.Secrets[1].Consumers[0].Env != "OIDC_RP_CLIENT_SECRET_FILE" {
		t.Fatalf("secrets = %+v, want the exported Authentik client secret second (S409)", manifest.Secrets)
	}
	for _, decl := range manifest.Secrets[:1] {
		if decl.Name != "superuser_password" || !decl.Immutable {
			t.Fatalf("secret = %+v", decl)
		}
		for _, consumer := range decl.Consumers {
			if consumer.Service == "linkding" && consumer.Env == "LD_SUPERUSER_PASSWORD_FILE" &&
				consumer.Export {
				exported = true
			}
		}
	}
	if !exported {
		t.Fatalf("secrets = %+v, want the exported superuser password", manifest.Secrets)
	}
	if scripts := EntrypointScriptsFor(manifest); len(scripts) != 1 || scripts[0].Service != "linkding" ||
		!strings.Contains(scripts[0].Body, "export OIDC_RP_CLIENT_SECRET\n") ||
		!strings.Contains(scripts[0].Body, "export LD_SUPERUSER_PASSWORD\n") {
		t.Fatalf("scripts = %+v, want one linkding supervisor exporting both secrets", scripts)
	}
	// S409: the secret file belongs to the data owner (33:33), like the
	// superuser password's.
	for _, f := range SecretFilesFor(manifest) {
		if f.UID == nil || f.GID == nil || *f.UID != 33 || *f.GID != 33 {
			t.Fatalf("secret file %s owner = %v:%v, want 33:33", f.Key, f.UID, f.GID)
		}
	}
	oidc := manifest.SSO.OIDC
	if oidc == nil || oidc.Adapter != AdapterEnvOIDC || oidc.Service != "linkding" || oidc.Env == nil ||
		oidc.Env.Secret != "oidc_client_secret" || oidc.Env.Trust != "REQUESTS_CA_BUNDLE" ||
		oidc.Env.SignInPath != "/oidc/authenticate/" || len(oidc.Env.RedirectPaths) != 1 ||
		oidc.Env.RedirectPaths[0] != "/oidc/callback/" || oidc.Env.Set["LD_ENABLE_OIDC"] != "True" ||
		oidc.Env.Set["OIDC_RP_SIGN_ALGO"] != "RS256" || oidc.Env.Set["OIDC_OP_JWKS_ENDPOINT"] != "{{issuer}}jwks/" {
		t.Fatalf("sso = %+v", oidc)
	}
	if _, secret := oidc.Env.Set["OIDC_RP_CLIENT_SECRET"]; secret {
		t.Fatal("the client secret is never a set value")
	}
	if len(service.Data) != 1 || service.Data[0].Target != "/etc/linkding/data" ||
		service.Data[0].Class != storage.ClassApplication || service.Data[0].Subpath != "data" ||
		service.Data[0].UID == nil || *service.Data[0].UID != 33 {
		t.Fatalf("data = %+v", service.Data)
	}
	if len(manifest.BackupPaths) != 1 || manifest.BackupPaths[0] != "data" {
		t.Fatalf("backuppaths = %v", manifest.BackupPaths)
	}
	if manifest.Database != nil {
		t.Fatalf("database = %+v, want SQLite in the data volume", manifest.Database)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if len(manifest.Integrations) != 1 || manifest.Integrations[0].Kind != IntegrationIdentity ||
		manifest.Integrations[0].Target != "authentik" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	plan, err := PlanInstall(InstallRequest{
		Manifest: manifest, Host: installHost(), Domain: "lan",
		ContainerPorts: map[string]int{"linkding": 9090},
		Storage:        curatedStorage(),
	})
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	var composed struct {
		Services map[string]struct {
			Entrypoint []string `yaml:"entrypoint"`
			Command    []string `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(plan.Compose.ComposeYAML), &composed); err != nil {
		t.Fatalf("compose: %v", err)
	}
	rendered := composed.Services["linkding"]
	if len(rendered.Entrypoint) != 1 || rendered.Entrypoint[0] != EntrypointName {
		t.Fatalf("entrypoint = %v, want the supervisor", rendered.Entrypoint)
	}
	if len(rendered.Command) != 1 || rendered.Command[0] != "./bootstrap.sh" {
		t.Fatalf("command = %v, want the image startup", rendered.Command)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 1024, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{storage.ClassApplication: 2 << 30},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedMealie pins the GHCR recipe manager (S350): one container
// with SQLite, a routed base URL, signups closed behind the seeded
// administrator, and no secret or database declaration.
func TestCuratedMealie(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("mealie")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "3.29.0" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 ||
		manifest.Services[0].Image != "ghcr.io/mealie-recipes/mealie:v3.29.0" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if base := service.Environment["BASE_URL"]; base != AppURLToken {
		t.Fatalf("BASE_URL = %q, want the app address (S407)", base)
	}
	if service.Environment["DB_ENGINE"] != "sqlite" {
		t.Fatalf("DB_ENGINE = %q", service.Environment["DB_ENGINE"])
	}
	if service.Environment["ALLOW_SIGNUP"] != "false" {
		t.Fatal("signups stay closed; the seeded administrator creates accounts")
	}
	// Only the Authentik client secret (S403), handed over as a file: no
	// supervisor and no exported value.
	if len(service.Entrypoint) != 0 || len(manifest.Secrets) != 1 ||
		manifest.Secrets[0].Name != "oidc_client_secret" ||
		len(manifest.Secrets[0].Consumers) != 1 || manifest.Secrets[0].Consumers[0].Export ||
		manifest.Secrets[0].Consumers[0].Env != "OIDC_CLIENT_SECRET_FILE" {
		t.Fatalf("mealie needs no supervisor; secrets = %+v", manifest.Secrets)
	}
	if len(service.Data) != 1 || service.Data[0].Target != "/app/data" ||
		service.Data[0].Class != storage.ClassApplication || service.Data[0].Subpath != "data" {
		t.Fatalf("data = %+v", service.Data)
	}
	// S405: run.sh drops to 911 before reading the *_FILE secret, so the
	// data volume and the secret file belong to 911:911.
	if d := service.Data[0]; d.UID == nil || d.GID == nil || *d.UID != 911 || *d.GID != 911 {
		t.Fatalf("data owner = %v:%v, want 911:911", d.UID, d.GID)
	}
	files := SecretFilesFor(manifest)
	if len(files) != 1 || files[0].UID == nil || files[0].GID == nil || *files[0].UID != 911 || *files[0].GID != 911 {
		t.Fatalf("secret files = %+v, want owner 911:911", files)
	}
	if len(manifest.BackupPaths) != 1 || manifest.BackupPaths[0] != "data" {
		t.Fatalf("backuppaths = %v", manifest.BackupPaths)
	}
	if manifest.Database != nil {
		t.Fatalf("database = %+v, want SQLite in the data volume", manifest.Database)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if len(manifest.Integrations) != 1 || manifest.Integrations[0].Kind != IntegrationIdentity ||
		manifest.Integrations[0].Target != "authentik" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 1024, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{storage.ClassApplication: 4 << 30},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedUptimeKuma pins the monitoring exception (S351): local-only
// exposure that refuses widening, no SSO, no secret, and the SQLite data
// directory on application storage with a memory floor that matters.
func TestCuratedUptimeKuma(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("uptime-kuma")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "2.5.7" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 ||
		manifest.Services[0].Image != "docker.io/louislam/uptime-kuma:2.5.7" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if len(service.Entrypoint) != 0 || len(manifest.Secrets) != 0 {
		t.Fatalf("uptime-kuma needs no supervisor; secrets = %+v", manifest.Secrets)
	}
	if len(service.Data) != 1 || service.Data[0].Target != "/app/data" ||
		service.Data[0].Class != storage.ClassApplication || service.Data[0].Subpath != "data" {
		t.Fatalf("data = %+v", service.Data)
	}
	if len(manifest.BackupPaths) != 1 || manifest.BackupPaths[0] != "data" {
		t.Fatalf("backuppaths = %v", manifest.BackupPaths)
	}
	if manifest.Database != nil {
		t.Fatalf("database = %+v, want SQLite in the data volume", manifest.Database)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok {
		t.Fatal("no web endpoint")
	}
	if string(endpoint.Recommended) != "local" || endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v, want local-only exposure that refuses widening", endpoint)
	}
	if manifest.SSO.Class != SSOUnsupported {
		t.Fatalf("sso = %+v, want the documented local-auth exception", manifest.SSO)
	}
	if len(manifest.Integrations) != 0 {
		t.Fatalf("integrations = %+v, want none", manifest.Integrations)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 2048, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{storage.ClassApplication: 4 << 30},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
	small, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 512, CPUCount: 1,
		FreeSpace: map[storage.Class]uint64{storage.ClassApplication: 4 << 30},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if small.Passed {
		t.Fatal("a 512 MB host must not pass the 1 GB memory floor")
	}
}

// TestCuratedAudiobookshelf pins the first Wave 2 package (S353): the
// pinned GHCR image, SQLite on /config with a consistent export, item
// metadata on application storage, transcode streams on bulk, owner media
// libraries left to the install, optional OIDC configured in the app, and
// the shared-library declaration.
func TestCuratedAudiobookshelf(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("audiobookshelf")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "2.37.1" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 ||
		manifest.Services[0].Image != "ghcr.io/advplyr/audiobookshelf:2.37.1" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if len(service.Environment) != 0 {
		t.Fatalf("environment = %v, want the image's own CONFIG_PATH/METADATA_PATH/PORT defaults", service.Environment)
	}
	if len(service.Entrypoint) != 0 || len(manifest.Secrets) != 3 || !manifest.HeldSecret("admin_password") || manifest.FirstRun.Routine != FirstRunAudiobookshelf {
		t.Fatalf("first run = %+v, secrets = %+v", manifest.FirstRun, manifest.Secrets)
	}
	wantData := []struct {
		target  string
		class   storage.Class
		subpath string
	}{
		{"/config", storage.ClassApplication, "config"},
		{"/metadata", storage.ClassApplication, "metadata"},
		{"/metadata/streams", storage.ClassBulk, "streams"},
	}
	if len(service.Data) != len(wantData) {
		t.Fatalf("data = %+v", service.Data)
	}
	for i, want := range wantData {
		data := service.Data[i]
		if data.Target != want.target || data.Class != want.class || data.Subpath != want.subpath {
			t.Fatalf("data[%d] = %+v, want %+v", i, data, want)
		}
		if data.Ephemeral || data.Database {
			t.Fatalf("data[%d] = %+v, want persistent file copies", i, data)
		}
	}
	if len(manifest.BackupPaths) != 2 ||
		manifest.BackupPaths[0] != "config" || manifest.BackupPaths[1] != "metadata" {
		t.Fatalf("backuppaths = %v, want the database and item metadata", manifest.BackupPaths)
	}
	if manifest.Database == nil || manifest.Database.Kind != DatabaseSQLite ||
		manifest.Database.Service != "audiobookshelf" ||
		manifest.Database.Path != "config/absdatabase.sqlite" {
		t.Fatalf("database = %+v, want the SQLite export", manifest.Database)
	}
	if len(manifest.Requirements.Ports) != 0 || manifest.Services[0].ContainerPort != 80 {
		t.Fatalf("container port = %d, host ports = %v", manifest.Services[0].ContainerPort, manifest.Requirements.Ports)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if manifest.SSO.Class != SSOOptional || len(manifest.SSO.Protocols) != 1 ||
		string(manifest.SSO.Protocols[0]) != "oidc" || manifest.SSO.OIDC == nil || manifest.SSO.OIDC.Adapter != AdapterEnvOIDC ||
		manifest.SSO.OIDC.Env == nil || !manifest.SSO.OIDC.Env.Restart || manifest.SSO.OIDC.Env.Trust != "NODE_EXTRA_CA_CERTS" || manifest.SSO.OIDC.Env.TrustBundle != "" ||
		manifest.SSO.OIDC.Env.AdminValue != "admin" || manifest.SSO.OIDC.Env.UserValue != "user" {
		t.Fatalf("sso = %+v, want optional OIDC through the env adapter (S417)", manifest.SSO)
	}
	if len(manifest.Integrations) != 2 ||
		manifest.Integrations[0].Kind != IntegrationIdentity ||
		manifest.Integrations[0].Target != "authentik" ||
		manifest.Integrations[1].Kind != IntegrationLibrary ||
		manifest.Integrations[1].Target != "nextcloud" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 1024, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 4 << 30,
			storage.ClassBulk:        20 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedNavidrome pins the second Wave 2 package (S354): the pinned
// official image, SQLite on application storage with a consistent export,
// the regenerable cache on bulk, the owner music volume left to the
// install, the documented local-auth exception, and the shared-library
// declaration.
func TestCuratedNavidrome(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("navidrome")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "0.64.2" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 ||
		manifest.Services[0].Image != "docker.io/navidrome/navidrome:0.64.2" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if len(service.Environment) != 1 || service.Environment["ND_CACHEFOLDER"] != "/cache" {
		t.Fatalf("environment = %v, want only the bulk cache folder", service.Environment)
	}
	if len(service.Entrypoint) != 0 || len(manifest.Secrets) != 0 {
		t.Fatalf("navidrome generates its JWT secrets into the database; secrets = %+v", manifest.Secrets)
	}
	wantData := []struct {
		target  string
		class   storage.Class
		subpath string
	}{
		{"/data", storage.ClassApplication, "data"},
		{"/cache", storage.ClassBulk, "cache"},
	}
	if len(service.Data) != len(wantData) {
		t.Fatalf("data = %+v", service.Data)
	}
	for _, data := range service.Data {
		if data.Target == "/music" {
			t.Fatal("/music is an owner volume; declaring it would pull the library into every backup")
		}
	}
	for i, want := range wantData {
		data := service.Data[i]
		if data.Target != want.target || data.Class != want.class || data.Subpath != want.subpath {
			t.Fatalf("data[%d] = %+v, want %+v", i, data, want)
		}
	}
	if len(manifest.BackupPaths) != 1 || manifest.BackupPaths[0] != "data" {
		t.Fatalf("backuppaths = %v", manifest.BackupPaths)
	}
	if manifest.Database == nil || manifest.Database.Kind != DatabaseSQLite ||
		manifest.Database.Service != "navidrome" ||
		manifest.Database.Path != "data/navidrome.db" {
		t.Fatalf("database = %+v, want the SQLite export", manifest.Database)
	}
	if len(manifest.Requirements.Ports) != 0 || manifest.Services[0].ContainerPort != 4533 {
		t.Fatalf("container port = %d, host ports = %v", manifest.Services[0].ContainerPort, manifest.Requirements.Ports)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if manifest.SSO.Class != SSOUnsupported || len(manifest.SSO.Protocols) != 0 {
		t.Fatalf("sso = %+v, want the documented local-auth exception", manifest.SSO)
	}
	if len(manifest.Integrations) != 1 ||
		manifest.Integrations[0].Kind != IntegrationLibrary ||
		manifest.Integrations[0].Target != "jellyfin" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 512, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 4 << 30,
			storage.ClassBulk:        20 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedKomga pins the third Wave 2 package (S355): the pinned
// official image, the JVM heap cap that keeps it inside its declared
// memory, SQLite on application storage with a consistent export, the
// comics library left to the install, optional OIDC, and the identity
// integration.
func TestCuratedKomga(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("komga")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "1.29.1" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 ||
		manifest.Services[0].Image != "docker.io/gotson/komga:1.29.1" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if len(service.Environment) != 1 || service.Environment["JAVA_TOOL_OPTIONS"] != "-Xmx768m" {
		t.Fatalf("environment = %v, want only the JVM heap cap", service.Environment)
	}
	// S411: the supervisor execs the image's own java command.
	if len(service.Entrypoint) != 6 || service.Entrypoint[0] != "java" || service.Entrypoint[4] != "application.jar" ||
		len(manifest.Secrets) != 2 || manifest.Secrets[1].Name != "oidc_client_secret" ||
		len(manifest.Secrets[1].Consumers) != 1 || !manifest.Secrets[1].Consumers[0].Export ||
		manifest.Secrets[1].Consumers[0].Env != "SPRING_SECURITY_OAUTH2_CLIENT_REGISTRATION_AUTHENTIK_CLIENTSECRET_FILE" {
		t.Fatalf("entrypoint = %v, secrets = %+v", service.Entrypoint, manifest.Secrets)
	}
	oidc := manifest.SSO.OIDC
	if oidc == nil || oidc.Adapter != AdapterEnvOIDC || oidc.Service != "komga" || oidc.Env == nil ||
		oidc.Env.Secret != "oidc_client_secret" || oidc.Env.Trust != "JDK_JAVA_OPTIONS" || oidc.Env.TrustBundle != TrustBundleJava ||
		oidc.Env.SignInPath != "/oauth2/authorization/authentik" || len(oidc.Env.RedirectPaths) != 1 ||
		oidc.Env.RedirectPaths[0] != "/login/oauth2/code/authentik" ||
		oidc.Env.Set["KOMGA_OAUTH2ACCOUNTCREATION"] != "true" ||
		oidc.Env.Set["SPRING_SECURITY_OAUTH2_CLIENT_REGISTRATION_AUTHENTIK_CLIENTID"] != "{{client_id}}" ||
		oidc.Env.Set["SPRING_SECURITY_OAUTH2_CLIENT_PROVIDER_AUTHENTIK_JWKSETURI"] != "{{issuer}}jwks/" {
		t.Fatalf("sso = %+v", oidc)
	}
	for key := range oidc.Env.Set {
		if strings.Contains(key, "CLIENTSECRET") || strings.Contains(key, "ISSUERURI") {
			t.Fatalf("env %s: the secret is never set and discovery at startup is avoided", key)
		}
	}
	if scripts := EntrypointScriptsFor(manifest); len(scripts) != 1 || scripts[0].Service != "komga" ||
		!strings.Contains(scripts[0].Body, "export SPRING_SECURITY_OAUTH2_CLIENT_REGISTRATION_AUTHENTIK_CLIENTSECRET\n") {
		t.Fatalf("scripts = %+v, want one komga supervisor exporting the client secret", scripts)
	}
	if len(manifest.Secrets) < 1 || !manifest.HeldSecret("recovery_password") || manifest.FirstRun == nil || manifest.FirstRun.Routine != FirstRunKomga || manifest.FirstRun.Account != "homestead-recovery@localhost.invalid" {
		t.Fatalf("komga recovery first run = %+v, secrets = %+v", manifest.FirstRun, manifest.Secrets)
	}
	wantData := []struct {
		target  string
		class   storage.Class
		subpath string
	}{
		{"/config", storage.ClassApplication, "config"},
	}
	if len(service.Data) != len(wantData) {
		t.Fatalf("data = %+v", service.Data)
	}
	for _, data := range service.Data {
		if data.Target == "/data" {
			t.Fatal("/data is an owner volume; declaring it would pull the comics library into every backup")
		}
	}
	for i, want := range wantData {
		data := service.Data[i]
		if data.Target != want.target || data.Class != want.class || data.Subpath != want.subpath {
			t.Fatalf("data[%d] = %+v, want %+v", i, data, want)
		}
	}
	if len(manifest.BackupPaths) != 1 || manifest.BackupPaths[0] != "config" {
		t.Fatalf("backuppaths = %v", manifest.BackupPaths)
	}
	if manifest.Database == nil || manifest.Database.Kind != DatabaseSQLite ||
		manifest.Database.Service != "komga" ||
		manifest.Database.Path != "config/database.sqlite" {
		t.Fatalf("database = %+v, want the SQLite export", manifest.Database)
	}
	if len(manifest.Requirements.Ports) != 0 || manifest.Services[0].ContainerPort != 25600 {
		t.Fatalf("container port = %d, host ports = %v", manifest.Services[0].ContainerPort, manifest.Requirements.Ports)
	}
	if manifest.Requirements.MemoryMB != 1024 {
		t.Fatalf("memory = %d MB, want the JVM cap to fit inside it", manifest.Requirements.MemoryMB)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if manifest.SSO.Class != SSOOptional || len(manifest.SSO.Protocols) != 1 ||
		manifest.SSO.Protocols[0] != identity.CapabilityOIDC {
		t.Fatalf("sso = %+v, want optional OIDC", manifest.SSO)
	}
	if len(manifest.Integrations) != 1 ||
		manifest.Integrations[0].Kind != IntegrationIdentity ||
		manifest.Integrations[0].Target != "authentik" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 2048, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 8 << 30,
			storage.ClassBulk:        20 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestCuratedCalibreWebAutomated pins the fourth Wave 2 package (S356):
// the pinned community image, the LinuxServer user the declared
// directories match, the library and shared ingest folder on bulk, the
// application databases with a consistent export, optional in-app OIDC,
// and the identity and shared-library declarations.
func TestCuratedCalibreWebAutomated(t *testing.T) {
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("calibre-web-automated")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if manifest.Version != "4.0.8" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 ||
		manifest.Services[0].Image != "docker.io/crocodilestick/calibre-web-automated:v4.0.8" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	// S415: the two sign-in secrets are files for the sign-in script, not
	// exported variables, so the image keeps its own entrypoint.
	if len(service.Entrypoint) != 0 || len(manifest.Secrets) != 2 {
		t.Fatalf("calibre-web-automated keeps the image entrypoint; secrets = %+v", manifest.Secrets)
	}
	for _, secret := range manifest.Secrets {
		for _, c := range secret.Consumers {
			if c.Export {
				t.Fatalf("secret %q is exported", secret.Name)
			}
		}
	}
	if len(service.Environment) != 3 ||
		service.Environment["PUID"] != "1000" || service.Environment["PGID"] != "1000" ||
		service.Environment["HOMESTEAD_CWA_URL"] != "{{app_url}}" {
		t.Fatalf("environment = %v, want PUID/PGID for the abc user and the app address", service.Environment)
	}
	wantData := []struct {
		target  string
		class   storage.Class
		subpath string
	}{
		{"/config", storage.ClassApplication, "config"},
		{"/calibre-library", storage.ClassBulk, "library"},
		{"/cwa-book-ingest", storage.ClassBulk, "ingest"},
	}
	if len(service.Data) != len(wantData) {
		t.Fatalf("data = %+v", service.Data)
	}
	for i, want := range wantData {
		data := service.Data[i]
		if data.Target != want.target || data.Class != want.class || data.Subpath != want.subpath {
			t.Fatalf("data[%d] = %+v, want %+v", i, data, want)
		}
		if data.Ephemeral || data.Database {
			t.Fatalf("data[%d] = %+v, want persistent file copies", i, data)
		}
		if uid, gid := data.Owner(); uid != 1000 || gid != 1000 {
			t.Fatalf("data[%d] owner = %d:%d, want the PUID/PGID user", i, uid, gid)
		}
	}
	if len(manifest.BackupPaths) != 2 ||
		manifest.BackupPaths[0] != "config" || manifest.BackupPaths[1] != "library" {
		t.Fatalf("backuppaths = %v, want the settings and the library", manifest.BackupPaths)
	}
	if manifest.Database == nil || manifest.Database.Kind != DatabaseSQLite ||
		manifest.Database.Service != "calibre-web-automated" ||
		manifest.Database.Path != "config/app.db" {
		t.Fatalf("database = %+v, want the SQLite export", manifest.Database)
	}
	if len(manifest.Requirements.Ports) != 0 || manifest.Services[0].ContainerPort != 8083 {
		t.Fatalf("container port = %d, host ports = %v", manifest.Services[0].ContainerPort, manifest.Requirements.Ports)
	}
	if manifest.Requirements.MemoryMB != 2048 {
		t.Fatalf("memory = %d MB, want room for Calibre conversions", manifest.Requirements.MemoryMB)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if manifest.SSO.Class != SSOOptional || len(manifest.SSO.Protocols) != 1 ||
		manifest.SSO.Protocols[0] != identity.CapabilityOIDC || manifest.SSO.OIDC == nil ||
		manifest.SSO.OIDC.Adapter != AdapterEnvOIDC || !manifest.SSO.OIDC.Env.Restart {
		t.Fatalf("sso = %+v, want optional OIDC through env-oidc with a restart (S415)", manifest.SSO)
	}
	if len(manifest.Integrations) != 2 ||
		manifest.Integrations[0].Kind != IntegrationIdentity ||
		manifest.Integrations[0].Target != "authentik" ||
		manifest.Integrations[1].Kind != IntegrationLibrary ||
		manifest.Integrations[1].Target != "nextcloud" {
		t.Fatalf("integrations = %+v", manifest.Integrations)
	}
	report, err := Evaluate(manifest, HostFacts{
		Arch: "amd64", MemoryMB: 4096, CPUCount: 2,
		FreeSpace: map[storage.Class]uint64{
			storage.ClassApplication: 16 << 30,
			storage.ClassBulk:        20 << 30,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report = %+v", report)
	}
}

// TestPaperlessSignInSecrets pins the sign-in declaration (S413): the
// provider JSON is a derived, immutable secret that starts as "{}" and is
// exported by the supervisor, and the client secret has no consumer.
func TestPaperlessSignInSecrets(t *testing.T) {
	manifest, err := repoCatalog(t).Get("paperless-ngx")
	if err != nil {
		t.Fatal(err)
	}
	env := manifest.SSO.OIDC.Env
	if env == nil || env.SecretFile == nil || env.SecretFile.Secret != "oidc_providers" || env.Secret != "oidc_client_secret" ||
		env.Trust != "REQUESTS_CA_BUNDLE" || env.TrustBundle != TrustBundleCombined {
		t.Fatalf("sso = %+v", env)
	}
	if _, plain := env.Set["PAPERLESS_SOCIALACCOUNT_PROVIDERS"]; plain || strings.Contains(strings.Join(mapValues(env.Set), " "), "secret") {
		t.Fatalf("provider setting or secret in the plain environment: %v", env.Set)
	}
	if _, disabled := env.Set["PAPERLESS_DISABLE_REGULAR_LOGIN"]; disabled {
		t.Fatal("the local login form must stay")
	}
	mem := newMemSecrets(t)
	if err := EnsureSecrets(mem, manifest); err != nil {
		t.Fatal(err)
	}
	if got := mem.values[SecretStoreName("paperless-ngx", "oidc_providers")]; got != "{}" {
		t.Fatalf("initial provider setting = %q", got)
	}
	dir := t.TempDir()
	for _, f := range SecretFilesFor(manifest) {
		if f.Name == "oidc_client_secret" {
			t.Fatal("the client secret must not be a file of its own")
		}
	}
	if err := WriteSecretFiles(dir, SecretFilesFor(manifest), mem); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, SecretsDirName, "webserver_oidc_providers")); err != nil || string(b) != "{}" {
		t.Fatalf("file = %q, %v", b, err)
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// linuxServerPackages are the only packages ADR-056 lets run a
// LinuxServer.io image.
var linuxServerPackages = map[string]bool{"prowlarr": true, "sonarr": true, "radarr": true, "sabnzbd": true, "bookstack": true}

// TestCuratedImageTrust enforces ADR-056 on the shipped catalog: every
// pulled image names its publisher, LinuxServer images are limited to the
// packages the ADR names, community images carry a justification, and
// craft images (built locally, ADR-028) carry none.
func TestCuratedImageTrust(t *testing.T) {
	catalog := repoCatalog(t)
	for _, name := range catalog.Names() {
		manifest, err := catalog.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, service := range manifest.Services {
			where := name + "/" + service.Name
			if _, _, isCraft := craft.ParseRef(service.Image); isCraft {
				if service.Publisher != "" {
					t.Errorf("%s: locally built craft image must not declare a publisher", where)
				}
				continue
			}
			switch service.Publisher {
			case PublisherUpstream:
			case PublisherLinuxServer:
				if !linuxServerPackages[name] {
					t.Errorf("%s: LinuxServer.io images are limited to %v (ADR-056)", where, linuxServerPackages)
				}
				if !strings.Contains(service.Image, "linuxserver/") {
					t.Errorf("%s: publisher linuxserver but image %q is not linuxserver/", where, service.Image)
				}
			case PublisherCommunity:
				if service.Justification == "" {
					t.Errorf("%s: community image without justification", where)
				}
			default:
				t.Errorf("%s: image %q must declare publisher upstream, linuxserver or community (ADR-056)", where, service.Image)
			}
			if service.Publisher != PublisherLinuxServer && strings.Contains(service.Image, "linuxserver/") {
				t.Errorf("%s: a linuxserver/ image must declare publisher linuxserver", where)
			}
		}
	}
	cwa, err := catalog.Get("calibre-web-automated")
	if err != nil {
		t.Fatal(err)
	}
	if cwa.Services[0].Publisher != PublisherCommunity {
		t.Errorf("calibre-web-automated must be classified community, got %q", cwa.Services[0].Publisher)
	}
}
