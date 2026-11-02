package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/craft"
)

// everythingPackages lists the Everything bridge packages (ADR-057). Each
// Everything slice appends its app here, adds its YAML, craft manifest
// entry (build: everything), knownImageVolumes row and sign-in posture.
var everythingPackages = []string{"tasks", "projects"}

func TestEverythingPackages(t *testing.T) {
	cat := repoCatalog(t)
	shipped := loadShippedCraftManifest(t)
	for _, name := range everythingPackages {
		t.Run(name, func(t *testing.T) {
			m, err := cat.Get(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Validate(); err != nil {
				t.Fatal(err)
			}
			// A bridge image, never a static craft image or a registry image.
			if len(m.BridgeImages()) != 1 || len(m.CraftImages()) != 0 || m.BridgeImages()[0] != "homestead/everything-"+name+":"+m.Version {
				t.Fatalf("bridge images %v craft images %v", m.BridgeImages(), m.CraftImages())
			}
			// Built from the in-repo definition and pin, never the signed
			// craft manifest (ADR-057): the version, digest and size agree
			// with the archive that ships with the appliance.
			if _, listed := shipped.App(name); listed {
				t.Fatal("an Everything app must not be listed in the owner-signed craft manifest")
			}
			rawDef, err := os.ReadFile("../../scripts/everything/apps/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var def craft.BridgeApp
			if err := json.Unmarshal(rawDef, &def); err != nil {
				t.Fatal(err)
			}
			archive, err := os.ReadFile("../../third_party/everything/" + def.Archive)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(archive)
			if def.App != name || def.Version != m.Version || def.SHA256 != hex.EncodeToString(sum[:]) || def.Size != int64(len(archive)) {
				t.Fatalf("definition %+v does not match the package (%s) or the shipped archive", def, m.Version)
			}
			if pin, err := os.ReadFile("../../third_party/everything/pin.yaml"); err != nil || !strings.Contains(string(pin), "sha256: "+def.SHA256) {
				t.Fatalf("third_party/everything/pin.yaml does not pin %s: %v", def.SHA256, err)
			}
			// Served by path with the path kept: the bridge knows its base.
			if m.Serve != ServePath || m.StripPrefix || m.AppPath() != "/apps/"+name+"/" {
				t.Fatalf("serve %q strip %v path %q", m.Serve, m.StripPrefix, m.AppPath())
			}
			// Sign-in is the normal plan, closed by default, secret by file,
			// and the bridge waits for it rather than serving anything.
			svc := m.Services[0]
			if svc.Environment["HOMESTEAD_EVERYTHING_AWAIT_SIGNIN"] != "1" {
				t.Fatal("the package must start in await-sign-in mode")
			}
			for _, key := range []string{"HOMESTEAD_EVERYTHING_BASE_PATH", "HOMESTEAD_EVERYTHING_ORIGIN", "HOMESTEAD_EVERYTHING_OIDC_REDIRECT_URL"} {
				if !usesRouteToken(svc.Environment[key]) {
					t.Errorf("%s = %q does not use a route token", key, svc.Environment[key])
				}
			}
			if m.SSO.Class != SSORecommended || m.SSO.OIDC == nil || m.SSO.OIDC.Adapter != "env-oidc" {
				t.Fatalf("sso = %+v", m.SSO)
			}
			env := m.SSO.OIDC.Env
			if !env.Exclusive || env.Secret != "oidc_client_secret" || env.Trust != "HOMESTEAD_EVERYTHING_OIDC_CA_FILE" || env.TrustBundle != TrustBundleCombined {
				t.Fatalf("env oidc = %+v", env)
			}
			if _, ok := env.Set["HOMESTEAD_EVERYTHING_OIDC_ISSUER"]; !ok {
				t.Fatal("issuer not set by the sign-in plan")
			}
			if _, ok := env.Set["HOMESTEAD_EVERYTHING_OIDC_CLIENT_ID"]; !ok {
				t.Fatal("client id not set by the sign-in plan")
			}
			consumed := false
			for _, s := range m.Secrets {
				for _, c := range s.Consumers {
					if s.Name == "oidc_client_secret" && c.Env == "HOMESTEAD_EVERYTHING_OIDC_CLIENT_SECRET_FILE" {
						consumed = true
					}
				}
			}
			if !consumed {
				t.Fatal("the client secret must reach the bridge as a file")
			}
			for k, v := range env.Set {
				if strings.Contains(strings.ToLower(k), "secret") && !strings.HasSuffix(k, "_FILE") {
					t.Errorf("sign-in sets %s=%q: secrets go by file only", k, v)
				}
			}
			// The data is the bridge's database; it is backed up.
			if m.Stateless() || m.Database == nil || m.Database.Kind != "sqlite" || len(m.BackupPaths) == 0 {
				t.Fatalf("data: stateless=%v database=%+v backup=%v", m.Stateless(), m.Database, m.BackupPaths)
			}
			// No routes for the static craft file server.
			recs, err := RecommendRoutes(m)
			if err != nil || len(recs) != 1 || recs[0].CraftApp != "" {
				t.Fatalf("routes = %+v, %v", recs, err)
			}
		})
	}
}

// TestEverythingContextIsInSync keeps images/everything (the build context
// that ships with the appliance) identical to the repository files it
// copies. Fix a failure with scripts/everything/sync-context.sh.
func TestEverythingContextIsInSync(t *testing.T) {
	const ctx = "../../images/everything/"
	pairs := map[string]string{"go.mod": "../../go.mod", "go.sum": "../../go.sum"}
	for _, dir := range [][2]string{{"cmd/everything-bridge", "../../cmd/everything-bridge"}, {"internal/everything", "../../internal/everything"}, {"scripts/everything/apps", "../../scripts/everything/apps"}} {
		entries, err := os.ReadDir(dir[1])
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			pairs[dir[0]+"/"+e.Name()] = dir[1] + "/" + e.Name()
		}
	}
	for _, f := range []string{"prepare_app.py", "puter-bridge.js", "tasks-sw.template.js", "projects-sw.template.js"} {
		pairs["scripts/everything/"+f] = "../../scripts/everything/" + f
	}
	for dst, src := range pairs {
		want, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(ctx + dst)
		if err != nil || string(got) != string(want) {
			t.Errorf("images/everything/%s differs from %s; run scripts/everything/sync-context.sh", dst, strings.TrimPrefix(src, "../../"))
		}
	}
	// No stray files either.
	_ = filepath.WalkDir(ctx, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(filepath.ToSlash(path), ctx)
		if _, ok := pairs[rel]; !ok && rel != "Dockerfile" && rel != "compose.example.yaml" && rel != "compose.env.example" {
			t.Errorf("images/everything/%s is not a copy of a repository file", rel)
		}
		return nil
	})
}

func TestEverythingServeRules(t *testing.T) {
	m, err := repoCatalog(t).Get("tasks")
	if err != nil {
		t.Fatal(err)
	}
	m.StripPrefix = true
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "must not have it stripped") {
		t.Fatalf("a stripped Everything path: err = %v", err)
	}
	m.StripPrefix = false
	m.Services[0].Image = "docker.io/homestead/everything-tasks:0.1.0"
	if err := m.Validate(); err == nil {
		t.Fatal("a registry-qualified bridge image was accepted")
	}
	m.Services[0].Image = "homestead/everything-other:0.1.0"
	if err := m.Validate(); err == nil {
		t.Fatal("another app's bridge image was accepted")
	}
}
