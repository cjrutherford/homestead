package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"homestead/internal/operation"
)

// AdguardImage pins the local DNS service (S084). Image updates arrive
// with the update flow (S190+); until then this constant is the version
// of record.
const AdguardImage = "adguard/adguardhome:v0.107.79"

// AdGuard stack layout: the initial local DNS provider (ARCHITECTURE).
const (
	// AdguardStackName is the Compose project backing local DNS.
	AdguardStackName = "adguard"
	// AdguardService is the DNS container.
	AdguardService = "adguard"
	// AdguardContainerName is the DNS container name.
	AdguardContainerName = "homestead-adguard"
	// AdguardWebPort is the published admin/API port the provider
	// adapter (S085) and the owner use.
	AdguardWebPort = 3000
	// AdguardDNSPort is the published DNS port (TCP and UDP). Binding
	// privileged ports needs the root-owned daemon, which Homestead
	// already requires.
	AdguardDNSPort = 53
)

// AdguardStack returns the local-DNS stack value: fixed name, layout
// directory, the DNS service, enabled.
func AdguardStack() Stack {
	return Stack{
		Name:     AdguardStackName,
		Dir:      DirFor(AdguardStackName),
		Services: []string{AdguardService},
		Enabled:  true,
	}
}

// checkAdguardStack rejects non-adguard stacks and empty dirs so deploy
// refuses misplacement instead of writing elsewhere.
func checkAdguardStack(stack Stack) error {
	if stack.Name != AdguardStackName {
		return fmt.Errorf("adguard: stack must be %q", AdguardStackName)
	}
	if stack.Dir == "" {
		return fmt.Errorf("adguard: stack dir must not be empty")
	}
	return nil
}

// WriteAdguardStack generates ordinary Compose artifacts under dir:
// compose.yaml plus work/conf data directories. The result runs with
// vanilla `docker compose` — no Homestead at runtime (recovery rule,
// ADR-005). No configuration file is generated: AdGuard Home creates
// defaults on first start, and initial setup (credentials, upstreams,
// rewrites, filters) belongs to the provider adapter (S085) through the
// published web port.
func WriteAdguardStack(dir string) error {
	for _, subdir := range []string{"work", "conf"} {
		if err := os.MkdirAll(filepath.Join(dir, subdir), 0o755); err != nil {
			return fmt.Errorf("adguard: %s dir: %w", subdir, err)
		}
	}
	compose := `name: adguard
services:
  adguard:
    image: ` + AdguardImage + `
    container_name: ` + AdguardContainerName + `
    restart: unless-stopped
    ports:
      - "53:53/tcp"
      - "53:53/udp"
      - "3000:3000/tcp"
    volumes:
      - ./work:/opt/adguardhome/work
      - ./conf:/opt/adguardhome/conf
    networks:
      - homestead-platform
networks:
  homestead-platform:
    external: true
`
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		return fmt.Errorf("adguard: write compose.yaml: %w", err)
	}
	return nil
}

// AdguardDeployDefinition plans local DNS: the platform network (a
// platform service home), stack files, detached up, and a verify proving
// the DNS container runs. Idempotent throughout: existing networks
// short-circuit, files rewrite identically, up converges. DNS-level
// proof belongs to the provider adapter (S085).
func AdguardDeployDefinition(stack Stack) (operation.Definition, error) {
	if err := checkAdguardStack(stack); err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "adguard.deploy"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "ensure-" + NetworkPlatform,
		Run: func(ctx context.Context) error {
			return ensureNetwork(ctx, NetworkSpec{Name: NetworkPlatform, Driver: "bridge"})
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "write-stack",
		Run: func(ctx context.Context) error {
			return WriteAdguardStack(stack.Dir)
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
			return verifyService(ctx, stack, AdguardService)
		},
	})
	return def, nil
}
