package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homestead/internal/craft"
	"homestead/internal/docker"
	"homestead/internal/storage"
)

const craftTag = "homestead/craft-wordcraft:1.2.0"

func withCraftSeams(t *testing.T, recorded map[string]string, live map[string]string) {
	t.Helper()
	oldCheck, oldRec := CraftChecker, CraftRecordedID
	CraftRecordedID = func(image string) (string, bool) { id, ok := recorded[image]; return id, ok }
	CraftChecker = func(_ context.Context, image string) error {
		want, ok := recorded[image]
		if !ok {
			return errors.New("no recorded local build")
		}
		if live[image] != want {
			return errors.New("local image does not match the recorded digest")
		}
		return nil
	}
	t.Cleanup(func() { CraftChecker, CraftRecordedID = oldCheck, oldRec })
}

func TestImageDriftUsesRecordedLocalDigest(t *testing.T) {
	const id = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	withCraftSeams(t, map[string]string{craftTag: id}, nil)
	none := func() []docker.Image { return nil }
	if got := imageDrift(craftTag, docker.Container{Image: craftTag, ImageID: id}, none); got != "" {
		t.Fatalf("matching recorded digest reported drift: %s", got)
	}
	other := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	got := imageDrift(craftTag, docker.Container{Image: craftTag, ImageID: other}, none)
	if !strings.Contains(got, "not the recorded local build") {
		t.Fatalf("replaced image not reported: %q", got)
	}
	// A container whose Image reads as the ID itself (tag moved) is judged too.
	if got := imageDrift(craftTag, docker.Container{Image: other}, none); got == "" {
		t.Fatal("moved tag not reported")
	}
	// No record: reported, not silently clean.
	if got := imageDrift("homestead/craft-gridcraft:1.0.0", docker.Container{Image: "x", ImageID: id}, none); !strings.Contains(got, "no recorded local build") {
		t.Fatalf("missing record: %q", got)
	}
}

func writeCompose(t *testing.T, image string) string {
	t.Helper()
	dir := t.TempDir()
	body := "name: wordcraft\nservices:\n  web:\n    image: " + image + "\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestComposeUpRefusesCraftImageMismatch(t *testing.T) {
	id := "sha256:" + strings.Repeat("a", 64)
	withCraftSeams(t, map[string]string{craftTag: id}, map[string]string{craftTag: "sha256:" + strings.Repeat("b", 64)})
	ran := false
	old := composeRun
	composeRun = func(context.Context, string, string, ...string) error { ran = true; return nil }
	t.Cleanup(func() { composeRun = old })
	dir := writeCompose(t, craftTag)
	err := composeUp(context.Background(), dir, "wordcraft")
	if err == nil || !strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("err = %v, want refusal", err)
	}
	if ran {
		t.Fatal("compose ran despite the mismatch")
	}
	// Matching digest starts.
	withCraftSeams(t, map[string]string{craftTag: id}, map[string]string{craftTag: id})
	if err := composeUp(context.Background(), dir, "wordcraft"); err != nil || !ran {
		t.Fatalf("matching image refused: %v", err)
	}
	// Non-craft stacks are untouched even without seams.
	CraftChecker = nil
	ran = false
	if err := composeUp(context.Background(), writeCompose(t, "nginx:1.27"), "other"); err != nil || !ran {
		t.Fatalf("non-craft stack affected: %v", err)
	}
	// Craft image with no checker wired fails closed.
	if err := composeUp(context.Background(), dir, "wordcraft"); err == nil {
		t.Fatal("craft image started without a checker")
	}
}

