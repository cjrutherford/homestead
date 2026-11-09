package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
)

// runBackupPolicy is `backup policy [status|plan|apply]` (S297).
func runBackupPolicy(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "status":
		if len(args) > 0 {
			fmt.Fprintln(stderr, "usage: homesteadctl backup policy status")
			return exitUsage
		}
		status, err := client.BackupPolicy(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		printBackupPolicyStatus(stdout, status)
		return exitOK
	case "plan", "apply":
	default:
		fmt.Fprintf(stderr, "unknown backup policy subcommand %q (status, plan, apply)\n", sub)
		return exitUsage
	}
	set := flag.NewFlagSet("backup policy "+sub, flag.ContinueOnError)
	set.SetOutput(stderr)
	at := set.String("time", "", "time of day, HH:MM in appliance local time (default 02:00)")
	retain := set.Int("retain", 0, "runs to keep per app (default 7)")
	off := set.Bool("off", false, "turn nightly backups off")
	digest := set.String("digest", "", "plan digest to apply (apply only)")
	if err := set.Parse(args); err != nil || set.NArg() > 0 {
		return exitUsage
	}
	if *retain < 0 {
		fmt.Fprintln(stderr, "invalid --retain (want a count of 1 or more)")
		return exitUsage
	}
	enabled := !*off
	req := apiclient.BackupPolicyRequest{TimeOfDay: *at, Retain: *retain, Enabled: &enabled}
	if sub == "plan" {
		if *digest != "" {
			fmt.Fprintln(stderr, "--digest belongs to apply")
			return exitUsage
		}
		plan, err := client.BackupPolicyPlan(ctx, req)
		if err != nil {
			return fail(stderr, err)
		}
		printBackupPolicyPlan(stdout, plan, *at, *retain, *off)
		return exitOK
	}
	if *digest == "" {
		fmt.Fprintln(stderr, "backup policy apply requires --digest from backup policy plan (same flags)")
		return exitUsage
	}
	req.Digest = *digest
	status, err := client.BackupPolicyApply(ctx, req)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, "approved")
	printBackupPolicyStatus(stdout, status)
	return exitOK
}

func policyLine(on bool, at string, retain int) string {
	if !on {
		return "off"
	}
	return fmt.Sprintf("on, nightly at %s, keep %d", at, retain)
}

func printBackupPolicyCoverage(stdout io.Writer, covered []string, skipped []apiclient.BackupPolicySkip) {
	if len(covered) == 0 {
		fmt.Fprintln(stdout, "covers: (no stateful apps installed yet; later installs are covered)")
	} else {
		fmt.Fprintf(stdout, "covers: %s\n", strings.Join(covered, ", "))
	}
	for _, s := range skipped {
		fmt.Fprintf(stdout, "  leaves out %s: %s\n", s.Package, s.Reason)
	}
}

func printBackupPolicyStatus(stdout io.Writer, s apiclient.BackupPolicyStatus) {
	if s.Policy != nil {
		fmt.Fprintf(stdout, "policy: %s (approved by %s)\n", policyLine(s.Policy.Enabled, s.Policy.TimeOfDay, s.Policy.Retain), orNone(s.Policy.Actor))
	} else {
		fmt.Fprintf(stdout, "policy: not approved; proposed %s\n", policyLine(s.Proposed.Enabled, s.Proposed.TimeOfDay, s.Proposed.Retain))
	}
	fmt.Fprintf(stdout, "time zone: %s\n", s.TimeZone)
	if !s.BackupStorage {
		fmt.Fprintln(stdout, "backup storage: not assigned (assign the backup class with homesteadctl storage assign first)")
	}
	printBackupPolicyCoverage(stdout, s.Covered, s.Skipped)
}

func printBackupPolicyPlan(stdout io.Writer, p apiclient.BackupPolicyPlan, at string, retain int, off bool) {
	fmt.Fprintf(stdout, "plan: %s (time zone %s)\n", policyLine(p.Policy.Enabled, p.Policy.TimeOfDay, p.Policy.Retain), p.TimeZone)
	for _, c := range p.Changes {
		fmt.Fprintf(stdout, "  %s\n", c)
	}
	printBackupPolicyCoverage(stdout, p.Covered, p.Skipped)
	fmt.Fprintf(stdout, "digest: %s\n", p.Digest)
	flags := ""
	if at != "" {
		flags += " --time " + at
	}
	if retain != 0 {
		flags += fmt.Sprintf(" --retain %d", retain)
	}
	if off {
		flags += " --off"
	}
	fmt.Fprintf(stdout, "approve: homesteadctl backup policy apply%s --digest %s\n", flags, p.Digest)
}
