// Package debianupdate validates and applies Homestead's dpkg-managed release.
package debianupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"homestead/internal/update"
)

const packageName = "homestead"

var (
	ErrNotInstalled = errors.New("debian update: Homestead is not dpkg-managed")
	ErrUnsafePlan   = errors.New("debian update: apt plan includes changes outside the Homestead package")
	ErrMode         = errors.New("debian update: package and source update modes cannot be mixed")
	ErrPlanChanged  = errors.New("debian update: apt plan changed; verify and review the package plan again")
)

type Package struct {
	Package      string
	Version      string
	Architecture string
}

type Plan struct {
	Actions []string `json:"actions"`
}

type Preview struct {
	PackageVersion string   `json:"package_version"`
	Architecture   string   `json:"architecture"`
	Installed      string   `json:"installed_version"`
	Actions        []string `json:"actions"`
	Digest         string   `json:"digest"`
	ArtifactSHA    string   `json:"-"`
}

// Runner is injectable so command construction and rejection paths are tested
// without invoking the host package manager.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LC_ALL=C", "LANG=C", "DEBIAN_FRONTEND=noninteractive"}
	var out limitedBuffer
	out.limit = 32 * 1024
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(out.Bytes())))
	}
	return out.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - b.Len()
	if left > 0 {
		if left > n {
			left = n
		}
		_, _ = b.Buffer.Write(p[:left])
	}
	return n, nil
}

type Manager struct{ Runner Runner }

func (m Manager) runner() Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return ExecRunner{}
}

func (m Manager) Installed(ctx context.Context) (Package, error) {
	out, err := m.runner().Run(ctx, "/usr/bin/dpkg-query", "--show", "--showformat=${db:Status-Status}\\n${Version}\\n${Architecture}\\n", packageName)
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "no packages found") {
			return Package{}, ErrNotInstalled
		}
		return Package{}, err
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(fields) != 3 || fields[0] != "installed" {
		return Package{}, ErrNotInstalled
	}
	return Package{Package: packageName, Version: fields[1], Architecture: fields[2]}, nil
}

func (m Manager) NativeArchitecture(ctx context.Context) (string, error) {
	out, err := m.runner().Run(ctx, "/usr/bin/dpkg", "--print-architecture")
	if err != nil {
		return "", err
	}
	arch := strings.TrimSpace(string(out))
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("debian update: unsupported dpkg architecture %q", arch)
	}
	return arch, nil
}

func (m Manager) Inspect(ctx context.Context, path string) (Package, error) {
	if path == "" || !strings.HasPrefix(path, "/") {
		return Package{}, errors.New("debian update: package path must be absolute")
	}
	out, err := m.runner().Run(ctx, "/usr/bin/dpkg-deb", "--field", path, "Package", "Version", "Architecture")
	if err != nil {
		return Package{}, fmt.Errorf("debian update: cannot read package metadata: %w", err)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || fields[name] != "" {
			return Package{}, errors.New("debian update: malformed package metadata")
		}
		fields[name] = strings.TrimSpace(value)
	}
	if fields["Package"] == "" || fields["Version"] == "" || fields["Architecture"] == "" || len(fields) != 3 {
		return Package{}, errors.New("debian update: malformed package metadata")
	}
	return Package{Package: fields["Package"], Version: fields["Version"], Architecture: fields["Architecture"]}, nil
}

