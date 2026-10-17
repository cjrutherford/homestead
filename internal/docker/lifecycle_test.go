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

func testStack(t *testing.T) Stack {
	t.Helper()
	// Real layout path: execution is stubbed, so nothing touches the host.
	return Stack{Name: "demo", Dir: DirFor("demo"), Enabled: true}
}

func TestDefinitionsValidateStack(t *testing.T) {
	bad := Stack{Name: "Bad!"}
	for _, build := range []func(Stack, ...string) (operation.Definition, error){
		PullDefinition, StopDefinition,
	} {
		if _, err := build(bad); err == nil {
			t.Fatal("invalid stack accepted")
		}
	}
}

func TestDefinitionTypes(t *testing.T) {
	stack := testStack(t)
	cases := []struct {
		build    func(Stack, ...string) (operation.Definition, error)
		opType   string
		steps    int
		services []string
	}{
		{PullDefinition, OpPull, 1, nil},
		{StopDefinition, OpStop, 1, nil},
	}
	for _, tc := range cases {
		def, err := tc.build(stack, tc.services...)
		if err != nil {
			t.Fatalf("%s: %v", tc.opType, err)
		}
		if def.Type != tc.opType || len(def.Steps) != tc.steps {
			t.Fatalf("%+v, want %s with %d steps", def, tc.opType, tc.steps)
		}
	}
}

func stubCompose(t *testing.T, failOn string) *[]string {
	t.Helper()
	var calls []string
	old := execRun
	execRun = func(_ context.Context, name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		calls = append(calls, joined)
		if failOn != "" && strings.Contains(joined, failOn) {
			return "", errors.New("exit status 1: boom")
		}
		return "", nil
	}
	t.Cleanup(func() { execRun = old })
	return &calls
}

func openLifecycleRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

func TestLifecycleFailureAborts(t *testing.T) {
	stubCompose(t, "stop")
	runner := openLifecycleRunner(t)
	def, err := StopDefinition(testStack(t))
	if err != nil {
		t.Fatalf("StopDefinition: %v", err)
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
	if !strings.Contains(done.Error, "stop demo") {
		t.Fatalf("error = %q, want action context", done.Error)
	}
}
