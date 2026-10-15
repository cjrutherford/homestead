package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/docker"
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
