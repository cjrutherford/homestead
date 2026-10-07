package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homestead/internal/operation"
)

// CaddyImage pins the edge proxy (S081). Image updates arrive with the
// update flow (S190+); until then this constant is the version of record.
const CaddyImage = "caddy:2.11.7"

// Caddy stack layout.
const (
	// CaddyStackName is the Compose project backing the edge proxy.
	CaddyStackName = "caddy"
	// CaddyService is the proxy container (exec/reload target).
	CaddyService = "caddy"
)

// CaddyStack returns the edge-proxy stack value: fixed name, layout
// directory, the proxy service, enabled.
func CaddyStack() Stack {
	return Stack{
		Name:     CaddyStackName,
		Dir:      DirFor(CaddyStackName),
		Services: []string{CaddyService},
		Enabled:  true,
	}
}

// WriteStack generates ordinary Compose artifacts under dir: compose.yaml,
// Caddyfile, routes snippet directory, and data/config directories. The
// result runs with vanilla `docker compose` — no Homestead at runtime
// (recovery rule, ADR-005). Routes arrive as snippets (S083); the base
// Caddyfile only sets admin-localhost (S083 reloads through it) and the
// routes import.
func WriteStack(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "routes"), 0o755); err != nil {
		return fmt.Errorf("caddy: routes dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "caddy-data"), 0o755); err != nil {
		return fmt.Errorf("caddy: data dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "caddy-config"), 0o755); err != nil {
		return fmt.Errorf("caddy: config dir: %w", err)
	}
	files := map[string]string{
		"compose.yaml": composeFile(),
		"Caddyfile":    caddyfile(),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return fmt.Errorf("caddy: write %s: %w", name, err)
		}
	}
	return nil
}

func composeFile() string {
	return `name: caddy
services:
  caddy:
    image: ` + CaddyImage + `
    container_name: homestead-caddy
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./routes:/etc/caddy/routes:ro
      - ./caddy-data:/data
      - ./caddy-config:/config
    networks:
      - homestead-edge
networks:
  homestead-edge:
    external: true
`
}

func caddyfile() string {
	return `{
	# Homestead edge proxy (S081). Route snippets drop into routes/ (S083);
	# the admin endpoint stays container-local for config reloads.
	admin localhost:2019
}

import /etc/caddy/routes/*.caddy
`
}

// DeployDefinition plans the edge proxy: platform edge network, stack
// files, detached up, and a verify proving the proxy container runs.
// Idempotent throughout: existing networks short-circuit, files rewrite
// identically, up converges.
func DeployDefinition(stack Stack) (operation.Definition, error) {
	if stack.Name != CaddyStackName {
		return operation.Definition{}, fmt.Errorf("caddy: stack must be %q", CaddyStackName)
	}
	if stack.Dir == "" {
		return operation.Definition{}, fmt.Errorf("caddy: stack dir must not be empty")
	}
	def := operation.Definition{Type: "caddy.deploy"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "ensure-" + NetworkEdge,
		Run: func(ctx context.Context) error {
			return ensureNetwork(ctx, NetworkSpec{Name: NetworkEdge, Driver: "bridge"})
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "write-stack",
		Run: func(ctx context.Context) error {
			return WriteStack(stack.Dir)
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
			return verifyProxy(ctx, stack)
		},
	})
	return def, nil
}

// verifyProxy proves the proxy container runs via compose ps.
func verifyProxy(ctx context.Context, stack Stack) error {
	out, err := execRun(ctx, "docker",
		"compose", "--project-directory", stack.Dir, "--project-name", stack.Name,
		"ps", "--format", "json")
	if err != nil {
		return fmt.Errorf("verify proxy: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var service struct {
			Service string `json:"Service"`
			State   string `json:"State"`
		}
		if err := json.Unmarshal([]byte(line), &service); err != nil {
			return fmt.Errorf("verify proxy: %w", err)
		}
		if service.Service == CaddyService && service.State == "running" {
			return nil
		}
	}
	return fmt.Errorf("verify proxy: %s not running", CaddyService)
}
