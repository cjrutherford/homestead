package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
)

const appPublicUsage = `usage: homesteadctl apps public <status|plan|apply> <package> [flags]
  status <package>
  plan <package> [--hostname NAME] [--route ROUTE] [--off]
  apply <package> --digest D [--hostname NAME] [--route ROUTE] [--off] [--follow]`

// runAppsPublic is `apps public` (S290): an app's public name on the tunnel.
func runAppsPublic(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, appPublicUsage)
		return exitUsage
	}
	sub, pkg, args := args[0], args[1], args[2:]
	set := flag.NewFlagSet("apps public "+sub, flag.ContinueOnError)
	set.SetOutput(stderr)
	off := set.Bool("off", false, "remove the public name")
	hostname := set.String("hostname", "", "public name (default <package>.<domain>)")
	route := set.String("route", "", "the app's edge route to serve it (default its first)")
	digest := set.String("digest", "", "plan digest to apply (apply only)")
	follow := set.Bool("follow", false, "follow the operation (apply only)")
	if strings.HasPrefix(pkg, "-") || set.Parse(args) != nil || set.NArg() > 0 {
		fmt.Fprintln(stderr, appPublicUsage)
		return exitUsage
	}
	enabled := !*off
	req := apiclient.AppPublicRequest{Enabled: &enabled, Hostname: *hostname, Route: *route}
	switch sub {
	case "status":
		if *off || *hostname != "" || *route != "" || *digest != "" || *follow {
			fmt.Fprintln(stderr, appPublicUsage)
			return exitUsage
		}
		status, err := client.AppPublic(ctx, pkg)
		if err != nil {
			return fail(stderr, err)
		}
		printAppPublicStatus(stdout, status)
		return exitOK
	case "plan":
		if *digest != "" || *follow {
			fmt.Fprintln(stderr, "--digest and --follow belong to apply")
			return exitUsage
		}
		plan, err := client.AppPublicPlan(ctx, pkg, req)
		if err != nil {
			return fail(stderr, err)
		}
		printAppPublicPlan(stdout, plan, *off)
		return exitOK
	case "apply":
	default:
		fmt.Fprintf(stderr, "unknown apps public subcommand %q (status, plan, apply)\n", sub)
		return exitUsage
	}
	if *digest == "" {
		fmt.Fprintln(stderr, "apps public apply requires --digest from apps public plan (same flags)")
		return exitUsage
	}
	req.Digest = *digest
	id, err := client.AppPublicApply(ctx, pkg, req)
	if err != nil {
		return fail(stderr, err)
	}
	if !*follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

func printAppPublicStatus(w io.Writer, s apiclient.AppPublicStatus) {
	if !s.Available {
		fmt.Fprintf(w, "public name: not available: %s\n", s.Reason)
	} else {
		fmt.Fprintf(w, "tunnel: %s\n", s.Tunnel)
	}
	none := true
	for _, r := range s.Routes {
		for _, n := range r.Public {
			if r.Path != "" {
				fmt.Fprintf(w, "public: %s -> %s", r.URL, r.Route)
			} else {
				fmt.Fprintf(w, "public: https://%s -> %s", n, r.Route)
			}
			for _, c := range s.Coverage {
				if c.Hostname == n {
					fmt.Fprintf(w, " (%s)", apiclient.CoverageLamp(c))
				}
			}
			fmt.Fprintln(w)
			none = false
		}
	}
	if none && s.Available {
		proposed := s.Proposed
		if s.ProposedURL != "" {
			proposed = s.ProposedURL
		}
		fmt.Fprintf(w, "public: none (proposed %s)\n", proposed)
	}
}

