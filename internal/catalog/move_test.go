package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveEnvPerPackage(t *testing.T) {
	cat := repoCatalog(t)
	names := []string{"homestead.lan", "homestead.local"}
	for name, want := range map[string]string{
		"nextcloud": "[{nextcloud map[NEXTCLOUD_TRUSTED_DOMAINS:homestead.lan homestead.local OVERWRITECLIURL:https://homestead.lan/apps/nextcloud OVERWRITEWEBROOT:/apps/nextcloud] []}]",
		"authentik": "[{server map[AUTHENTIK_WEB__PATH:/apps/auth/] []} {worker map[AUTHENTIK_WEB__PATH:/apps/auth/] []}]",
		"jellyfin":  "[]",
		"immich":    "[]",
	} {
		m, err := cat.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		got := MoveEnv(m, names)
		if got == nil {
			got = []ConfigChange{}
		}
		if fmt.Sprint(got) != want {
			t.Fatalf("%s: %v", name, got)
		}
	}
}

func TestMoveSeedSetsBaseURL(t *testing.T) {
	m, err := repoCatalog(t).Get("jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	dir, data := t.TempDir(), t.TempDir()
	compose := "services:\n  jellyfin:\n    volumes:\n      - type: bind\n        source: " + data + "/config\n        target: /config\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}
	seeds, err := MoveSeeds(m, dir)
	if err != nil || len(seeds) != 1 || seeds[0].Source != data+"/config/config/network.xml" || seeds[0].Elements["BaseUrl"] != "/apps/jellyfin" {
		t.Fatalf("seeds = %+v, %v", seeds, err)
	}
	// No file yet: the whole seed is written.
	if err := ApplyMoveSeed(seeds[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(seeds[0].Source); !strings.Contains(string(got), "<BaseUrl>/apps/jellyfin</BaseUrl>") {
		t.Fatalf("written = %s", got)
	}
	for existing, want := range map[string]string{
		"<NetworkConfiguration>\n  <BaseUrl />\n  <EnableHttps>false</EnableHttps>\n</NetworkConfiguration>\n": "<NetworkConfiguration>\n  <BaseUrl>/apps/jellyfin</BaseUrl>\n  <EnableHttps>false</EnableHttps>\n</NetworkConfiguration>\n",
		"<NetworkConfiguration>\n  <BaseUrl>/old</BaseUrl>\n</NetworkConfiguration>\n":                         "<NetworkConfiguration>\n  <BaseUrl>/apps/jellyfin</BaseUrl>\n</NetworkConfiguration>\n",
		"<NetworkConfiguration>\n  <EnableHttps>false</EnableHttps>\n</NetworkConfiguration>\n":                "<NetworkConfiguration>\n  <EnableHttps>false</EnableHttps>\n  <BaseUrl>/apps/jellyfin</BaseUrl>\n</NetworkConfiguration>\n",
	} {
		if err := os.WriteFile(seeds[0].Source, []byte(existing), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(seeds[0].Source, 0o640); err != nil {
			t.Fatal(err)
		}
		if err := ApplyMoveSeed(seeds[0]); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(seeds[0].Source)
		info, _ := os.Stat(seeds[0].Source)
		if string(got) != want || info.Mode().Perm() != 0o640 {
			t.Fatalf("from %q got %q (%v)", existing, got, info.Mode())
		}
	}
}
