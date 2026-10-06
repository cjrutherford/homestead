package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"homestead/internal/apiclient"
	"homestead/internal/version"
)

func runStatus(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	health, err := client.Healthz(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "status: %s\nserver: %s\n", health.Status, health.Version)
	return exitOK
}

func runVersion(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	serverVersion, err := client.Version(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "homesteadctl %s\nhomestead-server %s\n", version.Version, serverVersion)
	return exitOK
}

func writeJSON(stdout io.Writer, v any) {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(v)
}
