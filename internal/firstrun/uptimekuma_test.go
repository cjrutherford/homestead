package firstrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"homestead/internal/catalog"
)

func kumaManifest() catalog.Manifest {
	argv := func(mode string) []string { return []string{"node", "-e", "script", mode} }
	return catalog.Manifest{Name: "uptime-kuma", Title: "Uptime Kuma",
		FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunUptimeKuma, Service: "uptime-kuma", Account: KumaAdminAccount, Secret: "admin_password"},
		SSO: catalog.SSODeclaration{ForwardAuth: &catalog.ForwardAuth{OwnLoginOff: &catalog.ForwardOwnLoginOff{
			Prepare: argv("prepare"), Account: argv("account"), OnApply: argv("on"), OnOff: argv("off")}}}}
}

func stubKuma(t *testing.T, replies map[string][]string, restarts *int, inputs *[]string) {
	t.Helper()
	old, tries, pause, rc := execIn, Tries, Pause, restartContainer
	t.Cleanup(func() { execIn, Tries, Pause, restartContainer = old, tries, pause, rc })
	Tries, Pause = 4, time.Millisecond
	restartContainer = func(context.Context, string) error { *restarts++; return nil }
	execIn = func(_ context.Context, container, input string, args ...string) (string, error) {
		if container != "uptime-kuma-uptime-kuma" {
			t.Fatalf("unexpected container %s", container)
		}
		mode := args[len(args)-1]
		if mode == "account" {
			*inputs = append(*inputs, input)
		}
		q := replies[mode]
		if len(q) == 0 {
			return "", errors.New("no reply")
		}
		out := q[0]
		if len(q) > 1 {
			replies[mode] = q[1:]
		}
		return out, nil
	}
}

func TestKumaFirstRunChoosesDatabaseThenCreatesAccountFromAHashOnStdin(t *testing.T) {
	var restarts int
	var inputs []string
	stubKuma(t, map[string][]string{
		"prepare": {"chose the built-in SQLite database\n[restart]\n", "SQLite database in use\n"},
		"account": {"uptime-kuma account created\n"},
	}, &restarts, &inputs)
	secrets := memSecrets{}
	res, err := Run(context.Background(), kumaManifest(), secrets)
	if err != nil || res != Created || restarts != 1 {
		t.Fatalf("res=%q err=%v restarts=%d", res, err, restarts)
	}
	pw := secrets[catalog.SecretStoreName("uptime-kuma", "admin_password")]
	if pw == "" || len(inputs) != 1 || strings.Contains(inputs[0], pw) {
		t.Fatalf("password sealed=%v; stdin must carry only a hash", pw != "")
	}
	hash := strings.Split(strings.Split(inputs[0], `"hash":"`)[1], `"`)[0]
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil || !strings.HasPrefix(hash, "$2a$10$") {
		t.Fatalf("hash does not match the sealed password at cost 10: %s", hash[:7])
	}
}

func TestKumaFirstRunLeavesAnExistingAccountAlone(t *testing.T) {
	var restarts int
	var inputs []string
	stubKuma(t, map[string][]string{"prepare": {"SQLite database in use\n"}, "account": {"uptime-kuma account exists\n"}}, &restarts, &inputs)
	if res, err := Run(context.Background(), kumaManifest(), memSecrets{}); err != nil || res != Ready || restarts != 0 {
		t.Fatalf("res=%q err=%v", res, err)
	}
}

func TestKumaFirstRunWaitsForTheDatabase(t *testing.T) {
	var restarts int
	var inputs []string
	stubKuma(t, map[string][]string{"prepare": {"SQLite database in use\n"}}, &restarts, &inputs)
	// The account command never succeeds (the tables are not there yet).
	if _, err := Run(context.Background(), kumaManifest(), memSecrets{}); err == nil || !strings.Contains(err.Error(), "did not answer in time") {
		t.Fatalf("err = %v", err)
	}
}

func TestKumaFirstRunNeedsItsAccountCommand(t *testing.T) {
	m := kumaManifest()
	m.SSO.ForwardAuth.OwnLoginOff.Account = nil
	if _, err := Run(context.Background(), m, memSecrets{}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("err = %v", err)
	}
}
