package docker

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
	"homestead/internal/store"
)

func stubNetEnv(t *testing.T, existing map[string]string, failOn string) *[]string {
	t.Helper()
	var calls []string
	created := map[string]string{}
	old := execRun
	execRun = func(_ context.Context, name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		calls = append(calls, joined)
		if failOn != "" && strings.Contains(joined, failOn) {
			return "", errors.New("exit status 1: boom")
		}
		if name == "docker" && len(args) >= 3 && args[0] == "network" && args[1] == "inspect" {
			if driver, ok := existing[args[2]]; ok {
				return `[{"Driver":"` + driver + `"}]`, nil
			}
			if driver, ok := created[args[2]]; ok {
				return `[{"Driver":"` + driver + `"}]`, nil
			}
			return "", errors.New("exit status 1: Error: No such network: " + args[2])
		}
		if name == "docker" && len(args) >= 3 && args[0] == "network" && args[1] == "create" {
			driver := "bridge"
			for i, arg := range args {
				if arg == "--driver" && i+1 < len(args) {
					driver = args[i+1]
				}
			}
			created[args[len(args)-1]] = driver
			return "", nil
		}
		return "", nil
	}
	t.Cleanup(func() { execRun = old })
	return &calls
}

func openNetRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

func TestStandardNetworks(t *testing.T) {
	networks := StandardNetworks()
	if len(networks) != 2 || networks[0].Name != NetworkEdge || networks[1].Name != NetworkPlatform {
		t.Fatalf("networks = %+v", networks)
	}
	for _, network := range networks {
		if network.Driver != "bridge" || network.Internal {
			t.Fatalf("network = %+v, want bridged egress", network)
		}
	}
}

func TestEnsureCreatesMissing(t *testing.T) {
	calls := stubNetEnv(t, map[string]string{}, "")
	runner := openNetRunner(t)
	op, err := runner.Start(context.Background(), EnsureDefinition(StandardNetworks()), "")
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
	for _, want := range []string{
		"network create --driver bridge --label homestead.managed=true homestead-edge",
		"network create --driver bridge --label homestead.managed=true homestead-platform",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("calls = %v, want %q", *calls, want)
		}
	}
	if len(done.Steps) != 3 {
		t.Fatalf("steps = %d, want 2 ensures + verify", len(done.Steps))
	}
}

func TestEnsureSkipsPresent(t *testing.T) {
	calls := stubNetEnv(t, map[string]string{
		"homestead-edge":     "bridge",
		"homestead-platform": "bridge",
	}, "")
	runner := openNetRunner(t)
	op, err := runner.Start(context.Background(), EnsureDefinition(StandardNetworks()), "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q", done.Status)
	}
	for _, call := range *calls {
		if strings.Contains(call, "network create") {
			t.Fatalf("created despite present: %v", *calls)
		}
	}
}

func TestEnsureRefusesDriverMismatch(t *testing.T) {
	stubNetEnv(t, map[string]string{"homestead-edge": "macvlan"}, "")
	runner := openRunner(t)
	op, err := runner.Start(context.Background(), EnsureDefinition(StandardNetworks()), "")
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
	if !strings.Contains(done.Error, "foreign configuration") {
		t.Fatalf("error = %q", done.Error)
	}
}

func TestEnsureFailureAborts(t *testing.T) {
	stubNetEnv(t, map[string]string{}, "network create")
	runner := openRunner(t)
	op, err := runner.Start(context.Background(), EnsureDefinition(StandardNetworks()), "")
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
