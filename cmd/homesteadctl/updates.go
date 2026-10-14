package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
)

func runUpdates(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl updates <list|apply|platform|verify|apply-platform|trust> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "list":
		return runUpdatesList(ctx, client, stdout, stderr)
	case "apply":
		return runUpdatesApply(ctx, client, args[1:], stdout, stderr)
	case "platform":
		return runUpdatesPlatform(ctx, client, stdout, stderr)
	case "verify":
		return runUpdatesVerify(ctx, client, args[1:], stdout, stderr)
	case "apply-platform":
		return runUpdatesApplyPlatform(ctx, client, args[1:], stdout, stderr)
	case "trust":
		return runUpdatesTrust(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown updates subcommand %q\n", args[0])
		return exitUsage
	}
}

func runUpdatesList(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	rows, err := client.UpdatesList(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	for _, row := range rows {
		if !row.Installed {
			fmt.Fprintf(stdout, "%s not installed\n", row.Package)
			continue
		}
		if len(row.Plan.Services) == 0 {
			fmt.Fprintf(stdout, "%s already current\n", row.Package)
			continue
		}
		fmt.Fprintf(stdout, "%s updates:\n", row.Package)
		for _, move := range row.Plan.Services {
			fmt.Fprintf(stdout, "  %s: %s -> %s (%s)\n", move.Service, move.Current, move.Target, move.Source)
		}
		for _, warning := range row.Plan.Warnings {
			fmt.Fprintf(stdout, "  warning: %s\n", warning)
		}
		if row.Plan.Backup {
			fmt.Fprintf(stdout, "  backup required: %s\n", row.Plan.BackupWhy)
		}
	}
	return exitOK
}

func runUpdatesApply(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	// Manual scan like the apps group: flags may trail the package.
	backup := false
	var passwordFile string
	follow := false
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--backup":
			backup = true
		case args[i] == "--db-password-file" && i+1 < len(args):
			passwordFile = args[i+1]
			i++
		case args[i] == "--follow":
			follow = true
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl updates apply <package> [--backup] [--db-password-file FILE] [--follow]")
		return exitUsage
	}
	dbPassword := ""
	if passwordFile != "" {
		password, err := readSecretFile(passwordFile)
		if err != nil {
			return fail(stderr, err)
		}
		dbPassword = password
	}
	id, detail, err := client.ApplyUpdate(ctx, positional[0], backup, dbPassword)
	if err != nil {
		return fail(stderr, err)
	}
	if id == 0 {
		if detail == "" {
			detail = "already current"
		}
		fmt.Fprintf(stdout, "%s: %s\n", positional[0], detail)
		return exitOK
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
	switch last.Status {
	case operation.StatusSucceeded:
		return exitOK
	default:
		return exitError
	}
}
