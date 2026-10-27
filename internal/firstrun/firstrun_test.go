package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/secret"
)

type memSecrets map[string]string

func (m memSecrets) Get(name string) (string, error) {
	if v, ok := m[name]; ok {
		return v, nil
	}
	return "", secret.ErrNotFound
}

func (m memSecrets) SetApp(name, value string) error { m[name] = value; return nil }

func immichManifest() catalog.Manifest {
	return catalog.Manifest{Name: "immich", Title: "Immich",
		FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunImmich, Service: "server", Account: "admin@homestead.lan", Secret: "admin_password"}}
}

func stubExec(t *testing.T, replies ...string) *[]string {
	t.Helper()
	var stdins []string
	old, oldTries, oldPause := execIn, Tries, Pause
	Tries, Pause = 5, time.Millisecond
	execIn = func(_ context.Context, container, stdin string, args ...string) (string, error) {
		if container != "immich-server" || args[0] != "node" {
			t.Fatalf("exec %s %v", container, args)
		}
		for _, a := range args {
			if strings.Contains(a, "admin@homestead.lan") {
				t.Fatalf("account in argv: %v", args)
			}
		}
		stdins = append(stdins, stdin)
		reply := replies[0]
		if len(replies) > 1 {
			replies = replies[1:]
		}
		if reply == "exec-error" {
			return "", errors.New("container is not running")
		}
		return reply, nil
	}
	t.Cleanup(func() { execIn, Tries, Pause = old, oldTries, oldPause })
	return &stdins
}

func TestImmichWaitsThenCreatesAdministrator(t *testing.T) {
	stdins := stubExec(t, "exec-error", `{"state":"waiting","detail":"connect ECONNREFUSED"}`, `{"state":"created"}`)
	secrets := memSecrets{}
	res, err := Run(context.Background(), immichManifest(), secrets)
	if err != nil || res != Created {
		t.Fatalf("res %q err %v", res, err)
	}
	password := secrets["app_immich_admin_password"]
	if len(password) < 30 {
		t.Fatalf("password not generated: %q", password)
	}
	if len(*stdins) != 3 {
		t.Fatalf("tries %d", len(*stdins))
	}
	var in map[string]string
	if err := json.Unmarshal([]byte((*stdins)[2]), &in); err != nil || in["password"] != password || in["email"] != "admin@homestead.lan" {
		t.Fatalf("stdin %v %v", in, err)
	}
}

func TestImmichKeepsPasswordAndReadyIsNoChange(t *testing.T) {
	stubExec(t, `{"state":"ready"}`)
	secrets := memSecrets{"app_immich_admin_password": "kept"}
	res, err := Run(context.Background(), immichManifest(), secrets)
	if err != nil || res != Ready || secrets["app_immich_admin_password"] != "kept" {
		t.Fatalf("res %q err %v secrets %v", res, err, secrets)
	}
}

func TestImmichRefusalIsNamedAndRedacted(t *testing.T) {
	stubExec(t, `{"state":"failed","detail":"create the administrator: HTTP 400 \"bad\""}`)
	_, err := Run(context.Background(), immichManifest(), memSecrets{"app_immich_admin_password": "pw-secret"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("err %v", err)
	}
}

func TestImmichGivesUpBounded(t *testing.T) {
	stubExec(t, "exec-error")
	_, err := Run(context.Background(), immichManifest(), memSecrets{})
	if err == nil || !strings.Contains(err.Error(), "did not answer in time") {
		t.Fatalf("err %v", err)
	}
}

func TestNoFirstRunIsReady(t *testing.T) {
	res, err := Run(context.Background(), catalog.Manifest{Name: "x"}, nil)
	if err != nil || res != Ready {
		t.Fatalf("res %q err %v", res, err)
	}
}
