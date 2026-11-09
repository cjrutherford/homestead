package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
)

const readLinksUsage = `usage: homesteadctl apps read-links <status|plan|approve> <app> [flags]
  status <app>
  plan <app> [--off]
  approve <app> --digest D [--off] [--follow]`

// runAppsReadLinks is `apps read-links` (S433, ADR-058): whether an
// Everything app's read and view links answer on the public name.
func runAppsReadLinks(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, readLinksUsage)
		return exitUsage
	}
	sub, app, args := args[0], args[1], args[2:]
	set := flag.NewFlagSet("apps read-links "+sub, flag.ContinueOnError)
	set.SetOutput(stderr)
	off := set.Bool("off", false, "close the public paths")
	digest := set.String("digest", "", "plan digest to approve (approve only)")
	follow := set.Bool("follow", false, "follow the operation (approve only)")
	if strings.HasPrefix(app, "-") || set.Parse(args) != nil || set.NArg() > 0 {
		fmt.Fprintln(stderr, readLinksUsage)
		return exitUsage
	}
	enabled := !*off
	req := apiclient.ReadLinksRequest{Enabled: &enabled}
	switch sub {
	case "status":
		if *off || *digest != "" || *follow {
			fmt.Fprintln(stderr, readLinksUsage)
			return exitUsage
		}
		s, err := client.ReadLinks(ctx, app)
		if err != nil {
			return fail(stderr, err)
		}
		if !s.Available {
			fmt.Fprintf(stdout, "read links: not available: %s\n", s.Reason)
			return exitOK
		}
		if !s.Exposed {
			fmt.Fprintf(stdout, "read links: not on the public name (%d links)\n", s.Links)
			return exitOK
		}
		fmt.Fprintf(stdout, "read links: public at %s (%d links)\n", s.Origin, s.Links)
		for _, p := range s.Patterns {
			fmt.Fprintf(stdout, "public: %s\n", p)
		}
		return exitOK
	case "plan":
		if *digest != "" || *follow {
			fmt.Fprintln(stderr, "--digest and --follow belong to approve")
			return exitUsage
		}
		p, err := client.ReadLinksPlan(ctx, app, req)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "action: %s\norigin: %s\nroute: %s\n", p.Action, p.Origin, p.Route)
		for _, x := range p.Patterns {
			fmt.Fprintf(stdout, "public: %s\n", x)
		}
		for _, x := range p.Refused {
			fmt.Fprintf(stdout, "stays closed: %s\n", x)
		}
		fmt.Fprintf(stdout, "links: %d\nexpiry cap: %d days\n", p.Links, p.ExpiryCapDays)
		for _, w := range p.Warnings {
			fmt.Fprintf(stdout, "warning: %s\n", w)
		}
		fmt.Fprintf(stdout, "digest: %s\n", p.Digest)
		return exitOK
	case "approve":
		if *digest == "" {
			fmt.Fprintln(stderr, "apps read-links approve requires --digest from apps read-links plan (same flags)")
			return exitUsage
		}
		req.Digest = *digest
		id, err := client.ReadLinksApprove(ctx, app, req)
		if err != nil {
			return fail(stderr, err)
		}
		if !*follow {
			fmt.Fprintf(stdout, "operation: %d\n", id)
			return exitOK
		}
		return followOperation(ctx, client, id, stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown apps read-links subcommand %q (status, plan, approve)\n", sub)
	return exitUsage
}
