package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
)

const appSetupUsage = `usage: homesteadctl apps setup <status|resume|skip> <package> [flags]
  status <package>
  resume <package> --digest D [--token-file FILE] [--follow]
  skip <package>`

// runAppsSetup is `apps setup` (S324, ADR-048): the setup approved with
// an install runs on the appliance; read it, resume a stopped one with a
// fresh approval of the stopped step's plan, or skip it.
func runAppsSetup(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, appSetupUsage)
		return exitUsage
	}
	sub, pkg, args := args[0], args[1], args[2:]
	set := flag.NewFlagSet("apps setup "+sub, flag.ContinueOnError)
	set.SetOutput(stderr)
	digest := set.String("digest", "", "digest of the stopped step's current plan, from apps setup status (resume only)")
	tokenFile := set.String("token-file", "", "file holding an Authentik API token, used for this run only (resume only)")
	follow := set.Bool("follow", false, "follow the setup operation (resume only)")
	if strings.HasPrefix(pkg, "-") || set.Parse(args) != nil || set.NArg() > 0 {
		fmt.Fprintln(stderr, appSetupUsage)
		return exitUsage
	}
	resumeOnly := *digest != "" || *tokenFile != "" || *follow
	switch sub {
	case "status":
		if resumeOnly {
			fmt.Fprintln(stderr, "--digest, --token-file and --follow belong to resume")
			return exitUsage
		}
		setup, err := client.AppSetupStatus(ctx, pkg)
		if err != nil {
			return fail(stderr, err)
		}
		printAppSetup(stdout, setup)
		return exitOK
	case "skip":
		if resumeOnly {
			fmt.Fprintln(stderr, "--digest, --token-file and --follow belong to resume")
			return exitUsage
		}
		setup, err := client.AppSetupSkip(ctx, pkg)
		if err != nil {
			return fail(stderr, err)
		}
		printAppSetup(stdout, setup)
		return exitOK
	case "resume":
	default:
		fmt.Fprintf(stderr, "unknown apps setup subcommand %q (status, resume, skip)\n", sub)
		return exitUsage
	}
	if *digest == "" {
		fmt.Fprintln(stderr, "apps setup resume requires --digest from apps setup status")
		return exitUsage
	}
	req := apiclient.AppSetupResumeRequest{Digest: *digest}
	if *tokenFile != "" {
		token, err := readSecretFile(*tokenFile)
		if err != nil {
			return fail(stderr, err)
		}
		req.Token = token
	}
	id, err := client.AppSetupResume(ctx, pkg, req)
	if err != nil {
		return fail(stderr, err)
	}
	if !*follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

func printAppSetup(w io.Writer, s apiclient.AppSetup) {
	if s.Status == "none" {
		fmt.Fprintf(w, "%s: no setup was approved with its install\n", s.App)
		return
	}
	fmt.Fprintf(w, "setup:  %s\n", s.Status)
	if s.Operation != 0 {
		fmt.Fprintf(w, "operation: %d (after install operation %d)\n", s.Operation, s.Install)
	}
	for _, step := range s.Steps {
		fmt.Fprintf(w, "  %-8s %s", step.Name, step.State)
		if step.Reason != "" {
			fmt.Fprintf(w, ": %s", step.Reason)
		}
		fmt.Fprintln(w)
	}
	if s.Reason != "" {
		fmt.Fprintln(w, s.Reason)
	}
	r := s.Review
	if r == nil {
		return
	}
	if r.Problem != "" {
		fmt.Fprintf(w, "the %s step cannot be planned now: %s\n", r.Step, r.Problem)
		fmt.Fprintf(w, "skip: homesteadctl apps setup skip %s\n", s.App)
		return
	}
	fmt.Fprintf(w, "current plan for %s:\n", r.Step)
	for _, a := range r.Addresses {
		fmt.Fprintf(w, "  adds address: %s\n", a)
	}
	if p := r.Public; p != nil {
		fmt.Fprintf(w, "  publishes: %s\n", p.URL)
		for _, warn := range p.Warnings {
			fmt.Fprintf(w, "  warning: %s\n", warn)
		}
	}
	if p := r.SignIn; p != nil {
		for _, c := range p.Changes {
			fmt.Fprintf(w, "  change: %s\n", c)
		}
		for _, warn := range p.Warnings {
			fmt.Fprintf(w, "  warning: %s\n", warn)
		}
	}
	for _, person := range r.People {
		fmt.Fprintf(w, "  grants: %s\n", person)
	}
	token := ""
	if r.TokenKept != nil && !*r.TokenKept {
		token = " --token-file FILE"
	}
	fmt.Fprintf(w, "resume: homesteadctl apps setup resume %s --digest %s%s\n", s.App, r.Digest, token)
	fmt.Fprintf(w, "skip:   homesteadctl apps setup skip %s\n", s.App)
}
