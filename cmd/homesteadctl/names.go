package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
)

// runNames shows app names and drives the optional `.lan` resolver (S295).
func runNames(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "status" {
		return runNamesStatus(ctx, client, stdout, stderr)
	}
	if args[0] != "resolver" || len(args) < 2 {
		fmt.Fprintln(stderr, "usage: homesteadctl names [status] | names resolver <plan|apply> --on|--off [--digest D]")
		return exitUsage
	}
	var enabled, set bool
	var digest string
	rest := args[2:]
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--on":
			enabled, set = true, true
		case rest[i] == "--off":
			enabled, set = false, true
		case rest[i] == "--digest" && i+1 < len(rest):
			digest = rest[i+1]
			i++
		default:
			fmt.Fprintf(stderr, "unknown argument %q\n", rest[i])
			return exitUsage
		}
	}
	switch {
	case !set:
		fmt.Fprintln(stderr, "say --on or --off")
		return exitUsage
	case args[1] == "plan":
		plan, err := client.ResolverPlan(ctx, enabled)
		if err != nil {
			return fail(stderr, err)
		}
		printResolverPlan(stdout, plan)
		fmt.Fprintf(stdout, "approve with: homesteadctl names resolver apply %s --digest %s\n", onOffFlag(enabled), plan.Digest)
		return exitOK
	case args[1] == "apply" && digest != "":
		status, err := client.ResolverApply(ctx, enabled, digest)
		if err != nil {
			return fail(stderr, err)
		}
		printResolverStatus(stdout, status.Resolver)
		if status.Resolver.Enabled {
			fmt.Fprintln(stdout, "apply the firewall plan to add the port 53 rules: homesteadctl firewall plan, then firewall apply")
		}
		return exitOK
	default:
		fmt.Fprintln(stderr, "usage: homesteadctl names resolver <plan|apply> --on|--off [--digest D]")
		return exitUsage
	}
}

func onOffFlag(on bool) string {
	if on {
		return "--on"
	}
	return "--off"
}

func runNamesStatus(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	status, err := client.Names(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	printAppNames(stdout, status.Apps)
	printResolverStatus(stdout, status.Resolver)
	return exitOK
}

func printAppNames(w io.Writer, apps []apiclient.AppNames) {
	if len(apps) == 0 {
		fmt.Fprintln(w, "no local app names yet")
	}
	for _, app := range apps {
		fmt.Fprintf(w, "%s: %s\n", app.Host, strings.Join(app.Names, ", "))
	}
}

func printResolverStatus(w io.Writer, r apiclient.ResolverStatus) {
	state := "off"
	switch {
	case r.Enabled && r.Running:
		state = "on, answering on " + strings.Join(r.Listening, ", ")
	case r.Enabled:
		state = "on, not answering"
	}
	fmt.Fprintf(w, ".lan resolver: %s\n", state)
	if r.Error != "" {
		fmt.Fprintf(w, "  error: %s\n", r.Error)
	}
	if r.Enabled {
		fmt.Fprintf(w, "  answers: %s\n", strings.Join(r.Names, ", "))
		fmt.Fprintf(w, "  %s\n", r.RouterHint)
	}
}

func printResolverPlan(w io.Writer, plan apiclient.ResolverPlan) {
	fmt.Fprintf(w, "turn the .lan resolver %s\n", strings.TrimPrefix(onOffFlag(plan.Enabled), "--"))
	for _, c := range plan.Changes {
		fmt.Fprintf(w, "- %s\n", c)
	}
	if plan.Enabled {
		fmt.Fprintf(w, "listen: %s\n", strings.Join(plan.Listen, ", "))
		fmt.Fprintf(w, "answers: %s\n", strings.Join(plan.Names, ", "))
	}
	for _, r := range plan.Firewall {
		fmt.Fprintf(w, "firewall %s: %s\n", r.Name, r.Detail)
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
	if plan.Enabled {
		fmt.Fprintln(w, plan.RouterHint)
	}
	fmt.Fprintf(w, "digest: %s\n", plan.Digest)
}

// runAppsNames lists each app's local names (S295).
func runAppsNames(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	status, err := client.Names(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	printAppNames(stdout, status.Apps)
	return exitOK
}
