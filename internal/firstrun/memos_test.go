package firstrun

import (
	"context"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func memosManifest() catalog.Manifest {
	return catalog.Manifest{Name: "memos", Title: "Memos", FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunMemos, Service: "memos", Account: MemosAdminAccount, Secret: "admin_password"}}
}

func stubMemos(t *testing.T, needsSetup string, calls *[]string, bodies *[]string) {
	t.Helper()
	old, tries, pause := execIn, Tries, Pause
	t.Cleanup(func() { execIn, Tries, Pause = old, tries, pause })
	Tries, Pause = 3, time.Millisecond
	execIn = func(_ context.Context, container, input string, args ...string) (string, error) {
		if container != "memos-memos" {
			t.Fatalf("unexpected container %s", container)
		}
		*calls = append(*calls, strings.Join(args, " "))
		*bodies = append(*bodies, input)
		if strings.HasSuffix(args[len(args)-1], "/api/instance/profile") {
			return `{"version":"26.10.0","needsSetup":` + needsSetup + `}`, nil
		}
		return `{"name":"users/homestead-admin","role":"ADMIN"}`, nil
	}
}

func TestMemosFirstRunCreatesAdminWithSealedPasswordOnStdin(t *testing.T) {
	var calls, bodies []string
	stubMemos(t, "true", &calls, &bodies)
	secrets := memSecrets{}
	res, err := Run(context.Background(), memosManifest(), secrets)
	if err != nil || res != Created {
		t.Fatalf("res=%q err=%v", res, err)
	}
	pw := secrets["app_memos_admin_password"]
	if pw == "" || !strings.Contains(strings.Join(bodies, "\n"), pw) || strings.Contains(strings.Join(calls, "\n"), pw) {
		t.Fatal("password must be sealed, sent on stdin and not in argv")
	}
}

func TestMemosFirstRunLeavesAnExistingSetupAlone(t *testing.T) {
	var calls, bodies []string
	stubMemos(t, "false", &calls, &bodies)
	secrets := memSecrets{}
	res, err := Run(context.Background(), memosManifest(), secrets)
	if err != nil || res != Ready || len(secrets) != 0 || len(calls) != 1 {
		t.Fatalf("res=%q err=%v calls=%v", res, err, calls)
	}
}