var packageVersionPattern = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)-([0-9]+)$`)

func ValidateUpgrade(installed, candidate Package, releaseVersion, nativeArch string) error {
	if installed.Package != packageName || candidate.Package != packageName {
		return fmt.Errorf("debian update: only package %q is supported", packageName)
	}
	if installed.Architecture != nativeArch || candidate.Architecture != nativeArch {
		return fmt.Errorf("debian update: package architecture must match appliance architecture %s", nativeArch)
	}
	match := packageVersionPattern.FindStringSubmatch(candidate.Version)
	if match == nil || match[1] != releaseVersion {
		return fmt.Errorf("debian update: package version must be %s with a numeric Debian revision", releaseVersion)
	}
	current := packageVersionPattern.FindStringSubmatch(installed.Version)
	if current == nil {
		return fmt.Errorf("debian update: installed package version %q is not a supported release", installed.Version)
	}
	plan, err := update.PlanPlatform(map[string]string{update.ComponentDeb: current[1]}, []update.PlatformRelease{{Component: update.ComponentDeb, Version: match[1]}})
	if err != nil || len(plan.Components) != 1 {
		return fmt.Errorf("debian update: package downgrade or same-version update refused")
	}
	return nil
}

func ReleaseVersion(packageVersion string) (string, error) {
	match := packageVersionPattern.FindStringSubmatch(packageVersion)
	if match == nil {
		return "", fmt.Errorf("debian update: unsupported package version %q", packageVersion)
	}
	return match[1], nil
}

func ParsePlan(output string) (Plan, error) {
	plan := Plan{Actions: []string{}}
	scanner := bufio.NewScanner(strings.NewReader(output))
	installs := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "Remv", "Purg", "Del":
			return Plan{}, ErrUnsafePlan
		case "Inst", "Conf":
			if len(fields) < 2 || fields[1] != packageName {
				return Plan{}, ErrUnsafePlan
			}
			if fields[0] == "Inst" {
				installs++
			}
			plan.Actions = append(plan.Actions, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return Plan{}, err
	}
	if installs != 1 {
		return Plan{}, ErrUnsafePlan
	}
	return plan, nil
}

func Digest(artifactSHA, architecture, installedVersion string, plan Plan) string {
	input := artifactSHA + "\n" + architecture + "\n" + installedVersion + "\n" + strings.Join(plan.Actions, "\n") + "\n"
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

func (m Manager) Preview(ctx context.Context, path string, releaseVersion string) (Preview, error) {
	installed, err := m.Installed(ctx)
	if err != nil {
		return Preview{}, err
	}
	candidate, err := m.Inspect(ctx, path)
	if err != nil {
		return Preview{}, err
	}
	arch, err := m.NativeArchitecture(ctx)
	if err != nil {
		return Preview{}, err
	}
	if err := ValidateUpgrade(installed, candidate, releaseVersion, arch); err != nil {
		return Preview{}, err
	}
	artifactSHA, err := digestFile(path)
	if err != nil {
		return Preview{}, err
	}
	out, err := m.runner().Run(ctx, "/usr/bin/apt-get", "-s", "--no-download", "--no-remove", "--no-install-recommends", "install", path)
	if err != nil {
		return Preview{}, fmt.Errorf("debian update: apt simulation failed: %w", err)
	}
	plan, err := ParsePlan(string(out))
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		PackageVersion: candidate.Version, Architecture: arch, Installed: installed.Version,
		Actions: plan.Actions, Digest: Digest(artifactSHA, arch, installed.Version, plan), ArtifactSHA: artifactSHA,
	}, nil
}

func (m Manager) Install(ctx context.Context, path string) error {
	if path == "" || !strings.HasPrefix(path, "/") {
		return errors.New("debian update: package path must be absolute")
	}
	// APT's local-archive install path is incompatible with --no-download on
	// Debian 13: apt-get turns the absolute input into a basename and dpkg
	// refuses it as a non-absolute pathname. The approved APT simulation has
	// already proven there are no dependency changes or removals. Execute only
	// that exact staged package with dpkg so the transaction cannot resolve,
	// fetch, add, or remove any other package.
	_, err := m.runner().Run(ctx, "/usr/bin/dpkg", "--install", path)
	if err != nil {
		return fmt.Errorf("debian update: dpkg package installation failed: %w", err)
	}
	return nil
}

func (m Manager) VerifyInstalledBinaries(ctx context.Context, wantVersion string) error {
	for _, command := range [][]string{
		{"/usr/local/bin/homestead-server", "--version"},
		{"/usr/local/bin/homesteadctl", "version"},
	} {
		out, err := m.runner().Run(ctx, command[0], command[1:]...)
		if err != nil {
			return fmt.Errorf("debian update: installed binary check failed: %w", err)
		}
		fields := strings.Fields(string(out))
		// homestead-server --version prints only the version string; the CLI
		// version command prints both executable and daemon versions. Checking
		// the final token handles both formats while still requiring a value.
		if len(fields) == 0 || fields[len(fields)-1] != wantVersion {
			return fmt.Errorf("debian update: installed binary %s does not report version %s", command[0], wantVersion)
		}
	}
	return nil
}

func digestFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, (512<<20)+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
