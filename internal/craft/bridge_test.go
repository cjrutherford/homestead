package craft

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// bridgeEnv lays out a bridge context with one app definition and the
// pinned archive, the way the appliance ships them (ADR-057).
func bridgeEnv(t *testing.T, archive []byte, pinned []byte) *env {
	t.Helper()
	e := newEnv(t)
	ctx := filepath.Join(e.dir, "bridge-context")
	apps := filepath.Join(ctx, "scripts", "everything", "apps")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctx, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pinned)
	def := fmt.Sprintf(`{"app":"tasks","version":"0.1.0","archive":"tasks-source.tar.gz","sha256":%q,"size":%d}`, hex.EncodeToString(sum[:]), len(pinned))
	if err := os.WriteFile(filepath.Join(apps, "tasks.json"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(e.dir, "bridge-sources")
	if err := os.MkdirAll(sources, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sources, "tasks-source.tar.gz"), archive, 0o644); err != nil {
		t.Fatal(err)
	}
	e.pipe.BridgeContextDir, e.pipe.BridgeSourcesDir = ctx, sources
	return e
}

func (e *env) buildBridge() error {
	def, err := e.pipe.BridgeDefinition("tasks", "tester")
	if err != nil {
		return err
	}
	return runDefinition(context.Background(), def)
}

func TestBridgeAppBuildsFromThePinnedArchive(t *testing.T) {
	archive := tarGz(t, map[string]string{"src/tasks/index.html": "<html></html>"})
	e := bridgeEnv(t, archive, archive)
	if err := e.buildBridge(); err != nil {
		t.Fatal(err)
	}
	spec := e.fb.builds[len(e.fb.builds)-1]
	if !spec.Network || spec.Args["APP"] != "tasks" || spec.Args["ARTIFACT_FILE"] != "tasks-source.tar.gz" || spec.Args["ARTIFACT_SHA256"] == "" {
		t.Fatalf("spec = %+v", spec)
	}
	img, err := e.pipe.Store.Image("tasks", "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if img.Tag != "homestead/everything-tasks:0.1.0" || img.ImageID == "" || img.ImageID != e.fb.tags[img.Tag] || len(e.fb.tags) != 1 {
		t.Fatalf("record = %+v tags = %v", img, e.fb.tags)
	}
	if err := e.pipe.CheckImage(context.Background(), img.Tag); err != nil {
		t.Fatalf("CheckImage: %v", err)
	}
	if id, ok := e.pipe.RecordedID(img.Tag); !ok || id != img.ImageID {
		t.Fatal("RecordedID does not know the bridge image")
	}
	// A bridge build records no craft baseline and moves no high-water mark.
	if b, _ := e.pipe.Store.Baseline("tasks"); b.Counter != 0 {
		t.Fatalf("baseline = %+v", b)
	}
	if hw, _ := e.pipe.Store.HighWater(); hw != 0 {
		t.Fatalf("high water = %d", hw)
	}
	if entries, _ := os.ReadDir(e.pipe.StagingDir); len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
}

func TestBridgeAppRefusesAnArchiveThatIsNotThePin(t *testing.T) {
	pinned := tarGz(t, map[string]string{"src/tasks/index.html": "<html></html>"})
	other := tarGz(t, map[string]string{"src/tasks/index.html": "<html>other</html>"})
	e := bridgeEnv(t, other, pinned)
	wantCheck(t, e.buildBridge(), CheckArtifact)
	e.assertNothingBuilt()
	if _, err := e.pipe.Store.Image("tasks", "0.1.0"); err == nil {
		t.Fatal("an image was recorded")
	}
}

func TestBridgeAppUnknownOrUnavailable(t *testing.T) {
	archive := tarGz(t, map[string]string{"src/tasks/index.html": "<html></html>"})
	e := bridgeEnv(t, archive, archive)
	if _, err := e.pipe.BridgeDefinition("nope", "t"); !errors.Is(err, ErrInput) {
		t.Fatalf("unknown app: %v", err)
	}
	if _, err := e.pipe.BridgeDefinition("../x", "t"); !errors.Is(err, ErrInput) {
		t.Fatalf("bad name: %v", err)
	}
	if e.pipe.IsBridgeApp("wordcraft") || !e.pipe.IsBridgeApp("tasks") {
		t.Fatal("IsBridgeApp")
	}
	e.pipe.BridgeContextDir = ""
	if e.pipe.IsBridgeApp("tasks") {
		t.Fatal("bridge builds must be unavailable without a context")
	}
}

func TestBridgeCheckRecordsAVerdict(t *testing.T) {
	archive := tarGz(t, map[string]string{"src/tasks/index.html": "<html></html>"})
	e := bridgeEnv(t, archive, archive)
	plan, verdict, err := e.pipe.BridgeCheck(context.Background(), "tasks", "t")
	if err != nil || verdict.State != StateVerified || plan.Tag != "homestead/everything-tasks:0.1.0" || plan.Source != "bundled" {
		t.Fatalf("plan %+v verdict %+v err %v", plan, verdict, err)
	}
}

func TestStaticCraftBuildKeepsNoNetwork(t *testing.T) {
	e := newEnv(t)
	if err := e.build(e.manifestFor("1.2.0", 5, nil)); err != nil {
		t.Fatal(err)
	}
	if spec := e.fb.builds[0]; spec.Network || spec.File != "" {
		t.Fatalf("spec = %+v: a static craft build must stay network-less", spec)
	}
}

func TestBridgeRefs(t *testing.T) {
	if app, v, ok := ParseRef("homestead/everything-tasks:0.1.0"); !ok || app != "tasks" || v != "0.1.0" {
		t.Fatal("bridge ref rejected")
	}
	if !IsBridgeRef("homestead/everything-tasks:0.1.0") || IsBridgeRef("homestead/craft-tasks:0.1.0") || IsBridgeRef("homestead/everything-tasks:latest") {
		t.Fatal("IsBridgeRef")
	}
}
