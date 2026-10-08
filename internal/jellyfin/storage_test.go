package jellyfin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/storage"
)

func jellyfinManifest(t *testing.T) catalog.Manifest {
	t.Helper()
	cat, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	manifest, err := cat.Get("jellyfin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return manifest
}

func mediaLibraries(t *testing.T, bulk string) []MediaDir {
	t.Helper()
	var out []MediaDir
	for _, name := range []string{"movies", "shows"} {
		path := filepath.Join(bulk, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		out = append(out, MediaDir{Name: name, HostPath: path})
	}
	return out
}

func TestPlanStorage(t *testing.T) {
	manifest := jellyfinManifest(t)
	app := t.TempDir()
	bulk := t.TempDir()
	assignments := map[storage.Class]string{
		storage.ClassApplication: app,
		storage.ClassBulk:        bulk,
	}
	plan, err := PlanStorage(manifest, assignments, mediaLibraries(t, bulk))
	if err != nil {
		t.Fatalf("PlanStorage: %v", err)
	}
	if plan.ConfigMountpoint != app || plan.ConfigPath != "jellyfin/config" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Media) != 2 || plan.Media[0].Target != "/media/movies" ||
		plan.Media[1].Mountpoint != bulk {
		t.Fatalf("media = %+v", plan.Media)
	}
	volumes := plan.Volumes("jellyfin")
	if len(volumes) != 3 || volumes[0].Target != "/config" ||
		volumes[1].Subpath != "movies" || volumes[2].Target != "/media/shows" {
		t.Fatalf("volumes = %+v", volumes)
	}
	dirs := plan.ProvisionDirs()
	if len(dirs) != 1 || dirs[0].Mountpoint != app || dirs[0].Directory.Path != "jellyfin/config" {
		t.Fatalf("dirs = %+v", dirs)
	}
	// Empty libraries plan config alone.
	bare, err := PlanStorage(manifest, assignments, nil)
	if err != nil {
		t.Fatalf("PlanStorage: %v", err)
	}
	if len(bare.Media) != 0 || len(bare.Volumes("jellyfin")) != 1 {
		t.Fatalf("bare = %+v", bare)
	}
}

func TestPlanStorageRefusals(t *testing.T) {
	manifest := jellyfinManifest(t)
	app := t.TempDir()
	bulk := t.TempDir()
	good := map[storage.Class]string{storage.ClassApplication: app, storage.ClassBulk: bulk}
	libraries := mediaLibraries(t, bulk)
	file := filepath.Join(bulk, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cases := []struct {
		name        string
		assignments map[storage.Class]string
		media       []MediaDir
		want        string
	}{
		{"missing class", map[storage.Class]string{storage.ClassApplication: app}, libraries, "has no mountpoint"},
		{"dup names", good, []MediaDir{{Name: "x", HostPath: bulk}, {Name: "x", HostPath: bulk}}, "duplicate library"},
		{"bad name", good, []MediaDir{{Name: "Bad!", HostPath: bulk}}, "must match"},
		{"relative path", good, []MediaDir{{Name: "x", HostPath: "relative"}}, "must be absolute"},
		{"outside bulk", good, []MediaDir{{Name: "x", HostPath: app}}, "outside the bulk mount"},
		{"missing dir", good, []MediaDir{{Name: "x", HostPath: filepath.Join(bulk, "ghost")}}, "no such file"},
		{"not a dir", good, []MediaDir{{Name: "x", HostPath: file}}, "not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PlanStorage(manifest, tc.assignments, tc.media)
			if err == nil {
				t.Fatal("planned invalid storage")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	if _, err := PlanStorage(catalog.Manifest{}, good, nil); err == nil {
		t.Fatal("planned invalid manifest")
	}
}
