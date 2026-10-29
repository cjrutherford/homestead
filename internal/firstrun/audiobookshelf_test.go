package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func absManifest() catalog.Manifest {
	return catalog.Manifest{Name: "audiobookshelf", Title: "Audiobookshelf", FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunAudiobookshelf, Service: "audiobookshelf", Account: "homestead-admin", Secret: "admin_password"}}
}

func TestAudiobookshelfFirstRunSealsAndPassesPasswordOnStdin(t *testing.T) {
	old, tries, pause := execIn, Tries, Pause
	defer func() { execIn, Tries, Pause = old, tries, pause }()
	Tries = 3
	Pause = time.Millisecond
	calls := 0
	execIn = func(_ context.Context, container, input string, args ...string) (string, error) {
		calls++
		if container != "audiobookshelf-audiobookshelf" || len(args) != 3 || args[0] != "node" || args[1] != "-e" {
			t.Fatalf("command %s %v", container, args)
		}
		var v struct {
			Account  string `json:"account"`
			Password string `json:"password"`
		}
		if err := json.Unmarshal([]byte(input), &v); err != nil || v.Account != "homestead-admin" || (calls > 2 && len(v.Password) < 30) {
			t.Fatalf("bad stdin: %v", err)
		}
		for _, a := range args {
			if v.Password != "" && strings.Contains(a, v.Password) {
				t.Fatal("secret in command")
			}
		}
		if calls == 1 {
			return "", errors.New("not running")
		}
		if calls == 2 {
			return `{"state":"uninitialized"}`, nil
		}
		return `{"state":"created"}`, nil
	}
	secrets := memSecrets{}
	result, err := Run(context.Background(), absManifest(), secrets)
	if err != nil || result != Created || calls != 3 || len(secrets["app_audiobookshelf_admin_password"]) < 30 {
		t.Fatalf("result %q calls %d err %v", result, calls, err)
	}
}

func TestAudiobookshelfExistingAdminRemainsUntouchedAndErrorsAreRedacted(t *testing.T) {
	old, tries, pause := execIn, Tries, Pause
	defer func() { execIn, Tries, Pause = old, tries, pause }()
	Tries = 1
	Pause = time.Millisecond
	secrets := memSecrets{"app_audiobookshelf_admin_password": "secret-to-redact"}
	execIn = func(_ context.Context, _, _ string, _ ...string) (string, error) { return `{"state":"ready"}`, nil }
	result, err := Run(context.Background(), absManifest(), secrets)
	if err != nil || result != Ready {
		t.Fatalf("%q %v", result, err)
	}
	execIn = func(_ context.Context, _, _ string, _ ...string) (string, error) {
		return `{"state":"failed","detail":"secret-to-redact"}`, nil
	}
	_, err = Run(context.Background(), absManifest(), secrets)
	if err == nil || strings.Contains(err.Error(), "secret-to-redact") {
		t.Fatalf("unredacted error: %v", err)
	}
}

func TestAudiobookshelfScriptAgainstHTTP(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the script integration test")
	}
	initialized := false
	created := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"app": "audiobookshelf", "serverVersion": "2.37.1", "isInit": initialized})
		case "/init":
			var body struct {
				NewRoot struct{ Username, Password string }
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NewRoot.Username != "homestead-admin" || body.NewRoot.Password != "test-secret" {
				t.Errorf("bad init body: %v", err)
				http.Error(w, "bad", 400)
				return
			}
			initialized = true
			created++
			w.WriteHeader(200)
		case "/login":
			var body struct{ Username, Password string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username != "homestead-admin" || body.Password != "test-secret" {
				t.Errorf("bad login body: %v", err)
				http.Error(w, "bad", 401)
				return
			}
			_, _ = w.Write([]byte(`{"token":"token-must-not-leak"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	script := strings.Replace(audiobookshelfScript, "http://127.0.0.1:80", srv.URL, 1)
	run := func(input string) string {
		cmd := exec.Command(node, "-e", script)
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("node: %v %s", err, out)
		}
		if strings.Contains(string(out), "test-secret") || strings.Contains(string(out), "token-must-not-leak") {
			t.Fatal("credential leaked")
		}
		return strings.TrimSpace(string(out))
	}
	if got := run(`{"account":"homestead-admin","initialize":false}`); got != `{"state":"uninitialized"}` {
		t.Fatal(got)
	}
	if got := run(`{"account":"homestead-admin","password":"test-secret","initialize":true}`); got != `{"state":"created"}` {
		t.Fatal(got)
	}
	if got := run(`{"account":"homestead-admin","initialize":false}`); got != `{"state":"ready"}` || created != 1 {
		t.Fatalf("%s created=%d", got, created)
	}
}

func TestAudiobookshelfExistingAdminDoesNotGeneratePassword(t *testing.T) {
	old, tries, pause := execIn, Tries, Pause
	defer func() { execIn, Tries, Pause = old, tries, pause }()
	Tries = 1
	Pause = time.Millisecond
	calls := 0
	execIn = func(_ context.Context, _, _ string, _ ...string) (string, error) {
		calls++
		return `{"state":"ready"}`, nil
	}
	secrets := memSecrets{}
	result, err := Run(context.Background(), absManifest(), secrets)
	if err != nil || result != Ready || calls != 1 || len(secrets) != 0 {
		t.Fatalf("result %q calls %d secrets %d err %v", result, calls, len(secrets), err)
	}
}
