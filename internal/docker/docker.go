// Package docker observes the Docker platform (Phase 6).
//
// S070 detects Engine and Compose v2 versions through the docker CLI:
// presence plus parseable versions is compatibility (notably Compose v2
// over legacy docker-compose). Runtime health over the API belongs to
// S073, installation to S071/S072, and stack lifecycle to S074/S075.
// Detection never errors and never mutates: absence is data.
package docker

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// State is the S070 detection result.
type State struct {
	// EngineAvailable means the CLI reached a daemon and parsed versions.
	EngineAvailable bool
	// EngineVersion is the server version (e.g. 29.1.2).
	EngineVersion string
	// APIVersion is the server API version, the compatibility signal.
	APIVersion string
	// ComposeAvailable means the v2 plugin (`docker compose`) works.
	ComposeAvailable bool
	// ComposeVersion is the plugin version (e.g. v5.0.0).
	ComposeVersion string
	// LegacyComposePresent flags the Python v1 binary (migration hint).
	LegacyComposePresent bool
}

// runCmd and lookPath are vars so tests can stub process execution.
var (
	runCmd = func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				return string(out), fmt.Errorf("%v: %s", err, strings.TrimSpace(string(exit.Stderr)))
			}
			return string(out), err
		}
		return string(out), nil
	}
	lookPath = exec.LookPath
)

// Detect reports Engine and Compose versions. The CLI must exist and the
// daemon must answer; anything less reports unavailable fields.
func Detect() State {
	var state State
	if _, err := lookPath("docker"); err != nil {
		return state
	}
	if out, err := runCmd("docker", "version", "--format", "json"); err == nil {
		var version struct {
			Server struct {
				Version    string `json:"Version"`
				ApiVersion string `json:"ApiVersion"`
			} `json:"Server"`
		}
		if err := json.Unmarshal([]byte(out), &version); err == nil &&
			version.Server.Version != "" {
			state.EngineAvailable = true
			state.EngineVersion = version.Server.Version
			state.APIVersion = version.Server.ApiVersion
		}
	}
	if out, err := runCmd("docker", "compose", "version", "--format", "json"); err == nil {
		var version struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(out), &version); err == nil && version.Version != "" {
			state.ComposeAvailable = true
			state.ComposeVersion = version.Version
		}
	}
	if _, err := lookPath("docker-compose"); err == nil {
		state.LegacyComposePresent = true
	}
	return state
}