func TestInstallDefinitionGatesCraftImages(t *testing.T) {
	plan := installPlan(t, t.TempDir())
	plan.Compose.Images = []string{craftTag}
	registry := installRegistry(t)
	st := openDriftStore(t)
	stubComposeUp(t, false)
	def, err := InstallDefinition(plan, registry, t.TempDir(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if def.Steps[0].Name != "verify-craft-images" {
		t.Fatalf("first step = %q", def.Steps[0].Name)
	}
	withCraftSeams(t, map[string]string{craftTag: "sha256:" + strings.Repeat("a", 64)}, nil)
	if err := def.Steps[0].Run(context.Background()); err == nil {
		t.Fatal("unbuilt/mismatched craft image passed the install gate")
	}
}

func TestComposeUpCraftGateFailsClosed(t *testing.T) {
	id := "sha256:" + strings.Repeat("a", 64)
	withCraftSeams(t, map[string]string{craftTag: id}, map[string]string{craftTag: "sha256:" + strings.Repeat("b", 64)})
	ran := false
	old := composeRun
	composeRun = func(context.Context, string, string, ...string) error { ran = true; return nil }
	t.Cleanup(func() { composeRun = old })
	refuse := func(name, compose, env string) {
		t.Helper()
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644)
		if env != "" {
			_ = os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o644)
		}
		ran = false
		if err := composeUp(context.Background(), dir, "p"); err == nil || ran {
			t.Errorf("%s: started (err=%v ran=%v)", name, err, ran)
		}
	}
	refuse("interpolated from .env", "services:\n  web:\n    image: ${IMG}\n", "IMG="+craftTag+"\n")
	refuse("default value", "services:\n  web:\n    image: ${IMG:-"+craftTag+"}\n", "")
	refuse("bare $VAR", "services:\n  web:\n    image: $IMG\n", "IMG="+craftTag+"\n")
	refuse("unparseable compose", "services: [unclosed\n  image: "+craftTag, "")
	refuse("craft name outside a service image", "services:\n  web:\n    image: nginx:1\n    labels:\n      x: "+craftTag+"\n", "")
	t.Setenv("CRAFT_IMG_FROM_ENV", craftTag)
	refuse("daemon environment", "services:\n  web:\n    image: ${CRAFT_IMG_FROM_ENV}\n", "")
	// A matching recorded digest still starts through interpolation.
	withCraftSeams(t, map[string]string{craftTag: id}, map[string]string{craftTag: id})
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: ${IMG}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("IMG="+craftTag+"\n"), 0o644)
	ran = false
	if err := composeUp(context.Background(), dir, "p"); err != nil || !ran {
		t.Fatalf("matching interpolated image refused: %v", err)
	}
}

func TestComposeUpCraftGateSeesEveryFileComposeCouldLoad(t *testing.T) {
	id := "sha256:" + strings.Repeat("a", 64)
	withCraftSeams(t, map[string]string{craftTag: id}, map[string]string{craftTag: "sha256:" + strings.Repeat("b", 64)})
	ran := false
	old := composeRun
	composeRun = func(context.Context, string, string, ...string) error { ran = true; return nil }
	t.Cleanup(func() { composeRun = old })
	craftBody := "services:\n  web:\n    image: " + craftTag + "\n"
	cases := map[string]map[string]string{
		"compose.yml":                 {"compose.yml": craftBody},
		"docker-compose.yaml":         {"docker-compose.yaml": craftBody},
		"docker-compose.override.yml": {"compose.yaml": "services: {}\n", "docker-compose.override.yml": craftBody},
		"COMPOSE_FILE in .env":        {"other.yaml": craftBody, ".env": "COMPOSE_FILE=other.yaml\n"},
		"COMPOSE_FILE list":           {"compose.yaml": "services: {}\n", "extra.yaml": craftBody, ".env": "COMPOSE_FILE=compose.yaml:extra.yaml\n"},
	}
	for name, files := range cases {
		dir := t.TempDir()
		for file, body := range files {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		ran = false
		if err := composeUp(context.Background(), dir, "p"); err == nil || ran {
			t.Errorf("%s: started a craft stack past the gate (err=%v ran=%v)", name, err, ran)
		}
	}
	// COMPOSE_FILE from the daemon environment is honoured too.
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "env.yaml"), []byte(craftBody), 0o644)
	t.Setenv("COMPOSE_FILE", "env.yaml")
	ran = false
	if err := composeUp(context.Background(), dir, "p"); err == nil || ran {
		t.Errorf("daemon COMPOSE_FILE: started (err=%v ran=%v)", err, ran)
	}
}

