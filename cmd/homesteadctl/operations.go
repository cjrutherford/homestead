package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
)

// runOperations dispatches operation inspection (S062): get prints one
// record, follow streams state events until terminal. Rich progress
// display belongs to the dashboard (S064); the CLI reports outcomes.
func runOperations(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl operations <get|follow> ID")
		return exitUsage
	}
	switch args[0] {
	case "get":
		return runOperationsGet(ctx, client, args[1:], stdout, stderr)
	case "follow":
		return runOperationsFollow(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown operations subcommand %q\n", args[0])
		return exitUsage
	}
}

func operationID(args []string) (int64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("usage: operations <get|follow> ID")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid operation id %q", args[0])
	}
	return id, nil
}

func runOperationsGet(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	id, err := operationID(args)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitUsage
	}
	op, err := client.GetOperation(ctx, id)
	if err != nil {
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) && statusErr.Status == 404 {
			fmt.Fprintln(stderr, "operation not found")
			return exitError
		}
		return fail(stderr, err)
	}
	writeJSON(stdout, op)
	return exitOK
}

func runOperationsFollow(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	id, err := operationID(args)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitUsage
	}
	var last operation.Operation
	if err := client.StreamOperation(ctx, id, func(op operation.Operation) {
		last = op
		fmt.Fprintf(stdout, "%s: %s\n", op.Status, stepSummary(op))
	}); err != nil {
		// Interrupted by the user (SIGINT): the stream did its job.
		if ctx.Err() != nil {
			return exitOK
		}
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) && statusErr.Status == 404 {
			fmt.Fprintln(stderr, "operation not found")
			return exitError
		}
		return fail(stderr, err)
	}
	switch last.Status {
	case operation.StatusSucceeded:
		return exitOK
	case operation.StatusFailed, operation.StatusCancelled:
		return exitError
	default:
		return exitOK
	}
}

func stepSummary(op operation.Operation) string {
	parts := make([]string, 0, len(op.Steps))
	for _, step := range op.Steps {
		parts = append(parts, step.Name+"="+step.Status)
	}
	if len(parts) == 0 {
		return op.Type
	}
	return op.Type + " " + strings.Join(parts, " ")
}
