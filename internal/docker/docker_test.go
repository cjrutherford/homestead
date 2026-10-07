package docker

import (
	"errors"
	"testing"
)

func stubDocker(t *testing.T, look func(string) (string, error), run func(string, ...string) (string, error)) {
	t.Helper()
	oldLook, oldRun := lookPath, runCmd
	lookPath, runCmd = look, run
	t.Cleanup(func() {
		lookPath, runCmd = oldLook, oldRun
	})
}

const engineJSON = `{"Client":{"Version":"29.1.2","ApiVersion":"1.52"},
"Server":{"Version":"29.1.2","ApiVersion":"1.52","MinAPIVersion":"1.44"}}`

const composeJSON = `{"version":"v5.0.0"}`

func TestAbsentMeansUnavailable(t *testing.T) {
	stubDocker(t,
		func(string) (string, error) { return "", errors.New("not found") },
		func(string, ...string) (string, error) { t.Fatal("no commands should run"); return "", nil },
	)
	state := Detect()
	if state.EngineAvailable || state.ComposeAvailable || state.LegacyComposePresent {
		t.Fatalf("state = %+v, want all unavailable", state)
	}
}

func TestFullDetection(t *testing.T) {
	stubDocker(t,
		func(name string) (string, error) {
			if name == "docker-compose" {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + name, nil
		},
		func(name string, args ...string) (string, error) {
			joined := name + " " + args[0] + " " + args[1]
			switch joined {
			case "docker version --format":
				return engineJSON, nil
			case "docker compose version":
				return composeJSON, nil
			}
			t.Fatalf("unexpected command %s %v", name, args)
			return "", nil
		},
	)
	state := Detect()
	if !state.EngineAvailable || state.EngineVersion != "29.1.2" || state.APIVersion != "1.52" {
		t.Fatalf("engine = %+v", state)
	}
	if !state.ComposeAvailable || state.ComposeVersion != "v5.0.0" {
		t.Fatalf("compose = %+v", state)
	}
	if state.LegacyComposePresent {
		t.Fatalf("legacy = %+v, want absent", state)
	}
}

func TestDaemonDownKeepsCompose(t *testing.T) {
	stubDocker(t,
		func(name string) (string, error) {
			if name == "docker-compose" {
				return "/usr/bin/docker-compose", nil
			}
			return "/usr/bin/" + name, nil
		},
		func(name string, args ...string) (string, error) {
			joined := name + " " + args[0] + " " + args[1]
			switch joined {
			case "docker version --format":
				return "", errors.New("exit status 1: Cannot connect to the Docker daemon")
			case "docker compose version":
				return composeJSON, nil
			}
			t.Fatalf("unexpected command %s %v", name, args)
			return "", nil
		},
	)
	state := Detect()
	if state.EngineAvailable {
		t.Fatalf("engine available without daemon: %+v", state)
	}
	if !state.ComposeAvailable || !state.LegacyComposePresent {
		t.Fatalf("compose = %+v, want available plus legacy hint", state)
	}
}

func TestGarbageVersionsUnavailable(t *testing.T) {
	stubDocker(t,
		func(string) (string, error) { return "/usr/bin/docker", nil },
		func(name string, args ...string) (string, error) {
			return "not json", nil
		},
	)
	state := Detect()
	if state.EngineAvailable || state.ComposeAvailable {
		t.Fatalf("state = %+v, want unavailable", state)
	}
}

func TestLiveDetection(t *testing.T) {
	state := Detect()
	t.Logf("engine=%v(%s api %s) compose=%v(%s) legacy=%v",
		state.EngineAvailable, state.EngineVersion, state.APIVersion,
		state.ComposeAvailable, state.ComposeVersion, state.LegacyComposePresent)
	if state.EngineAvailable && state.EngineVersion == "" {
		t.Fatal("available without version")
	}
	if state.ComposeAvailable && state.ComposeVersion == "" {
		t.Fatal("compose available without version")
	}
}
