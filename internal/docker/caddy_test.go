package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
	"homestead/internal/store"
)

func TestCaddyStackValue(t *testing.T) {
	stack := CaddyStack()
	if stack.Name != CaddyStackName || stack.Dir != DirFor(CaddyStackName) {
		t.Fatalf("stack = %+v", stack)
	}
	if err := stack.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestWriteStackArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStack(dir); err != nil {
		t.Fatalf("WriteStack: %v", err)
	}
	compose, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	for _, want := range []string{
		"name: caddy",
		"image: caddy:2",
		"container_name: homestead-caddy",
		"unless-stopped",
		`"80:80"`,
		`"443:443"`,
		`"127.0.0.1:8480:8480"`,
		"./Caddyfile:/etc/caddy/Caddyfile:ro",
		"./routes:/etc/caddy/routes:ro",
		"./caddy-data:/data",
		"homestead-edge:",
		"external: true",
	} {
		if !strings.Contains(string(compose), want) {
			t.Fatalf("compose.yaml missing %q", want)
		}
	}
	caddyfile, err := os.ReadFile(filepath.Join(dir, "Caddyfile"))
	if err != nil {
		t.Fatalf("Caddyfile: %v", err)
	}
	for _, want := range []string{"admin localhost:2019", "import /etc/caddy/routes/*.caddy"} {
		if !strings.Contains(string(caddyfile), want) {
			t.Fatalf("Caddyfile missing %q", want)
		}
	}
	for _, subdir := range []string{"routes", "caddy-data", "caddy-config"} {
		info, err := os.Stat(filepath.Join(dir, subdir))
		if err != nil || !info.IsDir() {
			t.Fatalf("missing dir %s: %v", subdir, err)
		}
	}
	// Idempotent rewrite.
	if err := WriteStack(dir); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
}

func openCaddyRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

func stubCaddy(t *testing.T, existingEdge bool, upFails bool) *[]string {
	t.Helper()
	var calls []string
	old := execRun
	execRun = func(_ context.Context, name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		calls = append(calls, joined)
		switch {
		case strings.HasPrefix(joined, "docker network inspect homestead-edge"):
			if existingEdge {
				return `[{"Driver":"bridge"}]`, nil
			}
			return "", errors.New("exit status 1: Error: No such network: homestead-edge")
		case strings.Contains(joined, "network create"):
			return "edge-id", nil
		case strings.Contains(joined, "ps --format json"):
			return "{\"Service\":\"caddy\",\"State\":\"running\"}\n", nil
		}
		if upFails && strings.Contains(joined, " up -d") {
			return "", errors.New("exit status 1: compose failed")
		}
		return "", nil
	}
	t.Cleanup(func() { execRun = old })
	return &calls
}

func caddyStackIn(t *testing.T) Stack {
	t.Helper()
	stack := CaddyStack()
	stack.Dir = t.TempDir()
	return stack
}

func TestDeployDefinitionValidates(t *testing.T) {
	bad := Stack{Name: "other", Dir: DirFor("other"), Enabled: true}
	if _, err := DeployDefinition(bad); err == nil {
		t.Fatal("non-caddy stack accepted")
	}
}

func TestDeployFullFlow(t *testing.T) {
	calls := stubCaddy(t, false, false)
	runner := openCaddyRunner(t)
	def, err := DeployDefinition(caddyStackIn(t))
	if err != nil {
		t.Fatalf("DeployDefinition: %v", err)
	}
	if def.Type != "caddy.deploy" || len(def.Steps) != 4 {
		t.Fatalf("def = %+v", def)
	}
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	joined := strings.Join(*calls, "\n")
	for _, want := range []string{"network create", "up -d", "ps --format json"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("calls = %v, want %q", *calls, want)
		}
	}
}

func TestDeploySkipsPresentNetwork(t *testing.T) {
	calls := stubCaddy(t, true, false)
	runner := openCaddyRunner(t)
	stack := caddyStackIn(t)
	def, err := DeployDefinition(stack)
	if err != nil {
		t.Fatalf("DeployDefinition: %v", err)
	}
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	for _, call := range *calls {
		if strings.Contains(call, "network create") {
			t.Fatalf("created despite present: %v", *calls)
		}
	}
	if _, err := os.Stat(filepath.Join(stack.Dir, "compose.yaml")); err != nil {
		t.Fatalf("stack not written: %v", err)
	}
}

func TestDeployFailureAborts(t *testing.T) {
	stubCaddy(t, true, true)
	runner := openCaddyRunner(t)
	def, err := DeployDefinition(caddyStackIn(t))
	if err != nil {
		t.Fatalf("DeployDefinition: %v", err)
	}
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
}
