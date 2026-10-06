package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/config"
	"homestead/internal/desired"
	"homestead/internal/schema"
	"homestead/internal/server"
)

func testDaemon(t *testing.T) string {
	t.Helper()
	s := schema.Schema{Fields: []schema.Field{
		{
			Name:        "hostname",
			Type:        schema.TypeString,
			Description: "Local friendly name.",
			Required:    true,
		},
		{
			Name:        "web_port",
			Type:        schema.TypeInteger,
			Description: "Dashboard port.",
			Default:     8080,
		},
	}}
	if err := s.ValidateDefinition(); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
	srv := server.New("127.0.0.1:0")
	srv.MountConfig(config.New(s, filepath.Join(t.TempDir(), desired.FileName)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func invoke(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func withServer(t *testing.T, args []string) []string {
	t.Helper()
	return append([]string{"--server", testDaemon(t)}, args...)
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return path
}

func TestStatus(t *testing.T) {
	code, stdout, _ := invoke(t, withServer(t, []string{"status"}))
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "status: ok") || !strings.Contains(stdout, "server: ") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestVersion(t *testing.T) {
	code, stdout, _ := invoke(t, withServer(t, []string{"version"}))
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "homesteadctl ") || !strings.Contains(stdout, "homestead-server ") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestConfigGet(t *testing.T) {
	code, stdout, _ := invoke(t, withServer(t, []string{"config", "get"}))
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, `"version"`) || !strings.Contains(stdout, `"settings"`) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestConfigPlanValidJSON(t *testing.T) {
	file := writeTemp(t, "good.json", `{"hostname": "homestead", "web_port": 9090}`)
	code, stdout, _ := invoke(t, withServer(t, []string{"config", "plan", "-f", file}))
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, `"valid": true`) || !strings.Contains(stdout, `"effective"`) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestConfigPlanInvalid(t *testing.T) {
	file := writeTemp(t, "bad.json", `{"web_port": "lots"}`)
	code, _, stderr := invoke(t, withServer(t, []string{"config", "plan", "-f", file}))
	if code != exitError {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "web_port") {
		t.Fatalf("stderr = %q, want field error", stderr)
	}
}

func TestConfigApplyYAMLThenGet(t *testing.T) {
	serverArgs := withServer(t, nil)
	file := writeTemp(t, "apply.yaml", "settings:\n  hostname: yaml-host\n")
	code, _, _ := invoke(t, append(serverArgs, "config", "apply", "-f", file))
	if code != exitOK {
		t.Fatalf("apply exit = %d, want 0", code)
	}
	code, stdout, _ := invoke(t, append(serverArgs, "config", "get"))
	if code != exitOK || !strings.Contains(stdout, "yaml-host") {
		t.Fatalf("get exit = %d, stdout = %q", code, stdout)
	}
}

func TestConfigApplyInvalidPersistsNothing(t *testing.T) {
	serverArgs := withServer(t, nil)
	file := writeTemp(t, "bad.yaml", "web_port: lots\n")
	code, _, _ := invoke(t, append(serverArgs, "config", "apply", "-f", file))
	if code != exitError {
		t.Fatalf("apply exit = %d, want 1", code)
	}
	code, stdout, _ := invoke(t, append(serverArgs, "config", "get"))
	if code != exitOK || !strings.Contains(stdout, `"settings": {}`) {
		t.Fatalf("get exit = %d, stdout = %q", code, stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	serverArgs := withServer(t, nil)
	for _, args := range [][]string{
		{},
		{"bogus"},
		{"config"},
		{"config", "bogus"},
		{"config", "plan"},
	} {
		full := append(append([]string{}, serverArgs...), args...)
		if code, _, _ := invoke(t, full); code != exitUsage {
			t.Fatalf("args %q exit = %d, want 2", args, code)
		}
	}
}

func TestInputFileErrors(t *testing.T) {
	serverArgs := withServer(t, nil)
	missing := filepath.Join(t.TempDir(), "absent.json")
	badExt := writeTemp(t, "settings.txt", `{"a": 1}`)
	malformed := writeTemp(t, "broken.json", `{oops`)
	for _, file := range []string{missing, badExt, malformed} {
		full := append(append([]string{}, serverArgs...), "config", "plan", "-f", file)
		if code, _, stderr := invoke(t, full); code != exitError || !strings.Contains(stderr, "Error:") {
			t.Fatalf("file %q exit = %d, stderr = %q; want 1 with Error", file, code, stderr)
		}
	}
}

func TestUnreachableServer(t *testing.T) {
	code, _, stderr := invoke(t, []string{"--server", "http://127.0.0.1:1", "status"})
	if code != exitError || !strings.Contains(stderr, "Error:") {
		t.Fatalf("exit = %d, stderr = %q; want 1 with Error", code, stderr)
	}
}

func TestServerEnvFallback(t *testing.T) {
	t.Setenv("HOMESTEAD_SERVER", testDaemon(t))
	code, stdout, _ := invoke(t, []string{"status"})
	if code != exitOK || !strings.Contains(stdout, "status: ok") {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
}

func TestLoadSettingsFileForms(t *testing.T) {
	bare := writeTemp(t, "bare.json", `{"hostname": "h"}`)
	got, err := loadSettingsFile(bare)
	if err != nil || got["hostname"] != "h" {
		t.Fatalf("bare = %v, %v", got, err)
	}
	envelope := writeTemp(t, "env.yaml", "settings:\n  hostname: h\n")
	got, err = loadSettingsFile(envelope)
	if err != nil || got["hostname"] != "h" {
		t.Fatalf("envelope = %v, %v", got, err)
	}
	typed := writeTemp(t, "typed.json", `{"web_port": 9090, "debug": true}`)
	got, err = loadSettingsFile(typed)
	if err != nil || got["web_port"] != float64(9090) {
		t.Fatalf("typed = %v, %v", got, err)
	}
}
