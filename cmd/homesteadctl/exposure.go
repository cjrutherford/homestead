package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
)

func runExposure(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl exposure <review|approvals|approve|apply> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "review":
		return runExposureReview(ctx, client, stdout, stderr)
	case "approvals":
		return runExposureApprovals(ctx, client, stdout, stderr)
	case "approve":
		return runExposureApprove(ctx, client, args[1:], stdout, stderr)
	case "apply":
		return runExposureApply(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown exposure subcommand %q\n", args[0])
		return exitUsage
	}
}

func runExposureReview(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	plan, err := client.ExposureReview(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s: %s -> %s (aligned: %v)\n", plan.Host, plan.From, plan.To, plan.Assessment.Aligned)
	for _, finding := range plan.Assessment.Findings {
		fmt.Fprintf(stdout, "  %s: %s\n", finding.Severity, finding.Message)
	}
	fmt.Fprintf(stdout, "dns records: %d, firewall policies: %d\n", len(plan.DNS), len(plan.Firewall))
	return exitOK
}

func runExposureApprovals(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	approvals, err := client.ExposureApprovals(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	for _, approval := range approvals {
		fmt.Fprintf(stdout, "%s by %s\n", approval.Host, approval.Actor)
	}
	return exitOK
}

func runExposureApprove(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var actor string
	var networks []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--actor" && i+1 < len(args):
			actor = args[i+1]
			i++
		case args[i] == "--network" && i+1 < len(args):
			networks = append(networks, args[i+1])
			i++
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			fmt.Fprintln(stderr, "usage: homesteadctl exposure approve --actor NAME [--network CIDR]...")
			return exitUsage
		}
	}
	if actor == "" {
		fmt.Fprintln(stderr, "exposure approve requires --actor")
		return exitUsage
	}
	approval, err := client.ExposureApprove(ctx, actor, networks)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "approved %s by %s\n", approval.Host, approval.Actor)
	return exitOK
}

func runExposureApply(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	follow := false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "--follow":
			follow = true
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "unknown flag %q\n", arg)
				return exitUsage
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl exposure apply <host> [--follow]")
		return exitUsage
	}
	id, err := client.ExposureApply(ctx, positional[0])
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
	switch last.Status {
	case operation.StatusSucceeded:
		return exitOK
	default:
		return exitError
	}
}
