package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"homestead/internal/craft"
)

// Craft images (S279, ADR-028). A catalog service whose image is the
// local tag `homestead/craft-<app>:<version>` is built on the appliance
// by the verify-then-build operation, never pulled. This file is the
// extension point S249 uses: the reference convention (craft.ParseRef)
// plus two seams the daemon sets at startup. S249 owns catalog schema
// validation of craft packages (for example refusing registry-qualified
// references and requiring the tag to be listed in the craft manifest).

// CraftChecker gates every Compose up of a stack that runs craft
// images: it returns nil only when the local image behind the tag
// matches the digest recorded at build time. The daemon sets it
// (craft.Pipeline.CheckImage). While unset, craft images are refused
// (fail closed).
var CraftChecker func(ctx context.Context, image string) error

// CraftRecordedID returns the recorded local image ID for a craft tag,
// the reference live drift compares running containers with. The daemon
// sets it (craft.Pipeline.RecordedID).
var CraftRecordedID func(image string) (string, bool)

// craftImages returns the craft references among images, sorted, unique.
func craftImages(images []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, image := range images {
		if _, _, ok := craft.ParseRef(image); ok && !seen[image] {
			seen[image] = true
			out = append(out, image)
		}
	}
	sort.Strings(out)
	return out
}

// checkCraftImages refuses unless every craft image in images matches
// its recorded local build.
func checkCraftImages(ctx context.Context, images []string) error {
	for _, image := range craftImages(images) {
		if CraftChecker == nil {
			return fmt.Errorf("catalog: craft image %s cannot be verified: craft builds are not available", image)
		}
		if err := CraftChecker(ctx, image); err != nil {
			return fmt.Errorf("catalog: refusing to start: %w", err)
		}
	}
	return nil
}

// checkCraftStack reads the stack's compose files and checks its craft
// images; stacks without craft images pass untouched.
func checkCraftStack(ctx context.Context, dir, project string) error {
	desired, err := loadDesired(project, dir)
	if err != nil {
		return nil // unreadable compose is reported by the compose run itself
	}
	var images []string
	for _, svc := range desired.Services {
		images = append(images, svc.Image)
	}
	return checkCraftImages(ctx, images)
}

// craftImageDrift compares a running container with the recorded local
// digest of a craft image. It returns "" when they match.
func craftImageDrift(desired string, c craftContainer) string {
	if CraftRecordedID == nil {
		return fmt.Sprintf("craft image %s has no recorded local build to compare with", desired)
	}
	recorded, ok := CraftRecordedID(desired)
	if !ok {
		return fmt.Sprintf("craft image %s has no recorded local build to compare with", desired)
	}
	got := c.imageID
	if got == "" {
		return "" // the Engine reported no image ID: cannot judge
	}
	if sameImageID(got, recorded) {
		return ""
	}
	return fmt.Sprintf("running image %s is not the recorded local build %s of %s (the image was replaced or rebuilt since the container was created)",
		shortID(got), shortID(recorded), desired)
}

type craftContainer struct{ imageID string }

func sameImageID(a, b string) bool {
	a, b = strings.TrimPrefix(a, "sha256:"), strings.TrimPrefix(b, "sha256:")
	if len(a) > len(b) {
		a, b = b, a
	}
	return a != "" && strings.HasPrefix(b, a)
}
