package docker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
	"homestead/internal/store"
)

func stubDockerEnv(t *testing.T, run func(string, ...string) (string, error)) {
	t.Helper()
	oldRun, oldLook := runCmd, lookPath
	oldExec := execRun
	runCmd = func(name string, args ...string) (string, error) {
		return run(name, args...)
	}
	execRun = func(_ context.Context, name string, args ...string) (string, error) {
		return run(name, args...)
	}
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() {
		runCmd, lookPath = oldRun, oldLook
		execRun = oldExec
	})
}

func stubAptDirs(t *testing.T) {
	t.Helper()
	oldLists, oldKeys := aptListsDir, keyringsDir
	aptListsDir = t.TempDir()
	keyringsDir = t.TempDir()
	t.Cleanup(func() {
		aptListsDir, keyringsDir = oldLists, oldKeys
	})
}

func openRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

func testDistro(t *testing.T, gpgURL string) Distro {
	t.Helper()
	d, err := assembleDistro("debian", "bookworm", "amd64", "debian")
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	d.GPGURL = gpgURL
	return d
}

func TestReleaseIdentity(t *testing.T) {
	dir := t.TempDir()
	debian := filepath.Join(dir, "os-release")
	if err := os.WriteFile(debian, []byte("ID=debian\nVERSION_CODENAME=bookworm\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	id, codename, err := ReleaseIdentity(debian)
	if err != nil || id != "debian" || codename != "bookworm" {
		t.Fatalf("got %q %q, %v", id, codename, err)
	}
	ubuntu := filepath.Join(dir, "ubuntu-release")
	if err := os.WriteFile(ubuntu, []byte("ID=ubuntu\nVERSION_CODENAME=noble\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := ReleaseIdentity(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
	broken := filepath.Join(dir, "broken")
	if err := os.WriteFile(broken, []byte("ID=debian\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := ReleaseIdentity(broken); err == nil {
		t.Fatal("missing codename accepted")
	}
}

func TestAssembleDistro(t *testing.T) {
	if _, err := assembleDistro("ubuntu", "noble", "amd64", "debian"); err == nil {
		t.Fatal("ubuntu accepted as Debian")
	}
	if _, err := assembleDistro("debian", "", "amd64", "debian"); err == nil {
		t.Fatal("empty codename accepted")
	}
	d, err := assembleDistro("debian", "bookworm", "amd64", "debian")
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if !strings.Contains(d.GPGURL, "download.docker.com/linux/debian") {
		t.Fatalf("GPGURL = %q", d.GPGURL)
	}
	u, err := assembleDistro("ubuntu", "noble", "amd64", "ubuntu")
	if err != nil {
		t.Fatalf("ubuntu assemble: %v", err)
	}
	if !strings.Contains(u.GPGURL, "download.docker.com/linux/ubuntu") {
		t.Fatalf("GPGURL = %q", u.GPGURL)
	}
	if _, err := assembleDistro("debian", "bookworm", "amd64", "ubuntu"); err == nil {
		t.Fatal("debian accepted as Ubuntu")
	}
}

func TestSatisfiedSkipsInstall(t *testing.T) {
	stubAptDirs(t)
	if err := os.WriteFile(filepath.Join(aptListsDir, "docker.list"),
		[]byte("deb [arch=amd64 signed-by=/k] https://download.docker.com/linux/debian bookworm stable\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keyringsDir, "docker.asc"), []byte("key"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	installed := false
	stubDockerEnv(t, func(name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "docker version "):
			return `{"Server":{"Version":"29.1.2","ApiVersion":"1.52"}}`, nil
		case strings.HasPrefix(joined, "docker compose version"):
			return `{"version":"v5.0.0"}`, nil
		case strings.HasPrefix(joined, "dpkg -s"):
			return "Status: install ok installed", nil
		}
		installed = true
		return "", nil
	})
	runner := openRunner(t)
	op, err := runner.Start(context.Background(), InstallDefinition(testDistro(t, "http://unused/")), "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q", done.Status)
	}
	if installed {
		t.Fatal("installer ran despite satisfied detection")
	}
}

func TestFullInstallFlow(t *testing.T) {
	keyBody := "fake-gpg-key"
	keyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(keyBody))
	}))
	defer keyServer.Close()
	stubAptDirs(t)
	dockerUp := false
	stubDockerEnv(t, func(name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "docker version "):
			if !dockerUp {
				return "", errors.New("exit status 1: Cannot connect")
			}
			return `{"Server":{"Version":"29.1.2","ApiVersion":"1.52"}}`, nil
		case strings.HasPrefix(joined, "docker compose version"):
			if !dockerUp {
				return "", errors.New("exit status 1: Cannot connect")
			}
			return `{"version":"v5.0.0"}`, nil
		case joined == "dpkg -s ca-certificates":
			return "", errors.New("exit status 1: not installed")
		case strings.HasPrefix(joined, "apt-get install -y docker-ce"):
			dockerUp = true
			return "", nil
		case strings.HasPrefix(joined, "apt-get"):
			return "", nil
		case strings.HasPrefix(joined, "dpkg "):
			return "", nil
		}
		return "", errors.New("unexpected: " + joined)
	})
	runner := openRunner(t)
	op, err := runner.Start(context.Background(), InstallDefinition(testDistro(t, keyServer.URL)), "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	key, err := os.ReadFile(filepath.Join(keyringsDir, "docker.asc"))
	if err != nil || string(key) != keyBody {
		t.Fatalf("keyring = %q, %v", key, err)
	}
	list, err := os.ReadFile(filepath.Join(aptListsDir, "docker.list"))
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	want := "deb [arch=amd64 signed-by=" + filepath.Join(keyringsDir, "docker.asc") +
		"] https://download.docker.com/linux/debian bookworm stable\n"
	if string(list) != want {
		t.Fatalf("source = %q, want %q", list, want)
	}
}

