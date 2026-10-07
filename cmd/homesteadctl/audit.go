package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"

	"homestead/internal/apiclient"
)

// runAudit dispatches the audit group (S063). Reading is the whole
// surface: the trail is append-only, written by the daemon itself.
func runAudit(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl audit <list> [--limit N]")
		return exitUsage
	}
	switch args[0] {
	case "list":
		return runAuditList(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown audit subcommand %q\n", args[0])
		return exitUsage
	}
}

func runAuditList(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("audit list", flag.ContinueOnError)
	set.SetOutput(stderr)
	limitFlag := set.String("limit", "", "max entries (default 100, max 1000)")
	set.StringVar(limitFlag, "n", "", "max entries (default 100, max 1000)")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	limit := 0
	if *limitFlag != "" {
		parsed, err := strconv.Atoi(*limitFlag)
		if err != nil || parsed <= 0 {
			fmt.Fprintln(stderr, "audit list requires a positive --limit")
			return exitUsage
		}
		limit = parsed
	}
	entries, err := client.AuditList(ctx, limit)
	if err != nil {
		return fail(stderr, err)
	}
	for _, entry := range entries {
		override := ""
		if entry.Override {
			override = fmt.Sprintf(" override=%q", entry.OverrideReason)
		}
		fmt.Fprintf(stdout, "%d %s %s %s %s %s%s\n",
			entry.ID, entry.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			entry.Actor, entry.Action, entry.Outcome, entry.Detail, override)
	}
	return exitOK
}
