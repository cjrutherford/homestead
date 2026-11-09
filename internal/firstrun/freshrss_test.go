package firstrun

import (
	"context"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func freshrssManifest() catalog.Manifest {
	return catalog.Manifest{Name: "freshrss", Title: "FreshRSS", FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunFreshRSS, Service: "freshrss", Account: "admin", Secret: "admin_password"}}
}

type freshrssCall struct{ stdin, line string }

func stubFreshRSS(t *testing.T, installed bool, users string, calls *[]freshrssCall) {
	t.Helper()
	old, tries, pause := execIn, Tries, Pause
	t.Cleanup(func() { execIn, Tries, Pause = old, tries, pause })
	Tries, Pause = 3, time.Millisecond
	execIn = func(_ context.Context, container, input string, args ...string) (string, error) {
		line := strings.Join(args, " ")
		*calls = append(*calls, freshrssCall{input, line})
		if container != "freshrss-freshrss" {
			t.Fatalf("container %s", container)
		}
		switch {
		case strings.Contains(line, "applied_migrations") && !installed:
			return "", context.DeadlineExceeded
		case strings.Contains(line, "list-users"):
			return users, nil
		}
		return "", nil
	}
}

func TestFreshRSSInstallsForOwnerWithSealedPasswordOnStdin(t *testing.T) {
	var calls []freshrssCall
	stubFreshRSS(t, false, "", &calls)
	secrets := memSecrets{}
	res, err := RunAs(context.Background(), freshrssManifest(), secrets, "akadmin")
	pw := secrets["app_freshrss_admin_password"]
	if err != nil || res != Created || len(pw) < 20 {
		t.Fatalf("res=%q err=%v", res, err)
	}
	var install, create bool
	for _, c := range calls {
		if strings.Contains(c.line, pw) {
			t.Fatal("password in argv")
		}
		install = install || strings.Contains(c.line, "do-install.php --default-user akadmin --auth-type form --db-type sqlite")
		if strings.Contains(c.line, "create-user.php") {
			create = c.stdin == pw && strings.Contains(c.line, `"$(cat)"`)
		}
	}
	if !install || !create {
		t.Fatalf("install=%v create=%v calls=%v", install, create, calls)
	}
}

func TestFreshRSSLeavesAnInstalledAppWithItsOwnerUser(t *testing.T) {
	var calls []freshrssCall
	stubFreshRSS(t, true, "akadmin\n", &calls)
	secrets := memSecrets{}
	res, err := RunAs(context.Background(), freshrssManifest(), secrets, "akadmin")
	if err != nil || res != Ready || len(secrets) != 0 {
		t.Fatalf("res=%q err=%v secrets=%v", res, err, secrets)
	}
	for _, c := range calls {
		if strings.Contains(c.line, "do-install") || strings.Contains(c.line, "create-user") {
			t.Fatalf("changed an installed app: %s", c.line)
		}
	}
}

func TestFreshRSSAddsMissingOwnerAndMakesItDefault(t *testing.T) {
	var calls []freshrssCall
	stubFreshRSS(t, true, "admin\n", &calls)
	secrets := memSecrets{"app_freshrss_admin_password": "kept-password-kept-password"}
	res, err := RunAs(context.Background(), freshrssManifest(), secrets, "akadmin")
	if err != nil || res != Created {
		t.Fatalf("res=%q err=%v", res, err)
	}
	var made, def bool
	for _, c := range calls {
		made = made || (strings.Contains(c.line, "create-user") && c.stdin == "kept-password-kept-password")
		def = def || strings.Contains(c.line, "reconfigure.php --default-user akadmin")
	}
	if !made || !def {
		t.Fatalf("calls=%v", calls)
	}
}

func TestFreshRSSFallsBackToManifestAccount(t *testing.T) {
	var calls []freshrssCall
	stubFreshRSS(t, false, "", &calls)
	if _, err := Run(context.Background(), freshrssManifest(), memSecrets{}); err != nil {
		t.Fatal(err)
	}
	var all string
	for _, c := range calls {
		all += c.line + "\n"
	}
	if !strings.Contains(all, "--default-user admin") {
		t.Fatalf("calls=%v", calls)
	}
}
