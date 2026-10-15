package docker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/craft"
)

// The daemon cannot write ~/.docker (ProtectHome, ProtectSystem=strict), so
// Build must hand the docker CLI a private, existing DOCKER_CONFIG and
// remove it afterwards.
func TestBuildUsesPrivateDockerConfig(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "cfg")
	script := "#!/bin/sh\necho \"$DOCKER_CONFIG\" > " + marker + "\ntest -d \"$DOCKER_CONFIG\" || exit 3\necho \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_CONFIG", "")
	var out bytes.Buffer
	err := BuildClient{}.Build(context.Background(), craft.BuildSpec{Dir: t.TempDir(), Tag: "x:1", Output: &out})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(out.String(), "--network none") {
		t.Fatalf("args: %q", out.String())
	}
	b, _ := os.ReadFile(marker)
	cfg := strings.TrimSpace(string(b))
	if cfg == "" {
		t.Fatal("DOCKER_CONFIG not set")
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatalf("config dir %s not removed: %v", cfg, err)
	}
}
