package firstrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func giteaManifest() catalog.Manifest {
	return catalog.Manifest{Name: "gitea", Title: "Gitea", FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunGitea, Service: "gitea", Account: GiteaAdminAccount, Secret: "admin_password"}}
}

func stubGitea(t *testing.T, admins string, calls *[]string) {
	t.Helper()
	old, tries, pause := execIn, Tries, Pause
	t.Cleanup(func() { execIn, Tries, Pause = old, tries, pause })
	Tries, Pause = 3, time.Millisecond
	execIn = func(_ context.Context, container, input string, args ...string) (string, error) {
		if container != "gitea-gitea" || input != "" {
			t.Fatalf("unexpected exec %s input=%q", container, input)
		}
		*calls = append(*calls, strings.Join(args, " "))
		switch {
		case args[0] == "curl":
			return "", nil
		case strings.Contains(strings.Join(args, " "), "user list"):
			return admins, nil
		case strings.Contains(strings.Join(args, " "), "user create"):
			return "generated random password is 'S3cretS3cretS3cretS3cretS3cret12'\nNew user 'homestead-admin' has been successfully created!\n", nil
		}
		return "", nil
	}
}

func TestGiteaFirstRunCreatesAdminWithSealedPasswordNotInArgv(t *testing.T) {
	var calls []string
	stubGitea(t, "ID Username Email IsActive IsAdmin 2FA\n", &calls)
	secrets := memSecrets{}
	res, err := Run(context.Background(), giteaManifest(), secrets)
	if err != nil || res != Created {
		t.Fatalf("res=%q err=%v", res, err)
	}
	if secrets["app_gitea_admin_password"] != "S3cretS3cretS3cretS3cretS3cret12" {
		t.Fatal("password not sealed")
	}
	all := strings.Join(calls, "\n")
	if strings.Contains(all, "S3cret") || !strings.Contains(all, "--random-password") || !strings.Contains(all, "--must-change-password=false") || !strings.Contains(all, "--username homestead-admin") {
		t.Fatalf("calls: %s", all)
	}
}

func TestGiteaFirstRunLeavesExistingAdministratorAlone(t *testing.T) {
	var calls []string
	stubGitea(t, "ID Username Email IsActive IsAdmin 2FA\n1 owner o@example.org true true false\n", &calls)
	secrets := memSecrets{}
	res, err := Run(context.Background(), giteaManifest(), secrets)
	if err != nil || res != Ready || len(secrets) != 0 {
		t.Fatalf("res=%q err=%v secrets=%v", res, err, secrets)
	}
	for _, c := range calls {
		if strings.Contains(c, "user create") {
			t.Fatal("created over an existing administrator")
		}
	}
}

type failSeal struct{ memSecrets }

func (failSeal) SetApp(string, string) error { return errors.New("disk full") }

func TestGiteaFirstRunRemovesAccountWhenPasswordCannotBeSealed(t *testing.T) {
	var calls []string
	stubGitea(t, "ID Username Email IsActive IsAdmin 2FA\n", &calls)
	_, err := Run(context.Background(), giteaManifest(), failSeal{memSecrets{}})
	if err == nil || strings.Contains(err.Error(), "S3cret") {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(calls[len(calls)-1], "user delete --username homestead-admin") {
		t.Fatalf("account not removed: %v", calls)
	}
}