func TestInstallFailureAborts(t *testing.T) {
	stubAptDirs(t)
	keyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake-gpg-key"))
	}))
	defer keyServer.Close()
	stubDockerEnv(t, func(name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "docker version "),
			strings.HasPrefix(joined, "docker compose version"):
			return "", errors.New("exit status 1: Cannot connect")
		case joined == "dpkg -s ca-certificates":
			return "Status: install ok installed", nil
		case strings.HasPrefix(joined, "apt-get install -y docker-ce"):
			return "E: boom", errors.New("exit status 100")
		case strings.HasPrefix(joined, "apt-get"):
			return "", nil
		case strings.HasPrefix(joined, "dpkg "):
			return "", nil
		}
		return "", errors.New("unexpected: " + joined)
	})
	runner := openRunner(t)
	op, err := runner.Start(context.Background(), InstallDefinition(testDistro(t, keyServer.URL)), "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
	if done.Steps[2].Status != operation.StatusFailed {
		t.Fatalf("install step = %q, want failed", done.Steps[2].Status)
	}
	if done.Steps[3].Status != operation.StatusPending {
		t.Fatalf("verify step = %q, want untouched pending", done.Steps[3].Status)
	}
}

func TestFetchFile(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("keydata"))
	}))
	defer ok.Close()
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer missing.Close()
	path := filepath.Join(t.TempDir(), "key.asc")
	if err := fetchFile(context.Background(), ok.URL, path); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keydata" {
		t.Fatalf("data = %q, %v", data, err)
	}
	if err := fetchFile(context.Background(), missing.URL, path); err == nil {
		t.Fatal("404 accepted, want error")
	}
}

func TestDistroIsolation(t *testing.T) {
	keyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("key"))
	}))
	defer keyServer.Close()
	lines := map[string]string{}
	for _, tc := range []struct {
		id       string
		codename string
	}{
		{"debian", "bookworm"},
		{"ubuntu", "noble"},
	} {
		stubAptDirs(t)
		d, err := assembleDistro(tc.id, tc.codename, "amd64", tc.id)
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		d.GPGURL = keyServer.URL
		if err := configureRepository(context.Background(), d); err != nil {
			t.Fatalf("configure %s: %v", tc.id, err)
		}
		list, err := os.ReadFile(filepath.Join(aptListsDir, "docker.list"))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		lines[tc.id] = string(list)
	}
	if lines["debian"] == lines["ubuntu"] {
		t.Fatalf("identical repo lines: %q", lines["debian"])
	}
	for id, line := range lines {
		if !strings.Contains(line, "download.docker.com/linux/"+id) {
			t.Fatalf("%s line = %q", id, line)
		}
	}
}

func TestLiveDistroDetection(t *testing.T) {
	_, debErr := DebianDistro()
	_, ubuErr := UbuntuDistro()
	if (debErr == nil) == (ubuErr == nil) {
		t.Fatalf("debian=%v ubuntu=%v, want exactly one to match this host", debErr, ubuErr)
	}
}
