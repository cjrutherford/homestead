package docker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"homestead/internal/operation"
)

// Apt source layout (overridable for tests).
var (
	aptListsDir = "/etc/apt/sources.list.d"
	keyringsDir = "/etc/apt/keyrings"
)

// Distro describes a Debian-family release to install Docker on.
type Distro struct {
	ID       string
	Codename string
	Arch     string
	// GPGURL overrides the key download (tests point at httptest).
	GPGURL string
}

// execRun runs one command, returning combined output. Stubbed in tests.
var execRun = func(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// ReleaseIdentity reads ID and VERSION_CODENAME from an os-release file.
func ReleaseIdentity(path string) (id, codename string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("docker: read os-release: %w", err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	if values["ID"] == "" || values["VERSION_CODENAME"] == "" {
		return "", "", fmt.Errorf("docker: os-release lacks ID or VERSION_CODENAME")
	}
	return values["ID"], values["VERSION_CODENAME"], nil
}

// DebianDistro detects the local Debian release (S071).
func DebianDistro() (Distro, error) {
	return distroFromFile("/etc/os-release", "debian")
}

// UbuntuDistro detects the local Ubuntu release (S072), fully isolated
// from the Debian path: separate ID check, codename, and repository URLs.
func UbuntuDistro() (Distro, error) {
	return distroFromFile("/etc/os-release", "ubuntu")
}

// distroFromFile builds a Distro for the wanted ID from an os-release
// file plus dpkg architecture.
func distroFromFile(path, wantID string) (Distro, error) {
	id, codename, err := ReleaseIdentity(path)
	if err != nil {
		return Distro{}, err
	}
	arch, err := execRun(context.Background(), "dpkg", "--print-architecture")
	if err != nil {
		return Distro{}, err
	}
	return assembleDistro(id, codename, strings.TrimSpace(arch), wantID)
}

// assembleDistro validates release parts and fills the key URL. Pure for
// tests; DebianDistro and UbuntuDistro wire the live sources.
func assembleDistro(id, codename, arch, wantID string) (Distro, error) {
	if id != wantID {
		return Distro{}, fmt.Errorf("docker: not %s (id=%q)", wantID, id)
	}
	if codename == "" {
		return Distro{}, fmt.Errorf("docker: empty codename")
	}
	if arch == "" {
		return Distro{}, fmt.Errorf("docker: empty dpkg architecture")
	}
	return Distro{
		ID:       id,
		Codename: codename,
		Arch:     arch,
		GPGURL:   "https://download.docker.com/linux/" + id + "/gpg",
	}, nil
}

// InstallDefinition plans Docker CE plus the Compose v2 plugin for a
// Debian-family distro: prerequisites, repository, packages, verify. Every
// step short-circuits when already satisfied, so re-running converges
// without reinstalling; the closing verify step proves usability.
func InstallDefinition(d Distro) operation.Definition {
	return operation.Definition{Type: "docker.install", Steps: []operation.StepDef{
		{Name: "ensure-prerequisites", Run: ensurePrerequisites},
		{Name: "configure-repository", Run: func(ctx context.Context) error { return configureRepository(ctx, d) }},
		{Name: "install-packages", Run: installPackages},
		{Name: "verify", Run: verifyInstall},
	}}
}

func aptGet(ctx context.Context, args ...string) (string, error) {
	return execRun(ctx, "apt-get", args...)
}

// ensurePrerequisites installs CA certificates for TLS fetches.
func ensurePrerequisites(ctx context.Context) error {
	if _, err := execRun(ctx, "dpkg", "-s", "ca-certificates"); err == nil {
		return nil
	}
	if _, err := aptGet(ctx, "update"); err != nil {
		return fmt.Errorf("prerequisites update: %w", err)
	}
	if _, err := aptGet(ctx, "install", "-y", "ca-certificates"); err != nil {
		return fmt.Errorf("prerequisites install: %w", err)
	}
	return nil
}

// configuredRepository reports a download.docker.com source plus keyring.
func configuredRepository(d Distro) bool {
	list, err := os.ReadFile(filepath.Join(aptListsDir, "docker.list"))
	if err != nil || !strings.Contains(string(list), "download.docker.com") {
		return false
	}
	if _, err := os.Stat(filepath.Join(keyringsDir, "docker.asc")); err != nil {
		return false
	}
	return true
}

// configureRepository adds Docker's apt source and key (S071 Debian layout,
// shared by S072 Ubuntu via Distro).
func configureRepository(ctx context.Context, d Distro) error {
	if configuredRepository(d) {
		return nil
	}
	if err := os.MkdirAll(keyringsDir, 0o755); err != nil {
		return fmt.Errorf("repository keyrings dir: %w", err)
	}
	keyPath := filepath.Join(keyringsDir, "docker.asc")
	if err := fetchFile(ctx, d.GPGURL, keyPath); err != nil {
		return err
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		return fmt.Errorf("repository key mode: %w", err)
	}
	line := fmt.Sprintf("deb [arch=%s signed-by=%s] https://download.docker.com/linux/%s %s stable\n",
		d.Arch, keyPath, d.ID, d.Codename)
	if err := os.WriteFile(filepath.Join(aptListsDir, "docker.list"), []byte(line), 0o644); err != nil {
		return fmt.Errorf("repository source: %w", err)
	}
	return nil
}

// fetchFile downloads url to path (0600, caller chmods as needed).
func fetchFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: status %d", url, res.StatusCode)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	if _, err := io.Copy(file, res.Body); err != nil {
		file.Close()
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	return nil
}

// satisfied reports usable Engine plus Compose v2.
func satisfied() (bool, State) {
	state := Detect()
	return state.EngineAvailable && state.ComposeAvailable, state
}

// installPackages updates lists and installs CE plus plugins, skipping
// when detection already passes.
func installPackages(ctx context.Context) error {
	if ok, _ := satisfied(); ok {
		return nil
	}
	if _, err := aptGet(ctx, "update"); err != nil {
		return fmt.Errorf("packages update: %w", err)
	}
	if _, err := aptGet(ctx, "install", "-y",
		"docker-ce", "docker-ce-cli", "containerd.io",
		"docker-buildx-plugin", "docker-compose-plugin",
	); err != nil {
		return fmt.Errorf("packages install: %w", err)
	}
	return nil
}

// verifyInstall proves a usable Engine plus Compose v2 after the run.
func verifyInstall(_ context.Context) error {
	ok, state := satisfied()
	if !ok {
		return fmt.Errorf("verify: engine=%v(%s) compose=%v(%s)",
			state.EngineAvailable, state.EngineVersion,
			state.ComposeAvailable, state.ComposeVersion)
	}
	return nil
}
