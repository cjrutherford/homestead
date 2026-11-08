package firstrun

import (
	"context"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func paperlessManifest() catalog.Manifest {
	return catalog.Manifest{Name: "paperless-ngx", Title: "Paperless-ngx", FirstRun: &catalog.FirstRun{
		Routine: catalog.FirstRunPaperless, Service: "webserver", Account: "admin", Secret: "admin_password",
	}}
}

func TestPaperlessCreatesThePeopleGroupOnce(t *testing.T) {
	old, tries, pause := execIn, Tries, Pause
	defer func() { execIn, Tries, Pause = old, tries, pause }()
	Tries, Pause = 2, time.Millisecond
	for reply, want := range map[string]Result{"HOMESTEAD-PAPERLESS created\n": Created, "HOMESTEAD-PAPERLESS ready\n": Ready} {
		calls := 0
		execIn = func(_ context.Context, container, input string, args ...string) (string, error) {
			calls++
			if container != "paperless-ngx-webserver" {
				t.Fatalf("container %q", container)
			}
			if calls == 1 {
				return "", nil
			}
			if !strings.Contains(input, PaperlessPeopleGroup) || !strings.Contains(strings.Join(args, " "), "with-contenv") {
				t.Fatal("group script not sent on stdin through with-contenv")
			}
			return reply, nil
		}
		got, err := Run(context.Background(), paperlessManifest(), memSecrets{})
		if err != nil || got != want || calls != 2 {
			t.Fatalf("reply=%q got=%q calls=%d err=%v", reply, got, calls, err)
		}
	}
}

func TestPaperlessRefusesWhenPermissionsAreMissing(t *testing.T) {
	old := execIn
	defer func() { execIn = old }()
	execIn = func(_ context.Context, _, _ string, _ ...string) (string, error) {
		return "HOMESTEAD-PAPERLESS missing-permissions\n", nil
	}
	if _, err := Run(context.Background(), paperlessManifest(), memSecrets{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestPaperlessScriptNeverSendsASecretAndNamesTheManifestGroup(t *testing.T) {
	if strings.Contains(paperlessScript, "password") || strings.Contains(paperlessScript, "set_password") {
		t.Fatal("the script must not touch passwords")
	}
	m, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	pl, err := m.Get("paperless-ngx")
	if err != nil {
		t.Fatal(err)
	}
	env := pl.Services[0].Environment
	if env["PAPERLESS_SOCIAL_ACCOUNT_DEFAULT_GROUPS"] != PaperlessPeopleGroup || env["PAPERLESS_ACCOUNT_ALLOW_SIGNUPS"] != "false" {
		t.Fatalf("manifest does not join new sign-ins to %q or leaves sign-up open", PaperlessPeopleGroup)
	}
}
