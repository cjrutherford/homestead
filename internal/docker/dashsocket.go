package docker

import (
	"context"
	"fmt"
)

// Dashboard socket (S289, ADR-035). The daemon serves the dashboard and API
// on a dedicated unix socket; the Caddy stack mounts its directory
// read-only and the dashboard route forwards to it. The directory holds
// only this socket, never the recovery or craft sockets.
const (
	// DashboardSocketDir is the host directory mounted into the edge.
	DashboardSocketDir = "/run/homestead-dashboard"
	// DashboardSocketPath is the socket inside that directory.
	DashboardSocketPath = DashboardSocketDir + "/dashboard.sock"
	// DefaultDashboardName is the local dashboard name before the owner
	// sets one.
	DefaultDashboardName Hostname = "homestead.lan"
)

// DashboardRoute returns the dashboard route in the set, if any.
func (s RouteSet) DashboardRoute() (Route, bool) {
	for _, r := range s.Routes {
		if r.Dashboard {
			return r, true
		}
	}
	return Route{}, false
}

// DashboardMountPresent reports whether the running edge container mounts
// the dashboard socket directory. A missing mount means the edge predates
// ADR-035 and must be redeployed (POST /api/v1/edge/deploy).
func DashboardMountPresent(ctx context.Context) (bool, error) {
	out, err := execRun(ctx, "docker", "inspect", "--format", "{{json .Mounts}}", CaddyContainerName)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", CaddyContainerName, err)
	}
	return mountsContain(out, DashboardSocketDir)
}

// ErrDashboardMountMissing is the redeploy instruction.
var ErrDashboardMountMissing = fmt.Errorf("the edge container does not mount %s, so the dashboard cannot be reached through the edge: redeploy the edge (POST /api/v1/edge/deploy, homesteadctl edge deploy)", DashboardSocketDir)

// verifyDashboardMount fails when a dashboard route is desired but the
// running edge lacks the mount (drift from the generated stack, ADR-035).
func verifyDashboardMount(ctx context.Context, set RouteSet) error {
	if _, ok := set.DashboardRoute(); !ok {
		return nil
	}
	ok, err := DashboardMountPresent(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDashboardMountMissing
	}
	return nil
}
