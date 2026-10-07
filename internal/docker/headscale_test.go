package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
)

func TestHeadscaleStackValue(t *testing.T) {
	stack := HeadscaleStack()
	if stack.Name != HeadscaleStackName || stack.Dir != DirFor(HeadscaleStackName) {
		t.Fatalf("stack = %+v", stack)
	}
	if err := stack.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestWriteHeadscaleStackArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := WriteHeadscaleStack(dir, "https://hs.example.com"); err != nil {
		t.Fatalf("WriteHeadscaleStack: %v", err)
	}
	compose, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	for _, want := range []string{
		"name: headscale",
		"image: headscale/headscale:",
		"container_name: homestead-headscale",
		"unless-stopped",
		`"8080:8080"`,
		"./config:/etc/headscale",
		"./data:/var/lib/headscale",
		"homestead-platform:",
		"external: true",
	} {
		if !strings.Contains(string(compose), want) {
			t.Fatalf("compose.yaml missing %q", want)
		}
	}
	config, err := os.ReadFile(filepath.Join(dir, "config", "config.yaml"))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	for _, want := range []string{
		"server_url: https://hs.example.com",
		"listen_addr: 0.0.0.0:8080",
		"127.0.0.1:5049",
		"db.sqlite",
	} {
		if !strings.Contains(string(config), want) {
			t.Fatalf("config.yaml missing %q", want)
		}
	}
	if err := WriteHeadscaleStack(dir, "https://hs.example.com"); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := WriteHeadscaleStack(dir, "http://192.168.1.5:8080"); err == nil ||
		!strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v", err)
	}
	if err := WriteHeadscaleStack(dir, "bogus"); err == nil {
		t.Fatal("wrote bogus URL")
	}
}

func stubHeadscale(t *testing.T, existingPlatform bool, upFails bool, userExists bool) *[]string {
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
		case strings.Contains(joined, "users create"):
			if userExists {
				return "", errors.New("exit status 1: User already exists")
			}
			return "User created", nil
		case strings.Contains(joined, "ps --format json"):
			return "{\"Service\":\"headscale\",\"State\":\"running\"}\n", nil
		}
		if upFails && strings.Contains(joined, " up -d") {
			return "", errors.New("exit status 1: compose failed")
		}
		return "", nil
	}
	t.Cleanup(func() { execRun = old })
	return &calls
}

func headscaleStackIn(t *testing.T) Stack {
	t.Helper()
	stack := HeadscaleStack()
	stack.Dir = t.TempDir()
	return stack
}

func TestHeadscaleDeployFlow(t *testing.T) {
	calls := stubHeadscale(t, false, false, false)
	runner := openCaddyRunner(t)
	stack := headscaleStackIn(t)
	def, err := HeadscaleDeployDefinition(stack, "https://hs.example.com")
	if err != nil {
		t.Fatalf("HeadscaleDeployDefinition: %v", err)
	}
	if def.Type != "headscale.deploy" || len(def.Steps) != 4 {
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
	if _, err := os.Stat(filepath.Join(stack.Dir, "config", "config.yaml")); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	bad := Stack{Name: "other", Dir: stack.Dir, Enabled: true}
	if _, err := HeadscaleDeployDefinition(bad, "https://hs.example.com"); err == nil {
		t.Fatal("non-headscale stack accepted")
	}
}

func TestCreateUserFlow(t *testing.T) {
	calls := stubHeadscale(t, true, false, false)
	runner := openCaddyRunner(t)
	stack := headscaleStackIn(t)
	def, err := CreateUserDefinition(stack, "owner")
	if err != nil {
		t.Fatalf("CreateUserDefinition: %v", err)
	}
	if def.Type != "headscale.user" || len(def.Steps) != 1 {
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
	joined := strings.Join(*calls, " ")
	for _, want := range []string{"exec homestead-headscale", "headscale users create owner"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("calls = %v, want %q", *calls, want)
		}
	}
	// Existing users converge.
	stubHeadscale(t, true, false, true)
	op, err = runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err = runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", done.Status)
	}
	if _, err := CreateUserDefinition(stack, "has space"); err == nil {
		t.Fatal("accepted bad user")
	}
}
