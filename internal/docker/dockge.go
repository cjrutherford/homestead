package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"homestead/internal/layout"
	"homestead/internal/operation"
)

// DockgeImage pins the expert Compose interface (S210). Image updates
// arrive with the update flow (S190+); until then this constant is the
// version of record.
const DockgeImage = "louislam/dockge:1.5.0"

// Dockge stack layout: the advanced expert Compose interface
// (ARCHITECTURE).
const (
	// DockgeStackName is the Compose project backing Dockge.
	DockgeStackName = "dockge"
	// DockgeService is the Dockge container.
	DockgeService = "dockge"
	// DockgeContainerName is the Dockge container name.
	DockgeContainerName = "homestead-dockge"
	// DockgeUIPort is the published web interface port. Who may reach
	// it belongs to the administration link (S211); deployment only
	// publishes it for the owner.
	DockgeUIPort = 5001
)

// DockgeStack returns the expert-interface stack value: fixed name,
// layout directory, the Dockge service, enabled.
func DockgeStack() Stack {
	return Stack{
		Name:     DockgeStackName,
		Dir:      DirFor(DockgeStackName),
		Services: []string{DockgeService},
		Enabled:  true,
	}
}

// checkDockgeStack rejects non-dockge stacks and empty dirs so deploy
// refuses misplacement instead of writing elsewhere.
func checkDockgeStack(stack Stack) error {
	if stack.Name != DockgeStackName {
		return fmt.Errorf("dockge: stack must be %q", stack.Name)
	}
	if stack.Dir == "" {
		return fmt.Errorf("dockge: stack dir must not be empty")
	}
	return nil
}

// WriteDockgeStack generates ordinary Compose artifacts under dir:
// compose.yaml plus the data directory. The result runs with vanilla
// `docker compose` — no Homestead at runtime (recovery rule,
// ADR-005). The engine socket mount is deliberate: Dockge manages
// stacks through the engine API, which is the whole deployment. The
// stacks directory mounts at the same path inside and out (Dockge
// requires both to match), so experts see Homestead-managed stacks
// beside their own — full engine control by design, with S211 gating
// who may use it. File ownership stays root (PUID/PGID 0) like every
// other root-owned Homestead artifact. No credentials are generated:
// Dockge creates its login on first start through the published UI
// port.
func WriteDockgeStack(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		return fmt.Errorf("dockge: data dir: %w", err)
	}
	compose := `name: dockge
services:
  dockge:
    image: ` + DockgeImage + `
    container_name: ` + DockgeContainerName + `
    restart: unless-stopped
    ports:
      - "5001:5001/tcp"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./data:/app/data
      - ` + layout.StacksDir.Path + `:` + layout.StacksDir.Path + `
    environment:
      - DOCKGE_STACKS_DIR=` + layout.StacksDir.Path + `
      - PUID=0
      - PGID=0
    networks:
      - homestead-platform
networks:
  homestead-platform:
    external: true
`
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		return fmt.Errorf("dockge: write compose.yaml: %w", err)
	}
	return nil
}

// DockgeDeployDefinition plans the expert interface: the platform
// network (a platform service home), stack files, detached up, and a
// verify proving the Dockge container runs. Idempotent throughout:
// existing networks short-circuit, files rewrite identically, up
// converges. Who may reach the interface belongs to the
// administration link (S211).
func DockgeDeployDefinition(stack Stack) (operation.Definition, error) {
	if err := checkDockgeStack(stack); err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "dockge.deploy"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "ensure-" + NetworkPlatform,
		Run: func(ctx context.Context) error {
			return ensureNetwork(ctx, NetworkSpec{Name: NetworkPlatform, Driver: "bridge"})
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "write-stack",
		Run: func(ctx context.Context) error {
			return WriteDockgeStack(stack.Dir)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "up",
		Run: func(ctx context.Context) error {
			return runCompose(ctx, stack, []string{"up", "-d"}, nil)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "verify",
		Run: func(ctx context.Context) error {
			return verifyService(ctx, stack, DockgeService)
		},
	})
	return def, nil
}

// DockgeHealthPath probes the interface root: Dockge serves its login
// page there on 200 while running.
const DockgeHealthPath = "/"

// DockgeHealthIntervalSeconds spaces active probes at the shared default.
const DockgeHealthIntervalSeconds = 30

// DockgeDefaultHostname names the expert route under a base domain:
// dockge.<domain>. Owners override freely; defaults only start.
func DockgeDefaultHostname(domain string) (Hostname, error) {
	host := Hostname("dockge." + domain)
	if err := host.Validate(); err != nil {
		return "", fmt.Errorf("dockge: default hostname: %w", err)
	}
	return host, nil
}

// DockgeRouteFor builds the expert-interface proxy route (S211):
// local-only exposure with internal TLS for LAN serving, upstreaming
// to the Dockge container on the platform network. Exposure is not an
// input — the interface grants full engine control, so the
// constructor cannot describe anything wider; widening stays
// impossible, not merely unreviewed. The engine assessment (S120)
// proves the posture: local recommended, aligned, finding-free.
func DockgeRouteFor(host Hostname) (Route, error) {
	route := Route{
		Host:     host,
		Target:   Target{Host: DockgeContainerName, Port: DockgeUIPort},
		Exposure: ExposureLocal,
		TLS:      TLSInternal,
		Health:   HealthCheck{Path: DockgeHealthPath, IntervalSeconds: DockgeHealthIntervalSeconds},
	}
	if err := route.Validate(); err != nil {
		return Route{}, fmt.Errorf("dockge: route: %w", err)
	}
	return route, nil
}

// DockgeInstalled reports whether the engine runs the Dockge stack:
// any running container carrying the dockge Compose project label.
func DockgeInstalled(health Health) bool {
	for _, container := range health.Containers {
		if container.Labels[LabelProject] != DockgeStackName {
			continue
		}
		if container.State == "running" {
			return true
		}
	}
	return false
}
