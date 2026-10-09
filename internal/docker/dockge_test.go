package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/layout"
	"homestead/internal/operation"
	"homestead/internal/store"
)

func TestDockgeStackValue(t *testing.T) {
	stack := DockgeStack()
	if stack.Name != DockgeStackName || stack.Dir != DirFor(DockgeStackName) {
		t.Fatalf("stack = %+v", stack)
	}
	if err := stack.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestWriteDockgeStackArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDockgeStack(dir); err != nil {
		t.Fatalf("WriteDockgeStack: %v", err)
	}
	compose, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	stacks := layout.StacksDir.Path + ":" + layout.StacksDir.Path
	for _, want := range []string{
		"name: dockge",
		"image: louislam/dockge:",
		"container_name: homestead-dockge",
		"unless-stopped",
		`"5001:5001/tcp"`,
		"/var/run/docker.sock:/var/run/docker.sock",
		"./data:/app/data",
		stacks,
		"DOCKGE_STACKS_DIR=" + layout.StacksDir.Path,
		"homestead-platform:",
		"external: true",
	} {
		if !strings.Contains(string(compose), want) {
			t.Fatalf("compose.yaml missing %q", want)
		}
	}
	info, err := os.Stat(filepath.Join(dir, "data"))
	if err != nil || !info.IsDir() {
		t.Fatalf("missing data dir: %v", err)
	}
	if err := WriteDockgeStack(dir); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
}

func stubDockge(t *testing.T, existingPlatform bool, upFails bool) *[]string {
	t.Helper()
	var calls []string
	old := execRun
	execRun = func(_ context.Context, name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		calls = append(calls, joined)
		switch {
		case strings.HasPrefix(joined, "docker network inspect homestead-platform"):
			if existingPlatform {
				return `[{"Driver":"bridge"}]`, nil
			}
			return "", errors.New("exit status 1: Error: No such network: homestead-platform")
		case strings.Contains(joined, "network create"):
			return "platform-id", nil
		case strings.Contains(joined, "ps --format json"):
			return "{\"Service\":\"dockge\",\"State\":\"running\"}\n", nil
		}
		if upFails && strings.Contains(joined, " up -d") {
			return "", errors.New("exit status 1: compose failed")
		}
		return "", nil
	}
	t.Cleanup(func() { execRun = old })
	return &calls
}

func dockgeStackIn(t *testing.T) Stack {
	t.Helper()
	stack := DockgeStack()
	stack.Dir = t.TempDir()
	return stack
}

func openDockgeRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

func TestDockgeDeployValidates(t *testing.T) {
	bad := Stack{Name: "caddy", Dir: DirFor("caddy"), Enabled: true}
	if _, err := DockgeDeployDefinition(bad); err == nil {
		t.Fatal("non-dockge stack accepted")
	}
	empty := Stack{Name: DockgeStackName, Enabled: true}
	if _, err := DockgeDeployDefinition(empty); err == nil {
		t.Fatal("empty dir accepted")
	}
}

func TestDockgeDeployFullFlow(t *testing.T) {
	calls := stubDockge(t, false, false)
	runner := openDockgeRunner(t)
	def, err := DockgeDeployDefinition(dockgeStackIn(t))
	if err != nil {
		t.Fatalf("DockgeDeployDefinition: %v", err)
	}
	if def.Type != "dockge.deploy" || len(def.Steps) != 4 {
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

func TestDockgeDeploySkipsPresentNetwork(t *testing.T) {
	calls := stubDockge(t, true, false)
	runner := openDockgeRunner(t)
	stack := dockgeStackIn(t)
	def, err := DockgeDeployDefinition(stack)
	if err != nil {
		t.Fatalf("DockgeDeployDefinition: %v", err)
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

func TestDockgeDeployFailureAborts(t *testing.T) {
	stubDockge(t, true, true)
	runner := openDockgeRunner(t)
	def, err := DockgeDeployDefinition(dockgeStackIn(t))
	if err != nil {
		t.Fatalf("DockgeDeployDefinition: %v", err)
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
