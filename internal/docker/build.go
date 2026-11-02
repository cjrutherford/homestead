package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"homestead/internal/craft"
)

// BuildClient is the craft.Builder over the docker CLI (S279). It only
// builds, tags and untags local images; nothing is pushed or pulled
// except the digest-pinned base image docker build itself fetches.
type BuildClient struct{}

var _ craft.Builder = BuildClient{}

// Build runs `docker build` for a prepared context directory, streaming
// combined output to spec.Output (the caller caps it).
func (BuildClient) Build(ctx context.Context, spec craft.BuildSpec) error {
	// --network none: the artifact is already staged and verified, so no
	// build step may reach the network (the base pull is the daemon's).
	file := spec.File
	if file == "" {
		file = "Dockerfile"
	}
	args := []string{"build", "--tag", spec.Tag, "--progress=plain", "--file", file}
	if !spec.Network {
		args = append(args, "--network", "none")
	}
	keys := make([]string, 0, len(spec.Args))
	for k := range spec.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+spec.Args[k])
	}
	args = append(args, ".")
	// The daemon runs with ProtectHome and ProtectSystem=strict, so the
	// docker CLI cannot create ~/.docker; give it a private, throwaway
	// config directory (found in KVM validation).
	cfg, err := os.MkdirTemp("", "homestead-docker-config-")
	if err != nil {
		return fmt.Errorf("docker build config dir: %w", err)
	}
	defer os.RemoveAll(cfg)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+cfg)
	if spec.Output != nil {
		cmd.Stdout, cmd.Stderr = spec.Output, spec.Output
	} else {
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	}
	return cmd.Run()
}

// ImageID returns the local image ID behind ref.
func (BuildClient) ImageID(ctx context.Context, ref string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", ref).Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(string(exit.Stderr), "No such image") {
			return "", craft.ErrImageMissing
		}
		return "", fmt.Errorf("docker image inspect %s: %w", ref, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Tag adds a tag to a local image.
func (BuildClient) Tag(ctx context.Context, src, dst string) error {
	if out, err := exec.CommandContext(ctx, "docker", "tag", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("docker tag: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Remove untags (and, when no tag remains, deletes) a local image.
func (BuildClient) Remove(ctx context.Context, ref string) error {
	out, err := exec.CommandContext(ctx, "docker", "rmi", ref).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "No such image") {
			return craft.ErrImageMissing
		}
		return fmt.Errorf("docker rmi %s: %w: %s", ref, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RootDir reports the Docker data root.
func (BuildClient) RootDir(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.DockerRootDir}}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
