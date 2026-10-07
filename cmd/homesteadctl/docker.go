package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
)

// runDocker dispatches the Docker group (S073): health prints engine
// versions plus image, container, and network inventory, read-only.
func runDocker(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl docker <health>")
		return exitUsage
	}
	switch args[0] {
	case "health":
		return runDockerHealth(ctx, client, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown docker subcommand %q\n", args[0])
		return exitUsage
	}
}

func runDockerHealth(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	health, err := client.DockerHealth(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if !health.Reachable {
		fmt.Fprintln(stdout, "docker: unreachable (engine not running or socket unavailable)")
		return exitOK
	}
	running := 0
	for _, container := range health.Containers {
		if container.State == "running" {
			running++
		}
	}
	fmt.Fprintf(stdout, "engine: %s (api %s, driver %s)\n", health.Version, health.APIVersion, health.StorageDriver)
	fmt.Fprintf(stdout, "containers: %d (%d running)\n", len(health.Containers), running)
	for _, container := range health.Containers {
		name := strings.Join(container.Names, ",")
		id := container.ID
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Fprintf(stdout, "  %s %s %s %s\n", id, name, container.Image, container.State)
	}
	var total int64
	for _, image := range health.Images {
		total += image.Size
	}
	fmt.Fprintf(stdout, "images: %d (%.1f GiB)\n", len(health.Images), float64(total)/(1<<30))
	names := make([]string, 0, len(health.Networks))
	for _, network := range health.Networks {
		names = append(names, network.Name)
	}
	fmt.Fprintf(stdout, "networks: %s\n", strings.Join(names, ", "))
	return exitOK
}
