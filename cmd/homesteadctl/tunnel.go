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
		fmt.Fprintf(stdout, "%s -> %s via %s", route.Hostname, route.Service, route.TunnelID)
		for _, c := range status.Coverage {
			if c.Hostname == route.Hostname {
				fmt.Fprintf(stdout, " (%s)", apiclient.CoverageLamp(c))
			}
		}
		fmt.Fprintln(stdout)
	}
	if len(status.Routes) == 0 && status.Proposed.DashboardHostname != "" {
		if status.Proposed.AuthHostname != "" {
			fmt.Fprintf(stdout, "proposed: dashboard %s, Authentik %s", status.Proposed.DashboardHostname, status.Proposed.AuthHostname)
		} else {
			// ADR-044: Authentik signs in under the dashboard's name.
			fmt.Fprintf(stdout, "proposed: dashboard %s, Authentik https://%s/apps/auth/", status.Proposed.DashboardHostname, status.Proposed.DashboardHostname)
		}
		if !status.Proposed.SignInLinked {
			fmt.Fprint(stdout, " (the dashboard waits until sign-in with Authentik is linked)")
		}
		fmt.Fprintln(stdout)
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

// retiredRouteFlags named one route before S296 (ADR-037 §4): the tunnel
// now publishes the dashboard and Authentik through the edge.
var retiredRouteFlags = map[string]bool{"--hostname": true, "--service": true}

const retiredRouteFlagHelp = "the tunnel no longer takes a hostname and service: it publishes the dashboard and Authentik " +
	"through the edge, named by --dashboard-hostname (default homestead.<domain>); Authentik signs in at /apps/auth/ under it " +
	"(ADR-044), and --auth-hostname names only an Authentik installed under a name of its own"

type tunnelFlags struct {
	name, adopt, dashboard, auth, digest string
	follow                               bool
	agreeTerms, confirmProduction        bool
	positional                           []string
}

func (f tunnelFlags) request() apiclient.TunnelRequest {
	return apiclient.TunnelRequest{Name: f.name, Adopt: f.adopt, DashboardHostname: f.dashboard, AuthHostname: f.auth,
		AgreeTerms: f.agreeTerms, ConfirmProduction: f.confirmProduction}
}

// oneTunnel says exactly one of --name (create) and --adopt was given.
func (f tunnelFlags) oneTunnel() bool {
	return (f.name == "") != (f.adopt == "")
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
		case args[i] == "--adopt" && i+1 < len(args):
			f.adopt = args[i+1]
			i++
		case retiredRouteFlags[args[i]]:
			fmt.Fprintf(stderr, "%s: %s\n", args[i], retiredRouteFlagHelp)
			return f, false
		case args[i] == "--dashboard-hostname" && i+1 < len(args):
			f.dashboard = args[i+1]
			i++
		case args[i] == "--auth-hostname" && i+1 < len(args):
			f.auth = args[i+1]
			i++
		case allowDigest && args[i] == "--digest" && i+1 < len(args):
			f.digest = args[i+1]
			i++
		case allowFollow && args[i] == "--follow":
			f.follow = true
		case allowDigest && args[i] == "--agree-terms":
			f.agreeTerms = true
		case allowDigest && args[i] == "--confirm-production":
			f.confirmProduction = true
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
	if !ok || !f.oneTunnel() || len(f.positional) != 0 {
		fmt.Fprintln(stderr, tunnelPlanUsage)
		return exitUsage
	}
	plan, err := client.TunnelPlan(ctx, f.request())
	if err != nil {
		return fail(stderr, err)
	}
	printTunnelPlan(stdout, plan)
	approve := "homesteadctl tunnel setup --name " + plan.Name
	if f.adopt != "" {
		approve = "homesteadctl tunnel setup --adopt " + f.adopt
	}
	if f.dashboard != "" {
		approve += " --dashboard-hostname " + plan.DashboardHostname
	}
	if f.auth != "" {
		approve += " --auth-hostname " + plan.AuthHostname
	}
	if c := plan.Certificate; c != nil && c.Issue {
		approve += " --agree-terms"
		if c.DirectoryClass == "production" {
			approve += " --confirm-production"
		}
	}
	fmt.Fprintf(stdout, "approve with: %s --digest %s\n", approve, plan.Digest)
	return exitOK
}

const tunnelPlanUsage = "usage: homesteadctl tunnel plan (--name NAME | --adopt TUNNEL-ID) [--dashboard-hostname HOST] [--auth-hostname HOST]"

func printTunnelPlan(w io.Writer, plan apiclient.TunnelPlan) {
	fmt.Fprintf(w, "tunnel: %s (zone %s)\n", plan.Name, plan.Zone)
	if a := plan.Adopt; a != nil {
		fmt.Fprintf(w, "adopt: %s (%s, %d connections)\n", a.ID, a.Status, a.Connections)
		for _, e := range a.Existing {
			fmt.Fprintf(w, "  existing %s -> %s: %s\n", e.Hostname, e.Service, e.Outcome)
		}
	}
	for _, route := range plan.Ingress {
		fmt.Fprintf(w, "publish %s: %s -> %s (edge tunnel entry %s)\n", route.Role, route.Hostname, route.Route, route.Service)
	}
	for _, p := range plan.Paths {
		fmt.Fprintf(w, "publish %s: %s -> %s (under the dashboard's name, no DNS record)\n", p.Role, p.URL, p.Route)
	}
	if len(plan.Paths) > 0 {
		fmt.Fprintln(w, "note: apps under the dashboard's name share one web origin with it (ADR-044)")
	}
	for _, left := range plan.Omitted {
		fmt.Fprintf(w, "left out %s: %s: %s\n", left.Role, left.Hostname, left.Reason)
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
	if plan.SignIn != "" {
		fmt.Fprintf(w, "sign-in: %s\n", plan.SignIn)
	}
	if u := plan.SignInUpdate; u != nil {
		fmt.Fprintf(w, "sign-in adds origin: %s\n", u.Origin)
		fmt.Fprintf(w, "sign-in adds redirect: %s\n", u.RedirectURI)
		fmt.Fprintf(w, "sign-in at: %s\n", u.AuthentikURL)
	}
	if c := plan.Certificate; c != nil {
		printTunnelCertificate(w, *c)
	}
	printIssuerMoves(w, "", plan.IssuerMoves)
	fmt.Fprintf(w, "digest: %s\n", plan.Digest)
}

func runTunnelSetup(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	f, ok := parseTunnelFlags(args, true, true, stderr)
	if !ok || !f.oneTunnel() || len(f.positional) != 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl tunnel setup (--name NAME | --adopt TUNNEL-ID) [--dashboard-hostname HOST] [--auth-hostname HOST] --digest DIGEST [--agree-terms] [--confirm-production] [--follow]")
		return exitUsage
	}
	if f.digest == "" {
		fmt.Fprintln(stderr, "setup approves a reviewed plan: run homesteadctl tunnel plan with the same flags, then pass its --digest")
		return exitUsage
	}
	id, err := client.TunnelSetup(ctx, f.request(), f.digest)
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
	if !ok || len(f.positional) != 1 || f.name != "" || f.adopt != "" || f.dashboard != "" || f.auth != "" {
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

// printTunnelCertificate prints the public dashboard name's certificate
// (ADR-048 §5): placed already, issued by the plan, or why not.
func printTunnelCertificate(w io.Writer, c apiclient.TunnelCertificate) {
	switch {
	case c.Placed != "":
		fmt.Fprintf(w, "certificate: %s is already covered by %s\n", c.Name, c.Placed)
	case c.Issue:
		fmt.Fprintf(w, "certificate: %s as %s from %s (%s)\n", c.Name, c.Certificate, c.Directory, c.DirectoryClass)
		fmt.Fprintf(w, "  challenge: %s via %s, zone %s; renewal %s\n", c.Challenge, c.Provider, c.Zone, c.Renewal)
		if c.AddsToStrategy {
			fmt.Fprintf(w, "  adds %s to TLS\n", c.Certificate)
		}
		if c.TermsURL != "" {
			fmt.Fprintf(w, "  terms: %s (approve with --agree-terms)\n", c.TermsURL)
		}
	default:
		fmt.Fprintf(w, "certificate: %s is not issued: %s\n", c.Name, strings.Join(c.Blockers, "; "))
	}
}
