package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/testtools"
)

// TestRenderedEdgeAdaptsInCaddy runs the real caddy over what the route
// renderer writes: gated path apps behind the shared gate, a published
// path app (S498 redirects), a plain host route and the dashboard
// (S506). Upstreams are only names, which adapt does not resolve.
func TestRenderedEdgeAdaptsInCaddy(t *testing.T) {
	caddy := testtools.SkipUnlessTool(t, "caddy")
	for name, set := range map[string]RouteSet{
		"shared gate, published": sharedGateSet(),
		"gated, not published":   gatedPathSet(false),
		"paths and plain host":   pathSet(),
		"all paths published":    withPublished(pathSet()),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := ReconcileRoutes(dir, set, RouteSet{}); err != nil {
				t.Fatalf("ReconcileRoutes: %v", err)
			}
			files, _ := filepath.Glob(filepath.Join(dir, "routes", "*.caddy"))
			if len(files) == 0 {
				t.Fatal("no route files written")
			}
			body := "{\n\tadmin localhost:2019\n}\n\nimport routes/*.caddy\n"
			path := filepath.Join(dir, "Caddyfile")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(caddy, "adapt", "--config", path, "--adapter", "caddyfile").CombinedOutput()
			if err != nil {
				t.Fatalf("caddy adapt: %v\n%s", err, strings.TrimSpace(string(out)))
			}
		})
	}
}
