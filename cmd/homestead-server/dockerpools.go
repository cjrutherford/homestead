package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"homestead/internal/dockerpools"
)

// runDockerPools is `homestead-server docker-pools`, run by the installer
// as root (S463). It merges a small-subnet default-address-pools entry into
// Docker's daemon.json so a full catalog fits, and prints "changed" or
// "unchanged" so the installer restarts Docker only when needed. Existing
// keys, and pools the owner set, are never touched.
func runDockerPools(args []string) error {
	fs := flag.NewFlagSet("docker-pools", flag.ContinueOnError)
	path := fs.String("file", dockerpools.DefaultPath, "Docker daemon configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	existing, err := os.ReadFile(*path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	base, err := dockerpools.Choose(dockerpools.HostNetworks())
	if err != nil {
		return err
	}
	out, changed, err := dockerpools.Merge(existing, base)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Println("unchanged")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(*path), 0o755); err != nil {
		return err
	}
	tmp := *path + ".homestead-new"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, *path); err != nil {
		return err
	}
	fmt.Println("changed")
	return nil
}
