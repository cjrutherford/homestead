package catalog

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"homestead/internal/docker"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// imageVolumes is one table row: the VOLUMEs an image declares and
// whether they were confirmed by inspecting exactly that image.
type imageVolumes struct {
	Volumes []string
	// Unverified rows come from upstream Dockerfiles and compose files
	// (or an adjacent tag inspected locally); the KVM run inspects
	// these images on the VM and flips the flag.
	Unverified bool
}

// knownImageVolumes records each curated image's VOLUMEs (S276).
// Image inspection is unavailable at test time in general, so this
// table is the source of truth the coverage test compares declarations
// against. Verified rows were inspected locally for that exact tag
// (postgres:16-alpine). Unverified rows: jellyfin 12.2 was checked via
// the adjacent local jellyfin/jellyfin:12 and authentik 2026.8.3 via
// local 2024.12.3 (neither has a VOLUME); the rest come from upstream
// documentation. Bumping an image tag in catalog/*.yaml fails the
// table-membership test until someone re-verifies the image's VOLUMEs
// and updates the row - that is the point.
var knownImageVolumes = map[string]imageVolumes{
	"jellyfin/jellyfin:12.2":                                         {Volumes: []string{"/config", "/cache"}, Unverified: true},
	"nextcloud:35.0.1-apache":                                        {Volumes: []string{"/var/www/html"}, Unverified: true},
	"ghcr.io/immich-app/immich-server:v3.3.0":                        {Volumes: []string{"/data"}, Unverified: true},
	"ghcr.io/immich-app/immich-machine-learning:v3.3.0":              {Unverified: true},
	"ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0": {Volumes: []string{"/var/lib/postgresql/data"}, Unverified: true},
	"valkey/valkey:9":                                                {Volumes: []string{"/data"}, Unverified: true},
	"ghcr.io/goauthentik/server:2026.8.3":                            {Unverified: true},
	"docker.io/library/postgres:16-alpine":                           {Volumes: []string{"/var/lib/postgresql/data"}},
}

func loadCurated(t *testing.T) Catalog {
	t.Helper()
	cat, err := Load("../../catalog")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cat
}

func TestCuratedImageVolumesCovered(t *testing.T) {
	cat := loadCurated(t)
	for _, name := range cat.Names() {
		manifest, _ := cat.Get(name)
		for _, service := range manifest.Services {
			row, ok := knownImageVolumes[service.Image]
			volumes := row.Volumes
			if !ok {
				t.Errorf("%s/%s: image %s missing from knownImageVolumes; verify its VOLUMEs and add a row", name, service.Name, service.Image)
				continue
			}
			declared := map[string]bool{}
			for _, data := range service.Data {
				declared[data.Target] = true
			}
			for _, volume := range volumes {
				if !declared[volume] {
					t.Errorf("%s/%s: image VOLUME %s has no data declaration or ephemeral marker", name, service.Name, volume)
				}
			}
		}
	}
}

// TestKnownImageVolumesMatchLocalImages cross-checks the table against
// locally present images when the container CLI is available, in both
// directions: listed VOLUMEs must exist and extra image VOLUMEs fail.
// Absent images skip (KVM inspects the rest).
func TestKnownImageVolumesMatchLocalImages(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("container CLI unavailable")
	}
	checked := 0
	for image, row := range knownImageVolumes {
		out, err := exec.Command("docker", "image", "inspect", "--format", "{{json .Config.Volumes}}", image).Output()
		if err != nil {
			continue
		}
		checked++
		var actual map[string]any
		if err := json.Unmarshal(out, &actual); err != nil {
			t.Fatalf("%s: parse %q: %v", image, out, err)
		}
		listed := map[string]bool{}
		for _, volume := range row.Volumes {
			listed[volume] = true
			if _, ok := actual[volume]; !ok {
				t.Errorf("%s: table lists %s but the image does not declare it", image, volume)
			}
		}
		for volume := range actual {
			if !listed[volume] {
				t.Errorf("%s: image declares VOLUME %s missing from the table", image, volume)
			}
		}
	}
	t.Logf("cross-checked %d local images", checked)
}

func curatedStorage() map[storage.Class]string {
	return map[storage.Class]string{
		storage.ClassApplication: "/mnt/app",
		storage.ClassBulk:        "/mnt/bulk",
	}
}

