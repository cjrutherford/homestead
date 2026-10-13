package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"homestead/internal/apiclient"
	"homestead/internal/catalog"
)

// runDriftSchedule shows the periodic scan cadence and latest result
// (S261); with --interval or --enabled it saves a change first. Scans
// only detect: resolve findings with drift review, accept, or restore.
func runDriftSchedule(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("drift schedule", flag.ContinueOnError)
	set.SetOutput(stderr)
	interval := set.String("interval", "", "scan interval, for example 6h or 24h (minimum 5m)")
	enabled := set.String("enabled", "", "true or false")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return exitUsage
	}
	var view apiclient.DriftSchedule
	var err error
	if *interval == "" && *enabled == "" {
		view, err = client.DriftSchedule(ctx)
	} else {
		current, cerr := client.DriftSchedule(ctx)
		if cerr != nil {
			return fail(stderr, cerr)
		}
		seconds, on := current.IntervalSeconds, current.Enabled
		if *interval != "" {
			d, perr := time.ParseDuration(*interval)
			if perr != nil || d < time.Second {
				fmt.Fprintln(stderr, "invalid --interval (want a duration such as 6h)")
				return exitUsage
			}
			if d < time.Duration(catalog.MinScanInterval)*time.Second {
				fmt.Fprintf(stderr, "invalid --interval: minimum is %s\n", time.Duration(catalog.MinScanInterval)*time.Second)
				return exitUsage
			}
			seconds = int64(d / time.Second)
		}
		if *enabled != "" {
			parsed, perr := strconv.ParseBool(*enabled)
			if perr != nil {
				fmt.Fprintln(stderr, "invalid --enabled (want true or false)")
				return exitUsage
			}
			on = parsed
		}
		view, err = client.DriftScheduleSave(ctx, seconds, on)
	}
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "drift scan every %ds enabled=%v\n", view.IntervalSeconds, view.Enabled)
	scan := view.LastScan
	if scan == nil {
		fmt.Fprintln(stdout, "last scan: never")
		return exitOK
	}
	fmt.Fprintf(stdout, "last scan: %s, %d package(s) scanned\n", scan.ScannedAt.UTC().Format(time.RFC3339), scan.Scanned)
	for _, stack := range scan.Drifted {
		for _, file := range stack.Files {
			fmt.Fprintf(stdout, "drift: %s %s (%s)\n", stack.Package, file.Path, file.Kind)
		}
	}
	for _, stack := range scan.Drifted {
		for _, f := range stack.Live {
			fmt.Fprintf(stdout, "live drift: %s %s %s: %s [%s]\n", stack.Package, f.Service, f.Kind, f.Detail, f.Resolution)
		}
	}
	if scan.LiveSkipped != "" {
		fmt.Fprintf(stdout, "live comparison skipped: %s\n", scan.LiveSkipped)
	}
	for _, msg := range scan.Errors {
		fmt.Fprintf(stdout, "unreadable: %s\n", msg)
	}
	if len(scan.Drifted) == 0 && len(scan.Errors) == 0 {
		fmt.Fprintln(stdout, "clean")
	}
	return exitOK
}
