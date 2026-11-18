package debianupdate

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	outputs  map[string][]byte
	errors   map[string]error
	commands [][]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	f.commands = append(f.commands, append([]string{name}, args...))
	return f.outputs[command], f.errors[command]
}

func TestParsePlanAllowsOnlyHomesteadUpgrade(t *testing.T) {
	output := `NOTE: This is only a simulation!
Inst homestead [0.0.2-1] (0.0.3-1 local-deb [amd64])
Conf homestead (0.0.3-1 local-deb [amd64])
`
	plan, err := ParsePlan(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 2 || !strings.HasPrefix(plan.Actions[0], "Inst homestead") {
		t.Fatalf("unexpected actions: %#v", plan.Actions)
	}
}

func TestParsePlanRejectsDependenciesAndRemovals(t *testing.T) {
	for _, output := range []string{
		"Inst homestead (0.0.3-1 local-deb [amd64])\nInst libssl3 (3.0 [amd64])\n",
		"Inst homestead (0.0.3-1 local-deb [amd64])\nRemv other [1.0]\n",
		"Conf homestead (0.0.3-1 local-deb [amd64])\n",
	} {
		if _, err := ParsePlan(output); !errors.Is(err, ErrUnsafePlan) {
			t.Fatalf("ParsePlan(%q) error = %v, want ErrUnsafePlan", output, err)
		}
	}
}

func TestValidatePackageRequiresExactHomesteadVersionAndArchitecture(t *testing.T) {
	installed := Package{Package: "homestead", Version: "0.0.2-1", Architecture: "amd64"}
	for _, candidate := range []Package{
		{Package: "other", Version: "0.0.3-1", Architecture: "amd64"},
		{Package: "homestead", Version: "0.0.4-1", Architecture: "amd64"},
		{Package: "homestead", Version: "0.0.3-1", Architecture: "arm64"},
		{Package: "homestead", Version: "0.0.1-1", Architecture: "amd64"},
	} {
		if err := ValidateUpgrade(installed, candidate, "0.0.3", "amd64"); err == nil {
			t.Fatalf("candidate %#v unexpectedly accepted", candidate)
		}
	}
	if err := ValidateUpgrade(installed, Package{Package: "homestead", Version: "0.0.3-1", Architecture: "amd64"}, "0.0.3", "amd64"); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewDigestBindsArtifactArchitectureAndInstalledVersion(t *testing.T) {
	actions := Plan{Actions: []string{"Inst homestead [0.0.2-1] (0.0.3-1 local-deb [amd64])", "Conf homestead (0.0.3-1 local-deb [amd64])"}}
	first := Digest("aaaaaaaa", "amd64", "0.0.2-1", actions)
	if first == Digest("bbbbbbbb", "amd64", "0.0.2-1", actions) || first == Digest("aaaaaaaa", "arm64", "0.0.2-1", actions) || first == Digest("aaaaaaaa", "amd64", "0.0.1-1", actions) {
		t.Fatal("plan digest did not bind all approval inputs")
	}
}

func TestPreviewUsesFixedGuardedCommandsAndBindsMetadata(t *testing.T) {
	dir := t.TempDir()
	artifact := dir + "/homestead.deb"
	if err := os.WriteFile(artifact, []byte("fixture-package"), 0o600); err != nil {
		t.Fatal(err)
	}
	query := strings.Join([]string{"/usr/bin/dpkg-query", "--show", "--showformat=${db:Status-Status}\\n${Version}\\n${Architecture}\\n", "homestead"}, " ")
	field := strings.Join([]string{"/usr/bin/dpkg-deb", "--field", artifact, "Package", "Version", "Architecture"}, " ")
	arch := "/usr/bin/dpkg --print-architecture"
	apt := strings.Join([]string{"/usr/bin/apt-get", "-s", "--no-download", "--no-remove", "--no-install-recommends", "install", artifact}, " ")
	runner := &fakeRunner{outputs: map[string][]byte{
		query: []byte("installed\n0.0.2-1\namd64\n"),
		field: []byte("Package: homestead\nVersion: 0.0.3-1\nArchitecture: amd64\n"),
		arch:  []byte("amd64\n"),
		apt:   []byte("Inst homestead [0.0.2-1] (0.0.3-1 local-deb [amd64])\nConf homestead (0.0.3-1 local-deb [amd64])\n"),
	}}
	preview, err := (Manager{Runner: runner}).Preview(context.Background(), artifact, "0.0.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Digest) != 64 || preview.PackageVersion != "0.0.3-1" || preview.Installed != "0.0.2-1" || preview.Architecture != "amd64" {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	if len(runner.commands) != 4 || runner.commands[3][0] != "/usr/bin/apt-get" {
		t.Fatalf("unexpected command sequence: %#v", runner.commands)
	}
}

func TestInstallUsesDpkgOnExactAbsoluteArtifactPath(t *testing.T) {
	path := "/var/lib/homestead/staging/package.deb"
	runner := &fakeRunner{outputs: map[string][]byte{}}
	if err := (Manager{Runner: runner}).Install(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/dpkg", "--install", path}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("Install args = %#v, want %#v", runner.commands[0], want)
	}
}

func TestVerifyInstalledBinariesAcceptsServerAndCLIOutputFormats(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"/usr/local/bin/homestead-server --version": []byte("0.0.5\n"),
		"/usr/local/bin/homesteadctl version":       []byte("homesteadctl 0.0.5\nhomestead-server 0.0.5\n"),
	}}
	if err := (Manager{Runner: runner}).VerifyInstalledBinaries(context.Background(), "0.0.5"); err != nil {
		t.Fatal(err)
	}
	runner.outputs["/usr/local/bin/homestead-server --version"] = []byte("0.0.4\n")
	if err := (Manager{Runner: runner}).VerifyInstalledBinaries(context.Background(), "0.0.5"); err == nil {
		t.Fatal("outdated daemon version unexpectedly accepted")
	}
}
