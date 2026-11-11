// Package testtools lets tests that need an external program skip where it
// is missing, and fail where CI says it must be there (S506).
package testtools

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Required reports whether HOMESTEAD_REQUIRE_TOOLS (a comma list or "all")
// names tool. HOMESTEAD_REQUIRE_AGE, the older switch, counts as naming age.
func Required(tool string) bool {
	if os.Getenv("HOMESTEAD_REQUIRE_AGE") != "" && tool == "age" {
		return true
	}
	for _, name := range strings.Split(os.Getenv("HOMESTEAD_REQUIRE_TOOLS"), ",") {
		name = strings.TrimSpace(name)
		if name == "all" || (name != "" && name == tool) {
			return true
		}
	}
	return false
}

// SkipUnlessTool returns the path of tool, or skips the test when it is not
// on PATH. When the tool is required the test fails instead.
func SkipUnlessTool(t testing.TB, tool string) string {
	t.Helper()
	path, err := exec.LookPath(tool)
	if err == nil {
		return path
	}
	if Required(tool) {
		t.Fatalf("%s is not on PATH but HOMESTEAD_REQUIRE_TOOLS (or HOMESTEAD_REQUIRE_AGE) requires it", tool)
	}
	t.Skipf("%s not installed; skipping (set HOMESTEAD_REQUIRE_TOOLS=%s to fail instead)", tool, tool)
	return ""
}

// SkipOrFail is for a tool that is present but unusable here, such as nft
// without privileges: it fails when the tool is required and skips otherwise.
func SkipOrFail(t testing.TB, tool, why string) {
	t.Helper()
	if Required(tool) {
		t.Fatalf("%s is required but unusable: %s", tool, why)
	}
	t.Skipf("%s unusable here: %s", tool, why)
}