func TestCuratedPlansPlaceDataOnMountedStorage(t *testing.T) {
	cat := loadCurated(t)
	for _, name := range cat.Names() {
		manifest, _ := cat.Get(name)
		ports := map[string]int{}
		for _, service := range manifest.Services {
			if service.Exposes != "" {
				ports[service.Name] = 8080
			}
		}
		plan, err := PlanInstall(InstallRequest{
			Manifest: manifest, Host: installHost(), Domain: "lan",
			ContainerPorts: ports, Storage: curatedStorage(),
		})
		if err != nil {
			t.Fatalf("%s: PlanInstall: %v", name, err)
		}
		var doc struct {
			Services map[string]struct {
				Volumes []map[string]any `yaml:"volumes"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal([]byte(plan.Compose.ComposeYAML), &doc); err != nil {
			t.Fatalf("%s: compose: %v", name, err)
		}
		for _, service := range manifest.Services {
			rendered := map[string]map[string]any{}
			for _, volume := range doc.Services[service.Name].Volumes {
				rendered[volume["target"].(string)] = volume
			}
			if len(rendered) != len(service.Data) {
				t.Errorf("%s/%s: %d volumes rendered for %d declarations", name, service.Name, len(rendered), len(service.Data))
			}
			for _, data := range service.Data {
				volume, ok := rendered[data.Target]
				if !ok {
					t.Errorf("%s/%s: %s not rendered", name, service.Name, data.Target)
					continue
				}
				if data.Ephemeral {
					if volume["type"] != "tmpfs" {
						t.Errorf("%s/%s: %s should be tmpfs", name, service.Name, data.Target)
					}
					continue
				}
				// Sources sit under <mount>/<package>, the layout
				// backupSources scans.
				want := curatedStorage()[data.Class] + "/" + name + "/" + data.Subpath
				if volume["type"] != "bind" || volume["source"] != want {
					t.Errorf("%s/%s: %s = %v, want bind %s", name, service.Name, data.Target, volume, want)
				}
			}
		}
		if len(plan.Mounts) == 0 {
			t.Errorf("%s: plan lists no mounts", name)
		}
		for _, dir := range plan.Directories {
			if !strings.HasPrefix(dir.Directory.Path, name+"/") {
				t.Errorf("%s: directory %q outside package dir", name, dir.Directory.Path)
			}
		}
	}
}

func dataManifest() Manifest {
	m := validManifest()
	uid := 1000
	m.Services[0].Data = []DataPath{
		{Target: "/config", Class: storage.ClassApplication, Subpath: "config", UID: &uid, GID: &uid},
		{Target: "/cache", Ephemeral: true, Reason: "regenerable"},
	}
	return m
}

func TestPlanInstallDeclarationsWin(t *testing.T) {
	req := installRequest()
	req.Manifest = dataManifest()
	req.Volumes = []VolumeSpec{
		{Service: "jellyfin", Class: storage.ClassBulk, Subpath: "elsewhere", Target: "/config"},
	}
	if _, err := PlanInstall(req); err == nil || !strings.Contains(err.Error(), "cannot be overridden") {
		t.Fatalf("collision err = %v", err)
	}
	req.Volumes = []VolumeSpec{
		{Service: "jellyfin", Class: storage.ClassBulk, Subpath: "media", Target: "/media", ReadOnly: true},
	}
	plan, err := PlanInstall(req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	var extra, declared int
	for _, mount := range plan.Mounts {
		if mount.Declared {
			declared++
		} else if mount.Target == "/media" && mount.ReadOnly {
			extra++
		}
	}
	if declared != 2 || extra != 1 {
		t.Fatalf("mounts = %+v", plan.Mounts)
	}
	first := plan.Directories[0].Directory
	if first.Path != "jellyfin/config" || first.Uid != 1000 || first.Gid != 1000 || first.Mode != 0o750 {
		t.Fatalf("dir = %+v", first)
	}
}

func TestPlanInstallRendersSafeBindsAndSizedTmpfs(t *testing.T) {
	req := installRequest()
	req.Manifest = dataManifest()
	req.Volumes = nil
	plan, err := PlanInstall(req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	var doc struct {
		Services map[string]struct {
			Volumes []struct {
				Type string `yaml:"type"`
				Bind *struct {
					CreateHostPath bool `yaml:"create_host_path"`
				} `yaml:"bind"`
				Tmpfs *struct {
					Size int64 `yaml:"size"`
				} `yaml:"tmpfs"`
			} `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(plan.Compose.ComposeYAML), &doc); err != nil {
		t.Fatalf("compose: %v", err)
	}
	for _, volume := range doc.Services["jellyfin"].Volumes {
		switch volume.Type {
		case "bind":
			if volume.Bind == nil || volume.Bind.CreateHostPath {
				t.Fatalf("bind must set create_host_path: false: %s", plan.Compose.ComposeYAML)
			}
		case "tmpfs":
			if volume.Tmpfs == nil || volume.Tmpfs.Size != 256<<20 {
				t.Fatalf("tmpfs size: %s", plan.Compose.ComposeYAML)
			}
		}
	}
	if !strings.Contains(plan.Compose.ComposeYAML, "create_host_path: false") {
		t.Fatalf("compose = %s", plan.Compose.ComposeYAML)
	}
}

func TestPlanInstallHostPathLibraries(t *testing.T) {
	bulk := t.TempDir()
	library := filepath.Join(bulk, "movies")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(bulk, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	req := installRequest()
	req.Storage[storage.ClassBulk] = bulk
	req.Volumes = []VolumeSpec{{Service: "jellyfin", Class: storage.ClassBulk, HostPath: library, Target: "/media/movies"}}
	plan, err := PlanInstall(req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if !strings.Contains(plan.Compose.ComposeYAML, "source: "+library+"\n") {
		t.Fatalf("media source not the owner path: %s", plan.Compose.ComposeYAML)
	}
	for _, dir := range plan.Directories {
		if strings.Contains(dir.Directory.Path, "movies") {
			t.Fatalf("library provisioned: %+v", dir)
		}
	}
	cases := []struct {
		name string
		spec VolumeSpec
		want string
	}{
		{"relative", VolumeSpec{HostPath: "movies"}, "must be absolute"},
		{"outside mount", VolumeSpec{HostPath: t.TempDir()}, "outside"},
		{"missing", VolumeSpec{HostPath: filepath.Join(bulk, "ghost")}, "no such file"},
		{"file", VolumeSpec{HostPath: file}, "not a directory"},
		{"both", VolumeSpec{HostPath: library, Subpath: "x"}, "not both"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.Service, tc.spec.Class, tc.spec.Target = "jellyfin", storage.ClassBulk, "/media/x"
			bad := req
			bad.Volumes = []VolumeSpec{tc.spec}
			if _, err := PlanInstall(bad); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestNestedMountpointStubProvisioned(t *testing.T) {
	cat := loadCurated(t)
	manifest, err := cat.Get("nextcloud")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_, dirs, err := ResolveData(manifest, curatedStorage())
	if err != nil {
		t.Fatalf("ResolveData: %v", err)
	}
	for _, dir := range dirs {
		if dir.Mountpoint == "/mnt/app" && dir.Directory.Path == "nextcloud/html/data" {
			if dir.Directory.Uid != 33 || dir.Directory.Gid != 33 {
				t.Fatalf("stub owner = %+v", dir.Directory)
			}
			return
		}
	}
	t.Fatalf("no nested stub in %+v", dirs)
}

func TestRefuseInstalled(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	dir := filepath.Join(t.TempDir(), "demo")
	manifest := dataManifest()
	if err := RefuseInstalled(st, "jellyfin", dir, &manifest); err != nil {
		t.Fatalf("fresh refused: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("name: x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RefuseInstalled(st, "jellyfin", dir, &manifest); err == nil || !strings.Contains(err.Error(), "S277") {
		t.Fatalf("pre-S276 err = %v", err)
	}
	bound := "services:\n  jellyfin:\n    volumes:\n    - type: bind\n      target: /config\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(bound), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RefuseInstalled(st, "jellyfin", dir, &manifest); err == nil || !strings.Contains(err.Error(), "configure or update") {
		t.Fatalf("installed err = %v", err)
	}
	// InstallDefinition refuses too, so no caller can bypass the check.
	plan := InstallPlan{Package: "jellyfin", Routes: []docker.Route{{}}, Compose: StackFiles{ComposeYAML: "x"}}
	if _, err := InstallDefinition(plan, storage.NewRegistry(st), dir, st); err == nil ||
		!strings.Contains(err.Error(), "already installed") {
		t.Fatalf("definition err = %v", err)
	}
}

func TestPlanInstallMissingAssignmentFails(t *testing.T) {
	req := installRequest()
	req.Manifest = dataManifest()
	delete(req.Storage, storage.ClassApplication)
	_, err := PlanInstall(req)
	if err == nil || !strings.Contains(err.Error(), "no mountpoint") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanInstallUnknownVolumeServiceRefused(t *testing.T) {
	req := installRequest()
	req.Volumes[0].Service = "ghost"
	if _, err := PlanInstall(req); err == nil || !strings.Contains(err.Error(), "unknown service") {
		t.Fatalf("err = %v", err)
	}
}

func TestDataValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{"relative target", func(m *Manifest) { m.Services[0].Data[0].Target = "config" }, "absolute clean path"},
		{"duplicate target", func(m *Manifest) { m.Services[0].Data[1].Target = "/config" }, "twice"},
		{"bad class", func(m *Manifest) { m.Services[0].Data[0].Class = storage.ClassCache }, "application or bulk"},
		{"unrequired class", func(m *Manifest) {
			m.Requirements.Storage = m.Requirements.Storage[:1]
			m.Services[0].Data[0].Class = storage.ClassBulk
		}, "does not require"},
		{"escaping subpath", func(m *Manifest) { m.Services[0].Data[0].Subpath = "../x" }, "contained"},
		{"world writable", func(m *Manifest) { m.Services[0].Data[0].Mode = "0777" }, "mode"},
		{"ephemeral no reason", func(m *Manifest) { m.Services[0].Data[1].Reason = "" }, "needs a reason"},
		{"ephemeral with class", func(m *Manifest) { m.Services[0].Data[1].Class = storage.ClassBulk }, "no storage fields"},
		{"reason on persistent", func(m *Manifest) { m.Services[0].Data[0].Reason = "x" }, "ephemeral paths only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := dataManifest()
			tc.mutate(&m)
			err := m.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if err := dataManifest().Validate(); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
}

func TestDataSharedDirectoryMustAgree(t *testing.T) {
	m := dataManifest()
	m.Services = append(m.Services, Service{Name: "worker", Image: "x/y:1", Data: []DataPath{
		{Target: "/config", Class: storage.ClassApplication, Subpath: "config"},
	}})
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("err = %v", err)
	}
}