func printAppPublicPlan(w io.Writer, p apiclient.AppPublicPlan, off bool) {
	if p.Path != "" {
		fmt.Fprintf(w, "plan: %s %s for %s at %s (route %s, tunnel %s; no DNS change)\n", p.Action, p.URL, p.App, p.Path, p.Route, p.Tunnel)
	} else {
		fmt.Fprintf(w, "plan: %s public name %s for %s (route %s, tunnel %s)\n", p.Action, p.Hostname, p.App, p.Route, p.Tunnel)
	}
	for _, c := range p.DNS {
		fmt.Fprintf(w, "dns %s: %s %s -> %s\n", c.Action, c.Type, c.Name, c.Content)
		for _, old := range c.Replaces {
			fmt.Fprintf(w, "  deletes %s %s -> %s\n", old.Type, old.Name, old.Content)
		}
	}
	fmt.Fprintf(w, "connector before: %s\n", strings.Join(p.Before, ", "))
	fmt.Fprintf(w, "connector after: %s\n", strings.Join(p.After, ", "))
	for _, s := range p.AppSettings {
		fmt.Fprintf(w, "app setting: %s\n", s)
	}
	for _, s := range p.Warnings {
		fmt.Fprintf(w, "warning: %s\n", s)
	}
	if p.SignIn != "" {
		fmt.Fprintf(w, "sign-in: %s\n", p.SignIn)
	}
	fmt.Fprintf(w, "digest: %s\n", p.Digest)
	flags := " --hostname " + p.Hostname
	if p.Path != "" {
		// A path app is published on the dashboard's name (ADR-044).
		flags = ""
	}
	if off {
		flags += " --off"
	} else if p.Path == "" {
		flags += " --route " + p.Route
	}
	fmt.Fprintf(w, "approve: homesteadctl apps public apply %s%s --digest %s\n", p.App, flags, p.Digest)
}

const appMoveUsage = `usage: homesteadctl apps move <status|plan|apply> <package> [flags]
  status <package>
  plan <package>
  apply <package> --digest D [--follow]`

// runAppsMove is `apps move` (S315, ADR-044 §5): move an app installed
// under a name of its own to its path under the appliance's names, by a
// reviewed plan. Nothing moves without the digest.
func runAppsMove(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, appMoveUsage)
		return exitUsage
	}
	sub, pkg, args := args[0], args[1], args[2:]
	set := flag.NewFlagSet("apps move "+sub, flag.ContinueOnError)
	set.SetOutput(stderr)
	digest := set.String("digest", "", "plan digest to apply (apply only)")
	follow := set.Bool("follow", false, "follow the operation (apply only)")
	if strings.HasPrefix(pkg, "-") || set.Parse(args) != nil || set.NArg() > 0 {
		fmt.Fprintln(stderr, appMoveUsage)
		return exitUsage
	}
	if sub != "apply" && (*digest != "" || *follow) {
		fmt.Fprintln(stderr, "--digest and --follow belong to apply")
		return exitUsage
	}
	switch sub {
	case "status":
		s, err := client.AppMove(ctx, pkg)
		if err != nil {
			return fail(stderr, err)
		}
		if !s.Offered {
			fmt.Fprintf(stdout, "move: not offered: %s\n", s.Reason)
			return exitOK
		}
		fmt.Fprintf(stdout, "move: offered from %s to %s\n", s.From, strings.Join(s.To, ", "))
		fmt.Fprintf(stdout, "review: homesteadctl apps move plan %s\n", pkg)
		return exitOK
	case "plan":
		p, err := client.AppMovePlan(ctx, pkg)
		if err != nil {
			return fail(stderr, err)
		}
		printAppMovePlan(stdout, p)
		return exitOK
	case "apply":
	default:
		fmt.Fprintf(stderr, "unknown apps move subcommand %q (status, plan, apply)\n", sub)
		return exitUsage
	}
	if *digest == "" {
		fmt.Fprintln(stderr, "apps move apply requires --digest from apps move plan")
		return exitUsage
	}
	id, err := client.AppMoveApply(ctx, pkg, *digest)
	if err != nil {
		return fail(stderr, err)
	}
	if !*follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

func printAppMovePlan(w io.Writer, p apiclient.AppMovePlan) {
	fmt.Fprintf(w, "plan: move %s from %s to %s\n", p.App, p.From, p.Path)
	for _, to := range p.To {
		fmt.Fprintf(w, "address: %s\n", to)
	}
	strip := "kept (the app serves under it)"
	if p.StripPrefix {
		strip = "taken off before the app sees it"
	}
	fmt.Fprintf(w, "path prefix: %s\n", strip)
	for _, r := range p.Redirects {
		fmt.Fprintf(w, "redirect: %s\n", r)
	}
	for _, s := range p.AppSettings {
		fmt.Fprintf(w, "app setting: %s\n", s)
	}
	for _, s := range p.SignIn {
		fmt.Fprintf(w, "sign-in: %s\n", s)
	}
	for _, s := range p.Warnings {
		fmt.Fprintf(w, "warning: %s\n", s)
	}
	fmt.Fprintf(w, "digest: %s\n", p.Digest)
	fmt.Fprintf(w, "approve: homesteadctl apps move apply %s --digest %s --follow\n", p.App, p.Digest)
}
