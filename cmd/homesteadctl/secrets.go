package main

import (
	"context"
	"fmt"
	"io"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
)

func runSecrets(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl secrets <status|rekey --confirm [--follow]>")
		return exitUsage
	}
	switch args[0] {
	case "status":
		status, err := client.SecretKeyStatus(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "key version: %d\n", status.Version)
		if status.InProgress {
			fmt.Fprintln(stdout, "re-key in progress")
		}
		return exitOK
	case "rekey":
		return runSecretsRekey(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown secrets subcommand %q\n", args[0])
		return exitUsage
	}
}

func runSecretsRekey(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	confirm, follow := false, false
	for _, arg := range args {
		switch arg {
		case "--confirm":
			confirm = true
		case "--follow":
			follow = true
		default:
			fmt.Fprintf(stderr, "unknown flag %q\n", arg)
			return exitUsage
		}
	}
	if !confirm {
		fmt.Fprintln(stderr, "re-key rewrites every stored secret under a new key.")
		fmt.Fprintln(stderr, "Keep every retained key file (secret.key.v*) in your recovery material.")
		fmt.Fprintln(stderr, "usage: homesteadctl secrets rekey --confirm [--follow]")
		return exitUsage
	}
	id, err := client.SecretRekey(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "operation: %d\n", id)
	if !follow {
		return exitOK
	}
	var last operation.Operation
	if err := client.StreamOperation(ctx, id, func(op operation.Operation) {
		last = op
		fmt.Fprintf(stdout, "%s: %s\n", op.Status, stepSummary(op))
	}); err != nil {
		if ctx.Err() != nil {
			return exitOK
		}
		return fail(stderr, err)
	}
	if last.Status != operation.StatusSucceeded {
		return exitError
	}
	return exitOK
}
