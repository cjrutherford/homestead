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

func bookstackManifest() catalog.Manifest {
	return catalog.Manifest{Name: "bookstack", Title: "BookStack", FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunBookStack, Service: "bookstack", Account: BookStackSeededAccount, Secret: "admin_password"}}
}

func TestBookStackFirstRunWaitsThenReplacesSeededPassword(t *testing.T) {
	old, tries, pause := execIn, Tries, Pause
	defer func() { execIn, Tries, Pause = old, tries, pause }()
	Tries, Pause = 4, time.Millisecond
	calls := 0
	execIn = func(_ context.Context, container, input string, args ...string) (string, error) {
		calls++
		if container != "bookstack-bookstack" || len(args) != 3 || args[0] != "php" || args[1] != "-r" {
			t.Fatalf("command %s %v", container, args)
		}
		var v struct{ Account, Password string }
		if err := json.Unmarshal([]byte(input), &v); err != nil || v.Account != BookStackSeededAccount || len(v.Password) < 30 {
			t.Fatalf("bad stdin: %v", err)
		}
		if strings.Contains(args[2], v.Password) || strings.Contains(args[2], "admin@") {
			t.Fatal("password or account in argv")
		}
		switch calls {
		case 1:
			return "", errors.New("not running")
		case 2:
			return `{"state":"waiting","detail":"BookStack is still migrating its database"}`, nil
		}
		return `{"state":"created"}`, nil
	}
	secrets := memSecrets{}
	res, err := Run(context.Background(), bookstackManifest(), secrets)
	if err != nil || res != Created || calls != 3 || len(secrets["app_bookstack_admin_password"]) < 30 {
		t.Fatalf("res %q calls %d err %v", res, calls, err)
	}
}

func TestBookStackFirstRunLeavesChangedAccountAndRedactsErrors(t *testing.T) {
	old, tries, pause := execIn, Tries, Pause
	defer func() { execIn, Tries, Pause = old, tries, pause }()
	Tries, Pause = 2, time.Millisecond
	execIn = func(context.Context, string, string, ...string) (string, error) {
		return `{"state":"ready","detail":"the account already has its own password"}`, nil
	}
	if res, err := Run(context.Background(), bookstackManifest(), memSecrets{}); err != nil || res != Ready {
		t.Fatalf("res %q err %v", res, err)
	}
	var stdin string
	execIn = func(_ context.Context, _, input string, _ ...string) (string, error) {
		stdin = input
		var v struct{ Password string }
		_ = json.Unmarshal([]byte(input), &v)
		return "", errors.New("exec failed with " + v.Password)
	}
	_, err := Run(context.Background(), bookstackManifest(), memSecrets{})
	var v struct{ Password string }
	_ = json.Unmarshal([]byte(stdin), &v)
	if err == nil || strings.Contains(err.Error(), v.Password) || !strings.Contains(err.Error(), `"password"`) {
		t.Fatalf("err = %v", err)
	}
	execIn = func(context.Context, string, string, ...string) (string, error) {
		return `{"state":"failed","detail":"table users lacks column email"}`, nil
	}
	if _, err := Run(context.Background(), bookstackManifest(), memSecrets{}); err == nil || !strings.Contains(err.Error(), "lacks column") {
		t.Fatalf("err = %v", err)
	}
}

func TestBookStackFirstRunRefusesOtherAccounts(t *testing.T) {
	m := bookstackManifest()
	m.FirstRun.Account = "someone@example.com"
	if _, err := Run(context.Background(), m, memSecrets{}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("err = %v", err)
	}
}

// TestBookStackScriptGuards: the script compare-and-sets only the seeded
// password, waits for migrations, checks columns, and keeps secrets and
// raw exceptions out of its output.
func TestBookStackScriptGuards(t *testing.T) {
	for _, want := range []string{"password_verify('password'", "AND password = ?", "migrations", "information_schema.COLUMNS", "FILE__DB_PASSWORD", "getenv('DB_HOST')"} {
		if !strings.Contains(bookstackScript, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	for _, bad := range []string{"getMessage", "print_r", "var_dump", "DELETE", "INSERT", "DROP "} {
		if strings.Contains(bookstackScript, bad) {
			t.Errorf("script contains %q", bad)
		}
	}
}
