package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func mealieManifest() catalog.Manifest {
	return catalog.Manifest{Name: "mealie", Title: "Mealie", FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunMealie, Service: "mealie", Account: MealieSeededAccount, Secret: "admin_password"}}
}

func cwaManifest() catalog.Manifest {
	return catalog.Manifest{Name: "calibre-web-automated", Title: "Calibre-Web Automated", FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunCWA, Service: "calibre-web-automated", Account: CWASeededAccount, Secret: "admin_password"}}
}

// stubSteps runs a routine against scripted container replies and checks
// the password only ever travels on stdin.
func stubSteps(t *testing.T, container, program string, m catalog.Manifest, replies ...string) (Result, error, memSecrets, int) {
	t.Helper()
	old, tries, pause := execIn, Tries, Pause
	t.Cleanup(func() { execIn, Tries, Pause = old, tries, pause })
	Tries, Pause = 4, time.Millisecond
	calls := 0
	execIn = func(_ context.Context, c, input string, args ...string) (string, error) {
		if c != container || len(args) != 3 || args[0] != program || args[1] != "-c" {
			t.Fatalf("command %s %v", c, args)
		}
		var v struct{ Account, Password string }
		if err := json.Unmarshal([]byte(input), &v); err != nil || v.Account != m.FirstRun.Account || len(v.Password) < 30 {
			t.Fatalf("bad stdin: %v", err)
		}
		if strings.Contains(args[2], v.Password) {
			t.Fatal("password in argv")
		}
		r := replies[calls]
		calls++
		if r == "" {
			return "", errors.New("not running")
		}
		return r, nil
	}
	secrets := memSecrets{}
	res, err := Run(context.Background(), m, secrets)
	return res, err, secrets, calls
}

func TestMealieFirstRunWaitsThenReplacesSeededLogin(t *testing.T) {
	res, err, secrets, calls := stubSteps(t, "mealie-mealie", "python3", mealieManifest(), "", `{"state":"waiting","detail":"Mealie is not answering yet"}`, `{"state":"created"}`)
	if err != nil || res != Created || calls != 3 || len(secrets["app_mealie_admin_password"]) < 30 {
		t.Fatalf("res %q calls %d err %v", res, calls, err)
	}
}

func TestMealieFirstRunLeavesChangedAccountAndSaysWhatStaysOpen(t *testing.T) {
	res, err, _, _ := stubSteps(t, "mealie-mealie", "python3", mealieManifest(), `{"state":"ready","detail":"changed"}`)
	if err != nil || res != Ready {
		t.Fatalf("res %q err %v", res, err)
	}
	_, err, _, _ = stubSteps(t, "mealie-mealie", "python3", mealieManifest(), `{"state":"failed","detail":"Mealie refused the new password"}`)
	if err == nil || !strings.Contains(err.Error(), "MyPassword") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v", err)
	}
}

func TestMealieScriptReplacesPasswordAndIdentity(t *testing.T) {
	for _, want := range []string{"/api/users/password", `"MyPassword"`, "PUT", "/api/users/", "email", "signin(seeded, new)"} {
		if !strings.Contains(mealieScript, want) {
			t.Fatalf("script lacks %q", want)
		}
	}
	if MealieAdminEmail == MealieSeededAccount || strings.Contains(mealieScript, MealieSeededAccount) {
		t.Fatal("the seeded email must come from stdin and be replaced")
	}
}

func TestMealieRoutinePinnedToVerifiedVersion(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("mealie")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != MealieVerified || !strings.HasSuffix(m.Services[0].Image, ":v"+MealieVerified) {
		t.Fatalf("mealie is %s but its first-run routine was verified against %s. Before bumping: re-check the login, password and user update API and startup-info isFirstLogin, then update MealieVerified (S448).", m.Version, MealieVerified)
	}
	if m.FirstRun == nil || m.FirstRun.Routine != catalog.FirstRunMealie {
		t.Fatal("mealie declares no first run")
	}
}

func TestCWAFirstRunReplacesSeededPasswordAndName(t *testing.T) {
	res, err, secrets, calls := stubSteps(t, "calibre-web-automated-calibre-web-automated", "/lsiopy/bin/python", cwaManifest(), "", `{"state":"waiting","detail":"the application database does not exist yet"}`, `{"state":"created"}`)
	if err != nil || res != Created || calls != 3 || len(secrets["app_calibre_web_automated_admin_password"]) < 30 {
		t.Fatalf("res %q calls %d err %v keys %v", res, calls, err, secrets)
	}
	res, err, _, _ = stubSteps(t, "calibre-web-automated-calibre-web-automated", "/lsiopy/bin/python", cwaManifest(), `{"state":"ready"}`)
	if err != nil || res != Ready {
		t.Fatalf("res %q err %v", res, err)
	}
	_, err, _, _ = stubSteps(t, "calibre-web-automated-calibre-web-automated", "/lsiopy/bin/python", cwaManifest(), `{"state":"failed","detail":"table user lacks column name"}`)
	if err == nil || !strings.Contains(err.Error(), "admin123") || !strings.Contains(err.Error(), "lacks column") {
		t.Fatalf("err = %v", err)
	}
}

func TestCWARoutinePinnedToVerifiedVersion(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("calibre-web-automated")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != CWAVerified || !strings.HasSuffix(m.Services[0].Image, ":v"+CWAVerified) {
		t.Fatalf("calibre-web-automated is %s but its first-run routine was verified against %s. Before bumping: re-check app.db table user and the seeded admin password, then update CWAVerified (S448).", m.Version, CWAVerified)
	}
	for _, want := range []string{"check_password_hash(row[1], \"admin123\")", "AND password = ?", "os.setuid"} {
		if !strings.Contains(cwaScript, want) {
			t.Fatalf("script lacks %q", want)
		}
	}
}