func TestSameImageID(t *testing.T) {
	full := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{full, full, true},
		{strings.Repeat("a", 12), full, true},
		{"sha256:" + strings.Repeat("a", 12), full, true},
		{strings.Repeat("a", 11), full, false},
		{"sha256:a", full, false},
		{"", "", false},
		{"sha256:" + strings.Repeat("b", 64), full, false},
	} {
		if got := sameImageID(tc.a, tc.b); got != tc.want {
			t.Errorf("sameImageID(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

// composeUpPaths lists every Compose start the catalog package performs
// for an installed stack, as callers of the craft gate: the full up, the
// database-only up used by migrations, and the restart of stopped
// containers used by migration rollback. A craft service must not start
// through any of them without a matching recorded image (S249).
func composeUpPaths() map[string]func(ctx context.Context, dir, project string) error {
	return map[string]func(ctx context.Context, dir, project string) error{
		"composeUp": composeUp,
		"composeUpServices": func(ctx context.Context, dir, project string) error {
			return composeUpServices(ctx, dir, project, "web")
		},
		"composeStart": composeStart,
		"restartConsumers": func(ctx context.Context, dir, project string) error {
			return restartConsumers(ctx, dir, project, SecretDecl{})
		},
	}
}

func TestEveryComposeStartPathTakesTheCraftGate(t *testing.T) {
	id := "sha256:" + strings.Repeat("a", 64)
	other := "sha256:" + strings.Repeat("b", 64)
	for name, start := range composeUpPaths() {
		t.Run(name, func(t *testing.T) {
			old := composeRun
			ran := 0
			composeRun = func(context.Context, string, string, ...string) error { ran++; return nil }
			t.Cleanup(func() { composeRun = old })
			dir := writeCompose(t, craftTag)

			// Mismatched recorded image: refused, compose never runs.
			withCraftSeams(t, map[string]string{craftTag: id}, map[string]string{craftTag: other})
			if err := start(context.Background(), dir, "wordcraft"); err == nil || !strings.Contains(err.Error(), "refusing to start") {
				t.Fatalf("mismatched image: err = %v", err)
			}
			// Missing record: refused.
			withCraftSeams(t, map[string]string{}, map[string]string{})
			if err := start(context.Background(), dir, "wordcraft"); err == nil {
				t.Fatal("started with no recorded build")
			}
			// No checker wired at all: fails closed.
			CraftChecker = nil
			if err := start(context.Background(), dir, "wordcraft"); err == nil {
				t.Fatal("started with no craft checker")
			}
			if ran != 0 {
				t.Fatalf("compose ran %d times despite the refusals", ran)
			}
			// A matching recorded image starts.
			withCraftSeams(t, map[string]string{craftTag: id}, map[string]string{craftTag: id})
			if err := start(context.Background(), dir, "wordcraft"); err != nil || ran != 1 {
				t.Fatalf("matching image: err = %v ran = %d", err, ran)
			}
		})
	}
}

func TestComposeStartPathsLeaveNonCraftStacksAlone(t *testing.T) {
	old := composeRun
	t.Cleanup(func() { composeRun = old })
	CraftChecker = nil
	for name, start := range composeUpPaths() {
		ran := false
		composeRun = func(context.Context, string, string, ...string) error { ran = true; return nil }
		if err := start(context.Background(), writeCompose(t, "nginx:1.27"), "other"); err != nil || !ran {
			t.Errorf("%s: non-craft stack affected: err = %v ran = %v", name, err, ran)
		}
	}
}

func craftManifest(t *testing.T) Manifest {
	t.Helper()
	m := validManifest()
	m.Name, m.Version = "wordcraft", "0.7.0"
	m.Services = []Service{{Name: "wordcraft", Image: "homestead/craft-wordcraft:0.7.0", Exposes: "web",
		Data: []DataPath{{Target: "/tmp", Ephemeral: true, Reason: "nginx pid and temp files"}}}}
	m.Exposure.Package = "wordcraft"
	m.Requirements.Storage = nil
	m.BackupPaths = nil
	return m
}

func TestCraftImageValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manifest)
		want   string // empty means valid
	}{
		{"local tag", func(*Manifest) {}, ""},
		{"docker hub qualified", func(m *Manifest) { m.Services[0].Image = "docker.io/homestead/craft-wordcraft:0.7.0" }, "never pulled from a registry"},
		{"registry qualified", func(m *Manifest) { m.Services[0].Image = "ghcr.io/homestead/craft-wordcraft:0.7.0" }, "never pulled from a registry"},
		{"registry with port", func(m *Manifest) { m.Services[0].Image = "registry.local:5000/homestead/craft-wordcraft:0.7.0" }, "never pulled from a registry"},
		{"latest", func(m *Manifest) { m.Services[0].Image = "homestead/craft-wordcraft:latest" }, "non-latest tag"},
		{"digest pinned", func(m *Manifest) { m.Services[0].Image = "homestead/craft-wordcraft@sha256:" + strings.Repeat("a", 64) }, "local tag"},
		{"not a version", func(m *Manifest) { m.Services[0].Image = "homestead/craft-wordcraft:nightly" }, "never pulled from a registry"},
		{"other app", func(m *Manifest) { m.Services[0].Image = "homestead/craft-gridcraft:0.7.0" }, "own craft app"},
		{"version differs from manifest", func(m *Manifest) { m.Services[0].Image = "homestead/craft-wordcraft:0.8.0" }, "manifest version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := craftManifest(t)
			tc.mutate(&m)
			err := m.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("valid craft manifest refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateCraftListing(t *testing.T) {
	m := craftManifest(t)
	listed := func(app, version string) bool { return app == "wordcraft" && version == "0.7.0" }
	if err := ValidateCraftListing(m, listed); err != nil {
		t.Fatalf("listed image refused: %v", err)
	}
	m.Version, m.Services[0].Image = "0.8.0", "homestead/craft-wordcraft:0.8.0"
	if err := ValidateCraftListing(m, listed); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("unlisted image err = %v", err)
	}
	// A non-craft package has nothing to list.
	if err := ValidateCraftListing(validManifest(), func(string, string) bool { return false }); err != nil {
		t.Fatalf("non-craft package: %v", err)
	}
}

func TestStateless(t *testing.T) {
	m := craftManifest(t)
	if !m.Stateless() {
		t.Fatal("ephemeral-only manifest is not stateless")
	}
	m.Services[0].Data = nil
	if !m.Stateless() {
		t.Fatal("manifest with no data declarations is not stateless")
	}
	persistent := craftManifest(t)
	persistent.Services[0].Data = append(persistent.Services[0].Data,
		DataPath{Target: "/docs", Class: storage.ClassApplication, Subpath: "docs"})
	if persistent.Stateless() {
		t.Fatal("manifest with a persistent path is stateless")
	}
	withBackup := craftManifest(t)
	withBackup.BackupPaths = []string{"docs"}
	if withBackup.Stateless() {
		t.Fatal("manifest naming backup paths is stateless")
	}
	withDB := craftManifest(t)
	withDB.Database = &DatabaseBackup{Service: "wordcraft", Kind: DatabaseSQLite, Path: "x.db"}
	if withDB.Stateless() {
		t.Fatal("manifest with a database export is stateless")
	}
	if validManifest().Stateless() {
		t.Fatal("jellyfin-shaped manifest is stateless")
	}
}

func TestPlanInstallAttachesRoutedStacksToTheEdgeNetwork(t *testing.T) {
	// S279 found that the edge route to an installed app answered nothing:
	// a request that listed only a package network (the CLI and acceptance
	// flows) produced a stack the proxy could not reach. The planner now
	// always joins the edge network.
	for name, networks := range map[string][]NetworkAttachment{
		"none":          nil,
		"package net":   {{Name: "wordcraft-net"}},
		"edge as given": {{Name: "homestead-edge", External: true}},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := PlanInstall(InstallRequest{
				Manifest: craftManifest(t), Host: installHost(), Domain: "lan",
				ContainerPorts: map[string]int{"wordcraft": 8080}, Networks: networks,
			})
			if err != nil {
				t.Fatalf("PlanInstall: %v", err)
			}
			var doc struct {
				Services map[string]struct {
					Networks []string `yaml:"networks"`
				} `yaml:"services"`
				Networks map[string]struct {
					External bool `yaml:"external"`
				} `yaml:"networks"`
			}
			if err := yaml.Unmarshal([]byte(plan.Compose.ComposeYAML), &doc); err != nil {
				t.Fatal(err)
			}
			got := doc.Services["wordcraft"].Networks
			found := false
			for _, n := range got {
				found = found || n == "homestead-edge"
			}
			if !found || !doc.Networks["homestead-edge"].External {
				t.Fatalf("service networks %v, top-level %v: edge missing or not external\n%s", got, doc.Networks, plan.Compose.ComposeYAML)
			}
			if name == "none" && (len(got) != 2 || got[0] != "default") {
				t.Fatalf("project default network not kept: %v", got)
			}
			if name == "package net" && (len(got) != 2 || got[0] != "wordcraft-net") {
				t.Fatalf("owner network not kept: %v", got)
			}
			if len(plan.Routes) != 1 || plan.Routes[0].Target.Host != "wordcraft" || plan.Routes[0].Target.Port != 8080 {
				t.Fatalf("routes = %+v", plan.Routes)
			}
		})
	}
}

