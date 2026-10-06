package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
)

// runFirewall dispatches the firewall group (S056): status shows detection
// and LAN identification, plan previews the reviewed diff, apply commits
// it after the server-side guard.
func runFirewall(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl firewall <status|plan|apply>")
		return exitUsage
	}
	switch args[0] {
	case "status":
		return runFirewallStatus(ctx, client, stdout, stderr)
	case "plan":
		return runFirewallPlan(ctx, client, stdout, stderr)
	case "apply":
		return runFirewallApply(ctx, client, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown firewall subcommand %q\n", args[0])
		return exitUsage
	}
}

func runFirewallStatus(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	status, err := client.FirewallStatus(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "nftables: available=%v accessible=%v\n", status.NFTAvailable, status.RulesetAccessible)
	fmt.Fprintf(stdout, "managed: %v (%d rules), daemon port %d\n", status.Managed, status.RuleCount, status.DaemonPort)
	fmt.Fprintf(stdout, "mode: %s\n", status.Mode)
	if len(status.ExternalManagers) > 0 {
		fmt.Fprintf(stdout, "external managers: %s\n", strings.Join(status.ExternalManagers, ", "))
	}
	fmt.Fprintf(stdout, "local subnets: %s\n", strings.Join(status.LocalSubnets, ", "))
	return exitOK
}

func runFirewallPlan(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	plan, err := client.FirewallPlan(ctx)
	if err != nil {
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) && statusErr.Status == 503 {
			fmt.Fprintln(stderr, "firewall management unavailable here: "+statusErr.Body)
			return exitError
		}
		return fail(stderr, err)
	}
	if plan.InSync {
		fmt.Fprintln(stdout, "in sync: no changes")
		return exitOK
	}
	for _, change := range plan.Changes {
		if change.Rule == "" {
			fmt.Fprintf(stdout, "%s: %s\n", change.Action, change.Detail)
		} else {
			fmt.Fprintf(stdout, "%s %s: %s\n", change.Action, change.Rule, change.Detail)
		}
	}
	fmt.Fprintln(stdout, "--- script ---")
	fmt.Fprintln(stdout, plan.Script)
	return exitOK
}

func runFirewallApply(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	if err := client.FirewallApply(ctx); err != nil {
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) {
			switch statusErr.Status {
			case 409:
				fmt.Fprintln(stderr, "apply refused: "+statusErr.Body)
				return exitError
			case 503:
				fmt.Fprintln(stderr, "firewall management unavailable here: "+statusErr.Body)
				return exitError
			}
		}
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, "firewall applied")
	return exitOK
}
