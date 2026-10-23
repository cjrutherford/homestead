package catalog

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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
var curatedPackages = []string{"audiobookshelf", "authentik", "cadcraft", "deckcraft", "designcraft", "effectcraft", "figcraft", "filmcraft", "freshrss", "gitea", "gridcraft", "immich", "jellyfin", "lightcraft", "linkding", "mealie", "navidrome", "nextcloud", "paperless-ngx", "pdfcraft", "photocraft", "soundcraft", "uptime-kuma", "vaultwarden", "vectorcraft", "wordcraft"}

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
	if manifest.Requirements.MemoryMB < 1024 || len(manifest.Requirements.Ports) != 1 {
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
	if manifest.Version != "1.34.3" {
		t.Fatalf("version = %q", manifest.Version)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].Image != "vaultwarden/server:1.34.3" {
		t.Fatalf("services = %+v", manifest.Services)
	}
	service := manifest.Services[0]
	if service.Environment["SIGNUPS_ALLOWED"] != "true" {
		t.Fatalf("signups = %q, want owner signup open", service.Environment["SIGNUPS_ALLOWED"])
	}
	if _, enabled := service.Environment["ADMIN_TOKEN"]; enabled {
		t.Fatal("the admin page must stay off by default")
	}
	if !strings.HasPrefix(service.Environment["DOMAIN"], "https://") ||
		!strings.Contains(service.Environment["DOMAIN"], RouteHostToken) {
		t.Fatalf("domain = %q, want the routed HTTPS host", service.Environment["DOMAIN"])
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
	if root := service.Environment["GITEA__server__ROOT_URL"]; !strings.HasPrefix(root, "https://") ||
		!strings.Contains(root, RouteHostToken) || !strings.HasSuffix(root, "/") {
		t.Fatalf("ROOT_URL = %q, want the routed HTTPS host with a trailing slash", root)
	}
	if service.Environment["GITEA__service__DISABLE_REGISTRATION"] != "false" {
		t.Fatal("registration must stay open until the owner creates the first account")
	}
	if len(service.Data) != 1 || service.Data[0].Target != "/data" ||
		service.Data[0].Class != storage.ClassApplication || service.Data[0].Subpath != "data" ||
		service.Data[0].UID == nil || *service.Data[0].UID != 1000 {
		t.Fatalf("data = %+v", service.Data)
	}
	if len(service.Entrypoint) != 0 || len(manifest.Secrets) != 0 {
		t.Fatalf("gitea generates its keys into the data volume; secrets = %+v", manifest.Secrets)
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
	if manifest.Database != nil || len(manifest.Secrets) != 0 || len(service.Entrypoint) != 0 {
		t.Fatalf("freshrss installs through the web form; secrets = %+v, database = %+v",
			manifest.Secrets, manifest.Database)
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
	if origins := service.Environment["LD_CSRF_TRUSTED_ORIGINS"]; !strings.HasPrefix(origins, "https://") ||
		!strings.Contains(origins, RouteHostToken) {
		t.Fatalf("LD_CSRF_TRUSTED_ORIGINS = %q, want the routed HTTPS origin", origins)
	}
	if service.Environment["LD_SUPERUSER_NAME"] != "admin" {
		t.Fatalf("superuser name = %q", service.Environment["LD_SUPERUSER_NAME"])
	}
	if _, plain := service.Environment["LD_SUPERUSER_PASSWORD"]; plain {
		t.Fatal("the superuser password must not be plain environment")
	}
	var exported bool
	for _, decl := range manifest.Secrets {
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
	if scripts := EntrypointScriptsFor(manifest); len(scripts) != 1 || scripts[0].Service != "linkding" {
		t.Fatalf("scripts = %+v, want one linkding supervisor", scripts)
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
	if base := service.Environment["BASE_URL"]; !strings.HasPrefix(base, "https://") ||
		!strings.Contains(base, RouteHostToken) {
		t.Fatalf("BASE_URL = %q, want the routed HTTPS host", base)
	}
	if service.Environment["DB_ENGINE"] != "sqlite" {
		t.Fatalf("DB_ENGINE = %q", service.Environment["DB_ENGINE"])
	}
	if service.Environment["ALLOW_SIGNUP"] != "false" {
		t.Fatal("signups stay closed; the seeded administrator creates accounts")
	}
	if len(service.Entrypoint) != 0 || len(manifest.Secrets) != 0 {
		t.Fatalf("mealie needs no supervisor; secrets = %+v", manifest.Secrets)
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
	if len(service.Entrypoint) != 0 || len(manifest.Secrets) != 0 {
		t.Fatalf("audiobookshelf generates its JWT secret into the database; secrets = %+v", manifest.Secrets)
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
	if len(manifest.Requirements.Ports) != 1 || manifest.Requirements.Ports[0] != 80 {
		t.Fatalf("ports = %v", manifest.Requirements.Ports)
	}
	endpoint, ok := manifest.Exposure.Find("web")
	if !ok || string(endpoint.Recommended) != "local" || !endpoint.AllowWider || len(endpoint.Warnings) == 0 {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if manifest.SSO.Class != SSOOptional || len(manifest.SSO.Protocols) != 1 ||
		string(manifest.SSO.Protocols[0]) != "oidc" || manifest.SSO.OIDC != nil {
		t.Fatalf("sso = %+v, want optional in-app OIDC with no actuated client", manifest.SSO)
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
	if len(manifest.Requirements.Ports) != 1 || manifest.Requirements.Ports[0] != 4533 {
		t.Fatalf("ports = %v", manifest.Requirements.Ports)
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
