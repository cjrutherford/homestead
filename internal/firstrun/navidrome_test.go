package firstrun

import (
	"context"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func navidromeManifest() catalog.Manifest {
	return catalog.Manifest{Name: "navidrome", Title: "Navidrome", FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunNavidrome, Service: "navidrome", Account: NavidromeAdminAccount, Secret: "admin_password"}}
}

const navidromeListHeader = "user id,username,user's name,user email,admin,created at,updated at,last access,last login,libraries\n"

func stubNavidrome(t *testing.T, users string, calls, bodies *[]string) {
	t.Helper()
	old, tries, pause := execIn, Tries, Pause
	t.Cleanup(func() { execIn, Tries, Pause = old, tries, pause })
	Tries, Pause = 3, time.Millisecond
	execIn = func(_ context.Context, container, input string, args ...string) (string, error) {
		if container != "navidrome-navidrome" {
			t.Fatalf("unexpected container %s", container)
		}
		*calls = append(*calls, strings.Join(args, " "))
		*bodies = append(*bodies, input)
		switch {
		case strings.HasSuffix(args[len(args)-1], "/ping"):
			return "pong", nil
		case args[0] == "/app/navidrome":
			return navidromeListHeader + users, nil
		}
		return `{"id":"x","isAdmin":true}`, nil
	}
}

func TestNavidromeFirstRunCreatesAdminWithSealedPasswordOnStdin(t *testing.T) {
	var calls, bodies []string
	stubNavidrome(t, "", &calls, &bodies)
	secrets := memSecrets{}
	res, err := Run(context.Background(), navidromeManifest(), secrets)
	if err != nil || res != Created {
		t.Fatalf("res=%q err=%v", res, err)
	}
	pw := secrets["app_navidrome_admin_password"]
	if pw == "" || !strings.Contains(strings.Join(bodies, "\n"), pw) || strings.Contains(strings.Join(calls, "\n"), pw) {
		t.Fatal("password must be sealed, sent on stdin and not in argv")
	}
	if !strings.Contains(strings.Join(calls, "\n"), "/auth/createAdmin") {
		t.Fatalf("calls = %v", calls)
	}
}

func TestNavidromeFirstRunLeavesAnExistingAdministratorAlone(t *testing.T) {
	var calls, bodies []string
	stubNavidrome(t, "abc,someone,Someone,,true,2026-11-07T20:35:19.4Z,2026-11-07T20:35:19.4Z,never,never,'1:/music'\n", &calls, &bodies)
	secrets := memSecrets{}
	res, err := Run(context.Background(), navidromeManifest(), secrets)
	if err != nil || res != Ready || len(secrets) != 0 || strings.Contains(strings.Join(calls, "\n"), "createAdmin") {
		t.Fatalf("res=%q err=%v calls=%v", res, err, calls)
	}
}

func TestNavidromeFirstRunTreatsANonAdministratorAsNoAdministrator(t *testing.T) {
	var calls, bodies []string
	stubNavidrome(t, "abc,someone,Someone,,false,2026-11-07T20:35:19.4Z,2026-11-07T20:35:19.4Z,never,never,'1:/music'\n", &calls, &bodies)
	if res, err := Run(context.Background(), navidromeManifest(), memSecrets{}); err != nil || res != Created {
		t.Fatalf("res=%q err=%v", res, err)
	}
}

func TestNavidromeFirstRunRefusedCreateKeepsNoClaim(t *testing.T) {
	var calls, bodies []string
	stubNavidrome(t, "", &calls, &bodies)
	base := execIn
	execIn = func(ctx context.Context, c, in string, args ...string) (string, error) {
		if strings.HasSuffix(args[len(args)-1], "/auth/createAdmin") {
			return "", context.DeadlineExceeded
		}
		return base(ctx, c, in, args...)
	}
	if _, err := Run(context.Background(), navidromeManifest(), memSecrets{}); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v", err)
	}
}
