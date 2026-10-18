package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Craft file socket (S254, ADR-032). The daemon serves the craft-file
// handlers on a dedicated unix socket; the Caddy stack mounts its
// directory read-only and craft routes forward CraftFilesPrefix to it.
const (
	// CraftSocketDir is the host directory mounted into the edge. It holds
	// only the craft-file socket, never the recovery socket.
	CraftSocketDir = "/run/homestead-craft"
	// CraftSocketPath is the socket inside that directory.
	CraftSocketPath = CraftSocketDir + "/files.sock"
	// CraftFilesPrefix is the forwarded path prefix on a craft host.
	CraftFilesPrefix = "/_homestead/files/"
	// CraftAppHeader and CraftHostHeader carry the route's craft app and
	// host to the daemon; the edge sets them with header_up.
	CraftAppHeader  = "X-Homestead-Craft-App"
	CraftHostHeader = "X-Homestead-Craft-Host"
)

// HasCraftRoutes reports whether any route forwards the craft file path.
func (s RouteSet) HasCraftRoutes() bool {
	for _, r := range s.Routes {
		if r.CraftApp != "" {
			return true
		}
	}
	return false
}

// CraftMountPresent reports whether the running edge container mounts
// the craft socket directory. A missing mount means the edge predates
// ADR-032 and must be redeployed (POST /api/v1/edge/deploy).
func CraftMountPresent(ctx context.Context) (bool, error) {
	out, err := execRun(ctx, "docker", "inspect", "--format", "{{json .Mounts}}", CaddyContainerName)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", CaddyContainerName, err)
	}
	return mountsContain(out, CraftSocketDir)
}

func mountsContain(inspectJSON, dest string) (bool, error) {
	var mounts []struct {
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(inspectJSON)), &mounts); err != nil {
		return false, fmt.Errorf("inspect mounts: %w", err)
	}
	for _, m := range mounts {
		if m.Destination == dest {
			return true, nil
		}
	}
	return false, nil
}

// errCraftMountMissing is the redeploy instruction.
var errCraftMountMissing = fmt.Errorf("the edge container does not mount %s, so craft file storage cannot reach the daemon: redeploy the edge (POST /api/v1/edge/deploy, homesteadctl edge deploy)", CraftSocketDir)

// verifyCraftMount fails when craft routes are desired but the running
// edge lacks the mount (drift from the generated stack, ADR-032).
func verifyCraftMount(ctx context.Context, set RouteSet) error {
	if !set.HasCraftRoutes() {
		return nil
	}
	ok, err := CraftMountPresent(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errCraftMountMissing
	}
	return nil
}
