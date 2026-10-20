package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
)

func runTunnel(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl tunnel <status|plan|setup|teardown> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "status":
		return runTunnelStatus(ctx, client, stdout, stderr)
	case "plan":
		return runTunnelPlan(ctx, client, args[1:], stdout, stderr)
	case "setup":
		return runTunnelSetup(ctx, client, args[1:], stdout, stderr)
	case "teardown":
		return runTunnelTeardown(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown tunnel subcommand %q\n", args[0])
		return exitUsage
	}
}

func runTunnelStatus(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	status, err := client.TunnelStatus(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "preferred: %s", status.Preferred)
	if status.Gateway != "" {
		fmt.Fprintf(stdout, " (gateway %s on %s)", status.Gateway, status.Interface)
	}
	fmt.Fprintln(stdout)
	if len(status.Protocols) > 0 {
		fmt.Fprintf(stdout, "router protocols: %s\n", strings.Join(status.Protocols, ", "))
	}
	fmt.Fprintf(stdout, "connector: installed=%v active=%v\n",
		status.Connector.Installed, status.Connector.Active)
	for _, tunnel := range status.Tunnels {
		fmt.Fprintf(stdout, "%s %s: %s (%d connections)\n",
			tunnel.ID, tunnel.Name, tunnel.Status, len(tunnel.Connections))
	}
	for _, route := range status.Routes {
		fmt.Fprintf(stdout, "%s -> %s via %s\n", route.Hostname, route.Service, route.TunnelID)
	}
	return exitOK
}

// retiredTunnelFlags were the separate token and account inputs before
// S294 (ADR-037): the tunnel now reuses the Domain step's Cloudflare token
// and finds the account from the hostname's zone.
var retiredTunnelFlags = map[string]bool{
	"--api-token": true, "--api-token-file": true, "--account-id": true, "--account-id-file": true,
}

const retiredTunnelFlagHelp = "the tunnel no longer takes a token or account ID: store the Cloudflare token in the setup Domain step " +
	"(homesteadctl tls issuance credentials cloudflare api_token=-), and the account is found from the hostname's zone"

type tunnelFlags struct {
	name, hostname, service, digest string
	follow                          bool
	positional                      []string
}

func parseTunnelFlags(args []string, allowDigest, allowFollow bool, stderr io.Writer) (tunnelFlags, bool) {
	var f tunnelFlags
	for i := 0; i < len(args); i++ {
		switch {
		case retiredTunnelFlags[args[i]]:
			fmt.Fprintf(stderr, "%s: %s\n", args[i], retiredTunnelFlagHelp)
			return f, false
		case args[i] == "--name" && i+1 < len(args):
			f.name = args[i+1]
			i++
		case args[i] == "--hostname" && i+1 < len(args):
			f.hostname = args[i+1]
			i++
		case args[i] == "--service" && i+1 < len(args):
			f.service = args[i+1]
			i++
		case allowDigest && args[i] == "--digest" && i+1 < len(args):
			f.digest = args[i+1]
			i++
		case allowFollow && args[i] == "--follow":
			f.follow = true
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return f, false
		default:
			f.positional = append(f.positional, args[i])
		}
	}
	return f, true
}

func runTunnelPlan(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	f, ok := parseTunnelFlags(args, false, false, stderr)
	if !ok || f.name == "" || f.hostname == "" || f.service == "" || len(f.positional) != 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl tunnel plan --name NAME --hostname HOST --service URL")
		return exitUsage
	}
	plan, err := client.TunnelPlan(ctx, f.name, f.hostname, f.service)
	if err != nil {
		return fail(stderr, err)
	}
	printTunnelPlan(stdout, plan)
	fmt.Fprintf(stdout, "approve with: homesteadctl tunnel setup --name %s --hostname %s --service %s --digest %s\n",
		plan.Name, plan.Hostname, plan.Service, plan.Digest)
	return exitOK
}

func printTunnelPlan(w io.Writer, plan apiclient.TunnelPlan) {
	fmt.Fprintf(w, "tunnel: %s (zone %s)\n", plan.Name, plan.Zone)
	for _, route := range plan.Ingress {
		fmt.Fprintf(w, "route: %s -> %s\n", route.Hostname, route.Service)
	}
	for _, change := range plan.DNS {
		proxied := ""
		if change.Proxied {
			proxied = ", proxied"
		}
		fmt.Fprintf(w, "dns %s: %s %s -> %s%s\n", change.Action, change.Type, change.Name, change.Content, proxied)
		for _, old := range change.Replaces {
			fmt.Fprintf(w, "  deletes %s %s -> %s\n", old.Type, old.Name, old.Content)
		}
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
	fmt.Fprintf(w, "digest: %s\n", plan.Digest)
}

func runTunnelSetup(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	f, ok := parseTunnelFlags(args, true, true, stderr)
	if !ok || f.name == "" || f.hostname == "" || f.service == "" || len(f.positional) != 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl tunnel setup --name NAME --hostname HOST --service URL --digest DIGEST [--follow]")
		return exitUsage
	}
	if f.digest == "" {
		fmt.Fprintln(stderr, "setup approves a reviewed plan: run homesteadctl tunnel plan with the same flags, then pass its --digest")
		return exitUsage
	}
	id, err := client.TunnelSetup(ctx, f.name, f.hostname, f.service, f.digest)
	if err != nil {
		return fail(stderr, err)
	}
	if !f.follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

func runTunnelTeardown(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	f, ok := parseTunnelFlags(args, false, true, stderr)
	if !ok || len(f.positional) != 1 || f.name != "" || f.hostname != "" || f.service != "" {
		fmt.Fprintln(stderr, "usage: homesteadctl tunnel teardown <tunnel-id> [--follow]")
		return exitUsage
	}
	id, err := client.TunnelTeardown(ctx, f.positional[0])
	if err != nil {
		return fail(stderr, err)
	}
	if !f.follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}
