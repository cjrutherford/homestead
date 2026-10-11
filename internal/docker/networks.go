package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"homestead/internal/operation"
)

// Standard platform network names.
const (
	// NetworkEdge carries edge proxy to application traffic.
	NetworkEdge = "homestead-edge"
	// NetworkPlatform carries platform services to consuming applications.
	NetworkPlatform = "homestead-platform"
)

// ManagedLabel marks Homestead-owned networks for filtering and safety.
const ManagedLabel = "homestead.managed=true"

// NetworkSpec is one desired Docker network.
type NetworkSpec struct {
	Name     string
	Driver   string
	Internal bool
}

// StandardNetworks returns the platform set: edge for proxy-to-app
// traffic, platform for platform-service consumers. Both allow egress
// (applications fetch metadata and updates; platform DNS resolves
// upstream); isolation of backend stores belongs to per-app Compose
// files, not here.
func StandardNetworks() []NetworkSpec {
	return []NetworkSpec{
		{Name: NetworkEdge, Driver: "bridge"},
		{Name: NetworkPlatform, Driver: "bridge"},
	}
}

// EnsureDefinition plans idempotent creation of the given networks plus
// a closing verify, executed through the operation engine. Existing
// networks with matching drivers short-circuit per network; a driver
// mismatch fails loudly instead of touching foreign configuration.
func EnsureDefinition(networks []NetworkSpec) operation.Definition {
	def := operation.Definition{Type: "docker.networks.ensure"}
	for _, network := range networks {
		network := network
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "ensure-" + network.Name,
			Run: func(ctx context.Context) error {
				return ensureNetwork(ctx, network)
			},
		})
	}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "verify",
		Run: func(ctx context.Context) error {
			return verifyNetworks(ctx, networks)
		},
	})
	return def
}

// inspectNetwork reads one network, reporting existence and driver.
// Absence reads across daemon wordings ("No such network" on older
// daemons, "not found" on newer ones — proven live on Docker 26);
// anything else fails instead of creating over an unclear state.
func inspectNetwork(ctx context.Context, name string) (driver string, exists bool, err error) {
	out, err := execRun(ctx, "docker", "network", "inspect", name, "--format", "json")
	if err != nil {
		lowered := strings.ToLower(err.Error())
		if strings.Contains(lowered, "no such network") || strings.Contains(lowered, "not found") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect network %s: %w", name, err)
	}
	var networks []struct {
		Driver string `json:"Driver"`
	}
	if err := json.Unmarshal([]byte(out), &networks); err != nil || len(networks) == 0 {
		return "", false, fmt.Errorf("inspect network %s: unparsable output", name)
	}
	return networks[0].Driver, true, nil
}

// ensureNetwork creates a missing network with the managed label, or
// verifies a present one matches.
func ensureNetwork(ctx context.Context, network NetworkSpec) error {
	driver, exists, err := inspectNetwork(ctx, network.Name)
	if err != nil {
		return err
	}
	if exists {
		if driver != network.Driver {
			return fmt.Errorf("network %s exists with driver %q, want %q: refusing to touch foreign configuration",
				network.Name, driver, network.Driver)
		}
		return nil
	}
	args := []string{"network", "create", "--driver", network.Driver, "--label", ManagedLabel}
	if network.Internal {
		args = append(args, "--internal")
	}
	args = append(args, network.Name)
	if _, err := execRun(ctx, "docker", args...); err != nil {
		return fmt.Errorf("create network %s: %w", network.Name, err)
	}
	return nil
}

// verifyNetworks proves every desired network exists after the run.
func verifyNetworks(ctx context.Context, networks []NetworkSpec) error {
	for _, network := range networks {
		if _, exists, err := inspectNetwork(ctx, network.Name); err != nil || !exists {
			if err == nil {
				err = fmt.Errorf("network %s missing after ensure", network.Name)
			}
			return fmt.Errorf("verify: %w", err)
		}
	}
	return nil
}
