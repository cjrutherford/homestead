package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
)

const dataUsage = "usage: homesteadctl data <status|plan|approve|remove-volumes> [flags]"

// runData serves S277 app data migration: where each app's data lives,
// the reviewed migration plan, approval of that plan, and the separate
// removal of old volumes afterwards.
func runData(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, dataUsage)
		return exitUsage
	}
	switch args[0] {
	case "status":
		return runDataStatus(ctx, client, stdout, stderr)
	case "plan":
		return runDataPlan(ctx, client, args[1:], stdout, stderr)
	case "approve":
		return runDataApprove(ctx, client, args[1:], stdout, stderr)
	case "remove-volumes":
		return runDataRemoveVolumes(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown data subcommand %q\n%s\n", args[0], dataUsage)
		return exitUsage
	}
}

func runDataStatus(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	status, err := client.DataMigrationStatus(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if len(status.Apps) == 0 {
		fmt.Fprintln(stdout, "no installed apps")
	}
	for _, app := range status.Apps {
		fmt.Fprintf(stdout, "%s: %s\n", app.Package, app.State)
		if app.Reason != "" {
			fmt.Fprintf(stdout, "  reason: %s\n", app.Reason)
		}
		for _, finding := range app.Findings {
			detail := finding.Kind
			if finding.Volume != "" {
				detail += " " + finding.Volume
			}
			fmt.Fprintf(stdout, "  %s %s: %s (%s)\n", finding.Service, finding.Target, finding.State, detail)
		}
	}
	for _, volume := range status.OldVolumes {
		fmt.Fprintf(stdout, "old volume %s (%s %s, %d bytes): %s\n", volume.Volume, volume.Package, volume.Target, volume.SizeBytes, volume.State)
	}
	return exitOK
}

func runDataPlan(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl data plan <package>")
		return exitUsage
	}
	plan, err := client.DataMigrationPlan(ctx, args[0])
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "migration plan for %s (digest %s)\n", plan.Package, plan.Digest)
	for _, item := range plan.Items {
		fmt.Fprintf(stdout, "  copy %s %s: %s -> %s (%d bytes)\n", item.Service, item.Target, item.Source, item.Dest, item.SizeBytes)
	}
	for _, space := range plan.Space {
		fmt.Fprintf(stdout, "  space %s: needs %d bytes, %d free\n", space.Mountpoint, space.NeededBytes, space.FreeBytes)
	}
	for _, volume := range plan.OldVolumes {
		fmt.Fprintf(stdout, "  old volume kept: %s (%d bytes)\n", volume.Name, volume.SizeBytes)
	}
	fmt.Fprintf(stdout, "  expected downtime: about %d seconds\n", plan.EstimatedDowntimeSeconds)
	if plan.Backup.Possible {
		fmt.Fprintf(stdout, "  pre-migration backup: will run to %s\n", plan.Backup.Destination)
	} else {
		fmt.Fprintf(stdout, "  pre-migration backup: NOT possible (%s); approval needs --acknowledge-no-backup\n", plan.Backup.Reason)
	}
	for _, note := range plan.Notes {
		fmt.Fprintf(stdout, "  note: %s\n", note)
	}
	fmt.Fprintf(stdout, "approve with: homesteadctl data approve %s --digest %s\n", plan.Package, plan.Digest)
	return exitOK
}

func runDataApprove(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var positional []string
	digest := ""
	acknowledge, follow := false, false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--digest" && i+1 < len(args):
			digest = args[i+1]
			i++
		case args[i] == "--acknowledge-no-backup":
			acknowledge = true
		case args[i] == "--follow":
			follow = true
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 1 || digest == "" {
		fmt.Fprintln(stderr, "usage: homesteadctl data approve <package> --digest DIGEST [--acknowledge-no-backup] [--follow]")
		return exitUsage
	}
	id, err := client.DataMigrationApprove(ctx, positional[0], digest, acknowledge)
	if err != nil {
		return fail(stderr, err)
	}
	return reportOperation(ctx, client, id, follow, stdout, stderr)
}

func runDataRemoveVolumes(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var positional []string
	follow := false
	for _, arg := range args {
		switch {
		case arg == "--follow":
			follow = true
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", arg)
			return exitUsage
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) < 2 {
		fmt.Fprintln(stderr, "usage: homesteadctl data remove-volumes <package> <volume>... [--follow]")
		return exitUsage
	}
	id, err := client.DataMigrationRemoveVolumes(ctx, positional[0], positional[1:])
	if err != nil {
		return fail(stderr, err)
	}
	return reportOperation(ctx, client, id, follow, stdout, stderr)
}

// reportOperation prints the operation id and optionally follows it.
func reportOperation(ctx context.Context, client *apiclient.Client, id int64, follow bool, stdout, stderr io.Writer) int {
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
		if last.Error != "" {
			fmt.Fprintln(stderr, last.Error)
		}
		return exitError
	}
	return exitOK
}
