package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"

	"homestead/internal/apiclient"
	"homestead/internal/backup"
)

func runBackup(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl backup <destination|policy|schedules|schedule|unschedule|recipients|identity> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "destination":
		return runBackupDestination(ctx, client, stdout, stderr)
	case "policy":
		return runBackupPolicy(ctx, client, args[1:], stdout, stderr)
	case "schedules":
		return runBackupSchedules(ctx, client, stdout, stderr)
	case "schedule":
		return runBackupSchedule(ctx, client, args[1:], stdout, stderr)
	case "unschedule":
		return runBackupUnschedule(ctx, client, args[1:], stdout, stderr)
	case "recipients":
		return runBackupRecipients(ctx, client, args[1:], stdout, stderr)
	case "identity":
		return runBackupIdentity(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown backup subcommand %q\n", args[0])
		return exitUsage
	}
}

func runBackupDestination(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	info, err := client.BackupDestinationInfo(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, info.Destination)
	if info.Encrypted {
		fmt.Fprintf(stdout, "encrypted: yes (age, format %d)\n", info.FormatVersion)
	} else {
		fmt.Fprintln(stdout, "encrypted: no")
	}
	return exitOK
}

func runBackupSchedules(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	schedules, err := client.BackupSchedules(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	for _, schedule := range schedules {
		if schedule.Stateless {
			fmt.Fprintf(stdout, "%s stateless (no-op: keeps nothing on the appliance, no job runs)\n", schedule.Package)
			continue
		}
		fmt.Fprintf(stdout, "%s every %ds retain %d enabled=%v by %s\n",
			schedule.Package, schedule.IntervalSeconds, schedule.Retain, schedule.Enabled, schedule.Actor)
	}
	return exitOK
}

func runBackupSchedule(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("backup schedule", flag.ContinueOnError)
	set.SetOutput(stderr)
	packageName := set.String("package", "", "package name")
	interval := set.String("interval", "", "hours between runs (minimum 1)")
	retain := set.String("retain", "7", "runs to retain")
	enabled := set.String("enabled", "true", "true or false")
	actor := set.String("actor", "", "acting owner recorded with the change")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *packageName == "" || *interval == "" || *actor == "" {
		fmt.Fprintln(stderr, "backup schedule requires --package, --interval and --actor")
		return exitUsage
	}
	hours, err := strconv.Atoi(*interval)
	if err != nil || hours < 1 {
		fmt.Fprintln(stderr, "invalid --interval (want hours >= 1)")
		return exitUsage
	}
	count, err := strconv.Atoi(*retain)
	if err != nil || count < 0 {
		fmt.Fprintln(stderr, "invalid --retain (want count >= 0)")
		return exitUsage
	}
	on, err := strconv.ParseBool(*enabled)
	if err != nil {
		fmt.Fprintln(stderr, "invalid --enabled (want true or false)")
		return exitUsage
	}
	saved, err := client.BackupSchedule(ctx, backup.Schedule{
		Package: *packageName, IntervalSeconds: int64(hours) * 3600,
		Retain: count, Enabled: on, Actor: *actor,
	})
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s every %ds retain %d enabled=%v\n",
		saved.Package, saved.IntervalSeconds, saved.Retain, saved.Enabled)
	return exitOK
}

func runBackupUnschedule(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("backup unschedule", flag.ContinueOnError)
	set.SetOutput(stderr)
	packageName := set.String("package", "", "package name")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *packageName == "" {
		fmt.Fprintln(stderr, "backup unschedule requires --package")
		return exitUsage
	}
	if err := client.BackupUnschedule(ctx, *packageName); err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "unscheduled %s\n", *packageName)
	return exitOK
}
