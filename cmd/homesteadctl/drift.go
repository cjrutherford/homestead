package main

import (
	"context"
	"fmt"
	"io"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
)

func runDrift(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl drift <review|accept|restore> <package> [--follow]")
		return exitUsage
	}
	switch args[0] {
	case "review":
		return runDriftReview(ctx, client, args[1:], stdout, stderr)
	case "accept":
		return runDriftAccept(ctx, client, args[1:], stdout, stderr)
	case "restore":
		return runDriftRestore(ctx, client, args[1:], stdout, stderr)
	case "schedule":
		return runDriftSchedule(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown drift subcommand %q\n", args[0])
		return exitUsage
	}
}

func driftPackage(args []string, stderr io.Writer) (string, bool, int) {
	follow := false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "--follow":
			follow = true
		default:
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(stderr, "unknown flag %q\n", arg)
				return "", false, exitUsage
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		return "", false, exitUsage
	}
	return positional[0], follow, exitOK
}

func runDriftReview(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	name, _, code := driftPackage(args, stderr)
	if code != exitOK {
		fmt.Fprintln(stderr, "usage: homesteadctl drift review <package>")
		return code
	}
	review, err := client.DriftReview(ctx, name)
	if err != nil {
		return fail(stderr, err)
	}
	if len(review) == 0 {
		fmt.Fprintf(stdout, "%s: no drift\n", name)
		return exitOK
	}
	for _, file := range review {
		fmt.Fprintf(stdout, "%s (%s):\n--- managed ---\n%s\n--- current ---\n%s\n", file.Path, file.Kind, file.Recorded, file.Current)
	}
	return exitOK
}

func runDriftAccept(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	name, _, code := driftPackage(args, stderr)
	if code != exitOK {
		fmt.Fprintln(stderr, "usage: homesteadctl drift accept <package>")
		return code
	}
	resolution, err := client.DriftResolve(ctx, name, "accept")
	if err != nil {
		return fail(stderr, err)
	}
	if len(resolution.Accepted) == 0 {
		fmt.Fprintf(stdout, "%s: nothing to adopt\n", name)
		return exitOK
	}
	for _, path := range resolution.Accepted {
		fmt.Fprintf(stdout, "adopted %s\n", path)
	}
	return exitOK
}

func runDriftRestore(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	name, follow, code := driftPackage(args, stderr)
	if code != exitOK {
		fmt.Fprintln(stderr, "usage: homesteadctl drift restore <package> [--follow]")
		return code
	}
	resolution, err := client.DriftResolve(ctx, name, "restore")
	if err != nil {
		return fail(stderr, err)
	}
	if resolution.ID == 0 {
		detail := resolution.Detail
		if detail == "" {
			detail = "no drift"
		}
		fmt.Fprintf(stdout, "%s: %s\n", name, detail)
		return exitOK
	}
	fmt.Fprintf(stdout, "operation: %d\n", resolution.ID)
	if !follow {
		return exitOK
	}
	var last operation.Operation
	if err := client.StreamOperation(ctx, resolution.ID, func(op operation.Operation) {
		last = op
		fmt.Fprintf(stdout, "%s: %s\n", op.Status, stepSummary(op))
	}); err != nil {
		if ctx.Err() != nil {
			return exitOK
		}
		return fail(stderr, err)
	}
	switch last.Status {
	case operation.StatusSucceeded:
		return exitOK
	default:
		return exitError
	}
}