func TestPlanInstallJoinsOnlyRoutedServicesToTheEdgeNetwork(t *testing.T) {
	m := craftManifest(t)
	m.Services = append(m.Services, Service{Name: "cache", Image: "redis:7@sha256:" + strings.Repeat("a", 64),
		Data: []DataPath{{Target: "/data", Ephemeral: true, Reason: "scratch"}}})
	for name, networks := range map[string][]NetworkAttachment{
		"none":        nil,
		"package net": {{Name: "wordcraft-net"}},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := PlanInstall(InstallRequest{
				Manifest: m, Host: installHost(), Domain: "lan",
				ContainerPorts: map[string]int{"wordcraft": 8080}, Networks: networks,
			})
			if err != nil {
				t.Fatalf("PlanInstall: %v", err)
			}
			var doc struct {
				Services map[string]struct {
					Networks []string `yaml:"networks"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal([]byte(plan.Compose.ComposeYAML), &doc); err != nil {
				t.Fatal(err)
			}
			has := func(service string) bool {
				for _, n := range doc.Services[service].Networks {
					if n == "homestead-edge" {
						return true
					}
				}
				return false
			}
			if !has("wordcraft") || has("cache") || len(doc.Services["cache"].Networks) == 0 {
				t.Fatalf("routed service must join the edge, the cache must not:\n%s", plan.Compose.ComposeYAML)
			}
		})
	}
}

func TestPlanInstallRefusesOwnerVolumesOnAStatelessPackage(t *testing.T) {
	_, err := PlanInstall(InstallRequest{
		Manifest: craftManifest(t), Host: installHost(), Domain: "lan",
		ContainerPorts: map[string]int{"wordcraft": 8080},
		Storage:        map[storage.Class]string{storage.ClassApplication: "/mnt/data"},
		Volumes:        []VolumeSpec{{Service: "wordcraft", Class: storage.ClassApplication, Subpath: "docs", Target: "/docs"}},
	})
	if err == nil || !strings.Contains(err.Error(), "stateless") {
		t.Fatalf("err = %v, want a stateless refusal", err)
	}
}

// craftPackages lists the packages that follow the static craft shape
// (S249). Each craft slice (S250 to S253) appends its app here and adds
// its YAML, manifest entry, knownImageVolumes row and THIRD_PARTY.md
// entry; nothing else in this file changes.
var craftPackages = []string{"wordcraft", "gridcraft", "pdfcraft", "deckcraft", "vectorcraft", "photocraft", "lightcraft", "figcraft"}

// craftContainerPort is the port the shared nginx.conf listens on.
const craftContainerPort = 8080

func loadShippedCraftManifest(t *testing.T) craft.Manifest {
	t.Helper()
	raw, err := os.ReadFile("../../catalog/craft/craft-manifest.yaml")
	if err != nil {
		t.Fatalf("shipped craft manifest: %v", err)
	}
	m, err := craft.ParseManifest(raw)
	if err != nil {
		t.Fatalf("shipped craft manifest: %v", err)
	}
	return m
}

func TestShippedCraftManifestMatchesTheBuildContext(t *testing.T) {
	digest, err := craft.TreeDigest("../../images/craft")
	if err != nil {
		t.Fatal(err)
	}
	if got := loadShippedCraftManifest(t).ContextDigest; got != digest {
		t.Fatalf("catalog/craft/craft-manifest.yaml records context digest %s but images/craft digests to %s; "+
			"re-run scripts/craft-manifest.sh (docs/architecture/CRAFT_SIGNING_RUNBOOK.md) and have the owner re-sign", got, digest)
	}
}

func TestCraftPackages(t *testing.T) {
	cat := repoCatalog(t)
	shipped := loadShippedCraftManifest(t)
	thirdParty, err := os.ReadFile("../../docs/THIRD_PARTY.md")
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]string{}
	for _, name := range craftPackages {
		t.Run(name, func(t *testing.T) {
			m, err := cat.Get(name)
			if err != nil {
				t.Fatal(err)
			}
			// Local craft tag, listed (app and version) in the craft manifest.
			images := m.CraftImages()
			if len(images) != 1 || images[0] != "homestead/craft-"+name+":"+m.Version {
				t.Fatalf("craft images = %v", images)
			}
			var entry *craft.App
			for i := range shipped.Apps {
				if shipped.Apps[i].Name == name && shipped.Apps[i].Version == m.Version {
					entry = &shipped.Apps[i]
				}
			}
			if entry == nil {
				t.Fatalf("%s %s is not listed in catalog/craft/craft-manifest.yaml", name, m.Version)
			}
			if err := ValidateCraftListing(m, func(app, version string) bool {
				for _, a := range shipped.Apps {
					if a.Name == app && a.Version == version {
						return true
					}
				}
				return false
			}); err != nil {
				t.Fatal(err)
			}
			// Own host and port: one service, one default host, the shared
			// container port, no other package claiming the host.
			if len(m.Services) != 1 || m.Services[0].Exposes != "web" {
				t.Fatalf("services = %+v", m.Services)
			}
			host := defaultHostname(m, "web", "lan", true)
			if other, taken := hosts[host]; taken {
				t.Fatalf("host %s is also claimed by %s", host, other)
			}
			hosts[host] = name
			// No host port: the container publishes none (edge-only), and the
			// daemon itself listens on 127.0.0.1:8080, so declaring the
			// container port as a requirement refused every install.
			if len(m.Requirements.Ports) != 0 {
				t.Fatalf("craft packages declare no host ports: %v", m.Requirements.Ports)
			}
			hostFacts := installHost()
			hostFacts.UsedPorts = map[int]bool{craftContainerPort: true}
			if report, err := Evaluate(m, hostFacts); err != nil || !report.Passed {
				t.Fatalf("requirements with %d bound on the host = %+v, %v", craftContainerPort, report, err)
			}
			if len(m.Requirements.Storage) != 0 {
				t.Fatalf("craft packages need no storage class: %+v", m.Requirements.Storage)
			}
			// Only ephemeral data; stateless; nothing backed up.
			for _, data := range m.Services[0].Data {
				if !data.Ephemeral {
					t.Fatalf("persistent data %q in a craft package", data.Target)
				}
			}
			if !m.Stateless() || len(m.BackupPaths) != 0 || m.Database != nil {
				t.Fatal("craft package is not stateless")
			}
			// Local-only exposure that cannot be widened by the package.
			endpoint, ok := m.Exposure.Find("web")
			if !ok || string(endpoint.Recommended) != "local" || endpoint.AllowWider || len(endpoint.Warnings) == 0 {
				t.Fatalf("endpoint = %+v", endpoint)
			}
			// The package does not describe itself as clean-room.
			for _, text := range []string{m.Title, m.Blurb, m.Description} {
				if strings.Contains(strings.ToLower(text), "clean-room") || strings.Contains(strings.ToLower(text), "clean room") {
					t.Fatalf("package text calls itself clean-room: %q", text)
				}
			}
			// A verified VOLUME row for exactly this tag, with no VOLUME to cover.
			row, ok := knownImageVolumes[images[0]]
			if !ok {
				t.Fatalf("%s missing from knownImageVolumes", images[0])
			}
			if len(row.Volumes) != 0 {
				t.Fatalf("craft image declares VOLUMEs %v; the shared shape has none", row.Volumes)
			}
			// A licence entry in THIRD_PARTY.md for this release and artifact.
			doc := string(thirdParty)
			for _, want := range []string{"storytold/" + name, m.Version, entry.Artifact.SHA256, entry.Artifact.Name} {
				if !strings.Contains(doc, want) {
					t.Fatalf("docs/THIRD_PARTY.md has no %q for %s", want, name)
				}
			}
			// Zero-mount install: no storage assignment demanded, tmpfs only.
			plan, err := PlanInstall(InstallRequest{
				Manifest: m, Host: HostFacts{Arch: "amd64", MemoryMB: 1024, CPUCount: 2}, Domain: "lan",
				ContainerPorts: map[string]int{name: craftContainerPort},
			})
			if err != nil {
				t.Fatalf("zero-mount install refused: %v", err)
			}
			if len(plan.Directories) != 0 || len(plan.Mounts) != len(m.Services[0].Data) {
				t.Fatalf("directories = %+v mounts = %+v", plan.Directories, plan.Mounts)
			}
			if strings.Contains(plan.Compose.ComposeYAML, "type: bind") || !strings.Contains(plan.Compose.ComposeYAML, "type: tmpfs") {
				t.Fatalf("compose must be tmpfs only:\n%s", plan.Compose.ComposeYAML)
			}
			if len(plan.Routes) != 1 || plan.Routes[0].Host != docker.Hostname(host) || plan.Routes[0].TLS != docker.TLSInternal {
				t.Fatalf("routes = %+v", plan.Routes)
			}
			if !strings.Contains(strings.Join(plan.Notes, "\n"), "built locally") {
				t.Fatalf("notes = %v", plan.Notes)
			}
			// Listed in the shared curated set.
			found := false
			for _, curated := range curatedPackages {
				found = found || curated == name
			}
			if !found {
				t.Fatalf("%s is missing from curatedPackages", name)
			}
		})
	}
}

// TestCraftBuildContextEnforcesTheSharedShape pins the shared image rules
// in the build context itself (S249): non-root nginx on 8080, a /healthz,
// a runtime healthcheck, precompressed assets and no state.
func TestCraftBuildContextEnforcesTheSharedShape(t *testing.T) {
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join("../../images/craft", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	dockerfile, conf := read("Dockerfile"), read("nginx.conf")
	for _, want := range []string{"\nUSER nginx\n", "EXPOSE 8080", "HEALTHCHECK", "/healthz", "gzip -9 -n"} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("Dockerfile lacks %q", want)
		}
	}
	for _, banned := range []string{"\nVOLUME", "\nUSER root"} {
		if strings.Contains(dockerfile, banned) {
			t.Errorf("Dockerfile contains %q", strings.TrimSpace(banned))
		}
	}
	for _, want := range []string{"listen 8080", "location = /healthz", "gzip_static on", "application/wasm", "pid /tmp/nginx.pid", "immutable"} {
		if !strings.Contains(conf, want) {
			t.Errorf("nginx.conf lacks %q", want)
		}
	}
	for _, banned := range []string{"proxy_pass", "fastcgi_pass", "listen 80;", "listen 80 "} {
		if strings.Contains(conf, banned) {
			t.Errorf("nginx.conf contains %q", banned)
		}
	}
}
