package version

import (
	"regexp"
	"testing"
)

var semverLike = regexp.MustCompile(`^\d+\.\d+\.\d+`)

func TestVersionSet(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
	if !semverLike.MatchString(Version) {
		t.Fatalf("Version %q must start with MAJOR.MINOR.PATCH", Version)
	}
}
