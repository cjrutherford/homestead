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

func TestAdguardStackValue(t *testing.T) {
	stack := AdguardStack()
	if stack.Name != AdguardStackName || stack.Dir != DirFor(AdguardStackName) {
		t.Fatalf("stack = %+v", stack)
	}
	if err := stack.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestWriteAdguardStackArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAdguardStack(dir); err != nil {
		t.Fatalf("WriteAdguardStack: %v", err)
	}
	compose, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	for _, want := range []string{
		"name: adguard",
		"image: adguard/adguardhome:",
		"container_name: homestead-adguard",
		"unless-stopped",
		`"53:53/tcp"`,
		`"53:53/udp"`,
		`"3000:3000/tcp"`,
		"./work:/opt/adguardhome/work",
		"./conf:/opt/adguardhome/conf",
		"homestead-platform:",
		"external: true",
	} {
		if !strings.Contains(string(compose), want) {
			t.Fatalf("compose.yaml missing %q", want)
		}
	}
	for _, subdir := range []string{"work", "conf"} {
		info, err := os.Stat(filepath.Join(dir, subdir))
		if err != nil || !info.IsDir() {
			t.Fatalf("missing dir %s: %v", subdir, err)
		}
	}
	if err := WriteAdguardStack(dir); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
}

func stubAdguard(t *testing.T, existingPlatform bool, upFails bool) *[]string {
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
			return "{\"Service\":\"adguard\",\"State\":\"running\"}\n", nil
		}
		if upFails && strings.Contains(joined, " up -d") {
			return "", errors.New("exit status 1: compose failed")
		}
		return "", nil
	}
	t.Cleanup(func() { execRun = old })
	return &calls
}

func adguardStackIn(t *testing.T) Stack {
	t.Helper()
	stack := AdguardStack()
	stack.Dir = t.TempDir()
	return stack
}

func openAdguardRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

func TestAdguardDeployValidates(t *testing.T) {
	bad := Stack{Name: "caddy", Dir: DirFor("caddy"), Enabled: true}
	if _, err := AdguardDeployDefinition(bad); err == nil {
		t.Fatal("non-adguard stack accepted")
	}
	empty := Stack{Name: AdguardStackName, Enabled: true}
	if _, err := AdguardDeployDefinition(empty); err == nil {
		t.Fatal("empty dir accepted")
	}
}

func TestAdguardDeployFullFlow(t *testing.T) {
	calls := stubAdguard(t, false, false)
	runner := openAdguardRunner(t)
	def, err := AdguardDeployDefinition(adguardStackIn(t))
	if err != nil {
		t.Fatalf("AdguardDeployDefinition: %v", err)
	}
	if def.Type != "adguard.deploy" || len(def.Steps) != 4 {
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

func TestAdguardDeploySkipsPresentNetwork(t *testing.T) {
	calls := stubAdguard(t, true, false)
	runner := openAdguardRunner(t)
	stack := adguardStackIn(t)
	def, err := AdguardDeployDefinition(stack)
	if err != nil {
		t.Fatalf("AdguardDeployDefinition: %v", err)
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

func TestAdguardDeployFailureAborts(t *testing.T) {
	stubAdguard(t, true, true)
	runner := openAdguardRunner(t)
	def, err := AdguardDeployDefinition(adguardStackIn(t))
	if err != nil {
		t.Fatalf("AdguardDeployDefinition: %v", err)
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
