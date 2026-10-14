package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func platformStub(t *testing.T, verifyState string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/platform/updates":
			_, _ = w.Write([]byte(`{"running":{},"plan":{"Components":[{"Component":"homestead-server","Current":"0.0.0","Target":"1.0.0","Verification":"verified"}],"Backup":true,"BackupWhy":"state","Warnings":[]},
				"releases":[{"component":"homestead-server","version":"1.0.0","counter":4,"state":"verified"},{"component":"homesteadctl","version":"","counter":0,"state":"failed","detail":"signature key is not trusted","plan_error":"refused: failed: signature key is not trusted"}],
				"refused":[{"component":"homesteadctl","version":"","counter":0,"state":"failed","plan_error":"refused: failed: signature key is not trusted"}],"installed":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/platform/updates/verify":
			if verifyState == "failed" {
				_, _ = w.Write([]byte(`{"component":"homestead-server","version":"0.1.0","counter":1,"state":"failed","refusal":"rollback","detail":"older"}`))
				return
			}
			_, _ = w.Write([]byte(`{"component":"homestead-server","version":"1.0.0","counter":4,"state":"verified","key_id":"ABCD"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/platform/trust-keys":
			_, _ = w.Write([]byte(`[{"key_id":"ABCD","source":"embedded","label":"HAI","placeholder":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/platform/trust-keys":
			_, _ = w.Write([]byte(`{"key_id":"EEFF","source":"owner"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/platform/trust-keys/ABCD":
			_, _ = w.Write([]byte(`{"key_id":"EEFF","source":"owner"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/platform/trust-keys/ABCD":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestUpdatesPlatformShowsVerification(t *testing.T) {
	url := platformStub(t, "")
	code, stdout, _ := invoke(t, []string{"--server", url, "updates", "platform"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"homestead-server 1.0.0 (counter 4): verified", "homesteadctl - (counter 0): failed", "not trusted", "[verified]", "refused (never planned): homesteadctl -: refused: failed"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestUpdatesVerify(t *testing.T) {
	url := platformStub(t, "")
	args := []string{"--server", url, "updates", "verify", "--component", "homestead-server", "--artifact", "a", "--checksums", "b", "--signature", "c"}
	code, stdout, _ := invoke(t, args)
	if code != exitOK || !strings.Contains(stdout, "verified with key ABCD") {
		t.Fatalf("exit=%d stdout=%s", code, stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "updates", "verify", "--component", "x"}); code != exitUsage {
		t.Fatalf("missing flags exit = %d", code)
	}
	failURL := platformStub(t, "failed")
	code, _, stderr := invoke(t, append([]string{"--server", failURL}, args[2:]...))
	if code != exitError || !strings.Contains(stderr, "--allow-older") {
		t.Fatalf("failed exit=%d stderr=%s", code, stderr)
	}
}

func TestUpdatesTrust(t *testing.T) {
	url := platformStub(t, "")
	keyFile := filepath.Join(t.TempDir(), "k.pub")
	if err := os.WriteFile(keyFile, []byte("untrusted comment: x\nabc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := invoke(t, []string{"--server", url, "updates", "trust", "list"}); code != exitOK || !strings.Contains(out, "ABCD  embedded") || !strings.Contains(out, "PLACEHOLDER") {
		t.Fatalf("list: %d %s", code, out)
	}
	if code, out, _ := invoke(t, []string{"--server", url, "updates", "trust", "add", "--key-file", keyFile}); code != exitOK || !strings.Contains(out, "EEFF") {
		t.Fatalf("add: %d %s", code, out)
	}
	if code, out, _ := invoke(t, []string{"--server", url, "updates", "trust", "replace", "ABCD", "--key-file", keyFile}); code != exitOK || !strings.Contains(out, "replaced by EEFF") {
		t.Fatalf("replace: %d %s", code, out)
	}
	if code, out, _ := invoke(t, []string{"--server", url, "updates", "trust", "remove", "ABCD"}); code != exitOK || !strings.Contains(out, "removed") {
		t.Fatalf("remove: %d %s", code, out)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "updates", "trust", "add"}); code != exitUsage {
		t.Fatalf("add without key exit = %d", code)
	}
}

func applyPlatformStub(t *testing.T) (string, *[]string) {
	t.Helper()
	var bodies []string
	polls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/platform/updates/apply":
			raw, _ := io.ReadAll(r.Body)
			bodies = append(bodies, string(raw))
			if strings.Contains(string(raw), `"allow_older":false`) && strings.Contains(string(raw), "/srv/older/") {
				w.Header().Set("X-Homestead-Error", "older-release-needs-override")
				http.Error(w, "platformapply: release is older than the installed release", http.StatusUnprocessableEntity)
				return
			}
			_, _ = w.Write([]byte(`{"id":12}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/platform/updates/clear-apply":
			_, _ = w.Write([]byte(`{"op_id":12,"component":"homestead-server","from_version":"0.1.0","to_version":"0.2.0"}`))
		case r.URL.Path == "/api/v1/operations/12/events":
			// The daemon restarts mid-stream: the stream ends non-terminal.
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: state\ndata: {\"id\":12,\"type\":\"platform.update\",\"status\":\"running\",\"steps\":[]}\n\n")
		case r.URL.Path == "/api/v1/operations/12":
			polls++
			if polls < 2 {
				http.Error(w, "restarting", http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"id":12,"type":"platform.update","status":"succeeded","steps":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL, &bodies
}

func TestUpdatesApplyPlatformFollowsAcrossRestart(t *testing.T) {
	applyFollowPoll = 5 * time.Millisecond
	t.Cleanup(func() { applyFollowPoll = 2 * time.Second })
	url, bodies := applyPlatformStub(t)
	code, stdout, stderr := invoke(t, []string{"--server", url, "updates", "apply-platform",
		"--component", "homestead-server", "--artifact", "/srv/rel/homestead-server",
		"--checksums", "/srv/rel/SUMS", "--signature", "/srv/rel/SUMS.minisig", "--follow"})
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"operation: 12", "succeeded", "passed its health check"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], `"artifact":"/srv/rel/homestead-server"`) ||
		!strings.Contains((*bodies)[0], `"checksums":"/srv/rel/SUMS"`) || !strings.Contains((*bodies)[0], `"signature":"/srv/rel/SUMS.minisig"`) {
		t.Fatalf("bodies = %v", *bodies)
	}
}

func TestUpdatesApplyPlatformOlderAndUsage(t *testing.T) {
	url, bodies := applyPlatformStub(t)
	code, _, stderr := invoke(t, []string{"--server", url, "updates", "apply-platform",
		"--component", "homestead-server", "--artifact", "/srv/older/homestead-server",
		"--checksums", "/srv/older/SUMS", "--signature", "/srv/older/SUMS.minisig"})
	if code != exitError || !strings.Contains(stderr, "--allow-older") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	code, stdout, _ := invoke(t, []string{"--server", url, "updates", "apply-platform",
		"--component", "homestead-server", "--artifact", "/srv/older/homestead-server",
		"--checksums", "/srv/older/SUMS", "--signature", "/srv/older/SUMS.minisig", "--allow-older"})
	if code != exitOK || !strings.Contains(stdout, "operation: 12") {
		t.Fatalf("override exit = %d, stdout = %q", code, stdout)
	}
	if !strings.Contains((*bodies)[1], `"allow_older":true`) {
		t.Fatalf("bodies = %v", *bodies)
	}
	for _, args := range [][]string{
		{"updates", "apply-platform"},
		{"updates", "apply-platform", "--component", "homestead-server", "--artifact", "/a"},
		{"updates", "apply-platform", "--bogus"},
	} {
		if code, _, _ := invoke(t, append([]string{"--server", url}, args...)); code != exitUsage {
			t.Fatalf("%v exit = %d, want usage", args, code)
		}
	}
}

func TestUpdatesPlatformClearApply(t *testing.T) {
	url, _ := applyPlatformStub(t)
	code, stdout, stderr := invoke(t, []string{"--server", url, "updates", "platform", "clear-apply"})
	if code != exitOK || !strings.Contains(stdout, "cleared the pending apply") || !strings.Contains(stdout, "operation 12") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "updates", "platform", "bogus"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestUpdatesRollbackPlatform(t *testing.T) {
	applyFollowPoll = 5 * time.Millisecond
	t.Cleanup(func() { applyFollowPoll = 2 * time.Second })
	var bodies []string
	polls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/platform/updates/rollback":
			raw, _ := io.ReadAll(r.Body)
			bodies = append(bodies, string(raw))
			if strings.Contains(string(raw), `"allow_older":false`) {
				w.Header().Set("X-Homestead-Error", "older-release-needs-override")
				http.Error(w, "platformapply: release is older than the installed release", http.StatusUnprocessableEntity)
				return
			}
			_, _ = w.Write([]byte(`{"id":12}`))
		case "/api/v1/operations/12/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: state\ndata: {\"id\":12,\"type\":\"platform.update\",\"status\":\"running\",\"steps\":[]}\n\n")
		case "/api/v1/operations/12":
			polls++
			_, _ = w.Write([]byte(`{"id":12,"type":"platform.update","status":"succeeded","steps":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	code, _, stderr := invoke(t, []string{"--server", ts.URL, "updates", "rollback-platform"})
	if code != exitError || !strings.Contains(stderr, "--allow-older") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	code, stdout, stderr := invoke(t, []string{"--server", ts.URL, "updates", "rollback-platform",
		"--component", "homesteadctl", "--allow-older", "--follow"})
	if code != exitOK || !strings.Contains(stdout, "operation: 12") || !strings.Contains(stdout, "succeeded") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if strings.Contains(stdout, "daemon restarted") {
		t.Fatalf("a CLI rollback does not restart the daemon: %s", stdout)
	}
	if len(bodies) != 2 || !strings.Contains(bodies[0], `"component":"homestead-server"`) || !strings.Contains(bodies[1], `"component":"homesteadctl"`) ||
		!strings.Contains(bodies[1], `"allow_older":true`) {
		t.Fatalf("bodies = %v", bodies)
	}
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "updates", "rollback-platform", "--bogus"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

// Only the stable error code triggers the override hint, never message text.
func TestUpdatesRollbackPlatformHintUsesErrorCodeNotText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the previous release is older but this is a different refusal", http.StatusUnprocessableEntity)
	}))
	t.Cleanup(ts.Close)
	code, _, stderr := invoke(t, []string{"--server", ts.URL, "updates", "rollback-platform"})
	if code != exitError || strings.Contains(stderr, "--allow-older") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}
