package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"homestead/internal/apiclient"
	"homestead/internal/setup"
)

const setupUsage = `usage: homesteadctl setup <status|step|skip|unskip> ...
  status
  step <account|sign-in|domain|apps> [--acknowledge] [--provider NAME] [--access home|tunnel|router]
  skip <step>
  unskip <step>

The post-claim setup walkthrough (ADR-034). It only records your choices and
reads what is already true; changes go through their own commands:
  account   homesteadctl backup identity export / ack
  sign-in   homesteadctl sso plan / approve / apply / link
  domain    homesteadctl tls, tunnel and exposure commands
  apps      homesteadctl apps install
Nothing here is gated: the CLI works whatever the walkthrough says.`

func runSetup(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, setupUsage)
		return exitUsage
	}
	switch args[0] {
	case "status":
		st, err := client.SetupStatus(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		printSetup(stdout, st)
		return exitOK
	case "skip", "unskip":
		if len(args) != 2 {
			fmt.Fprintln(stderr, setupUsage)
			return exitUsage
		}
		state := setup.Skipped
		if args[0] == "unskip" {
			state = setup.Pending
		}
		st, err := client.SetupUpdate(ctx, args[1], &state, nil, nil)
		if err != nil {
			return fail(stderr, err)
		}
		printSetup(stdout, st)
		return exitOK
	case "step":
		return runSetupStep(ctx, client, args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown setup subcommand %q\n%s\n", args[0], setupUsage)
	return exitUsage
}

func runSetupStep(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, setupUsage)
		return exitUsage
	}
	name := args[0]
	fs := flag.NewFlagSet("setup step", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ack := fs.Bool("acknowledge", false, "confirm the domain or apps step as finished (changes nothing else)")
	provider := fs.String("provider", "", "DNS provider choice for the domain step")
	access := fs.String("access", "", "access choice for the domain step: home, tunnel or router")
	if err := fs.Parse(args[1:]); err != nil {
		return exitUsage
	}
	var state, prov, acc *string
	if *ack {
		s := setup.Done
		state = &s
	}
	if *provider != "" {
		prov = provider
	}
	if *access != "" {
		acc = access
	}
	var st setup.Status
	var err error
	if state == nil && prov == nil && acc == nil {
		st, err = client.SetupStatus(ctx)
	} else {
		st, err = client.SetupUpdate(ctx, name, state, prov, acc)
	}
	if err != nil {
		return fail(stderr, err)
	}
	for _, s := range st.Steps {
		if s.Name == name {
			printSetupStep(stdout, s)
			if name == setup.StepDomain {
				fmt.Fprintf(stdout, "  provider: %s\n  access: %s\n", orNone(st.Choices.Provider), orNone(st.Choices.Access))
			}
			return exitOK
		}
	}
	fmt.Fprintf(stderr, "unknown setup step %q (account, sign-in, domain, apps)\n", name)
	return exitUsage
}

func orNone(s string) string {
	if s == "" {
		return "(not chosen)"
	}
	return s
}

func printSetupStep(w io.Writer, s setup.StepStatus) {
	fmt.Fprintf(w, "%-8s %-8s %s\n", s.Name, s.State, s.Title)
	fmt.Fprintf(w, "  %s\n", s.Detail)
}

func printSetup(w io.Writer, st setup.Status) {
	switch {
	case st.Legacy:
		fmt.Fprintln(w, "Setup: optional (this appliance was claimed before the walkthrough; the dashboard does not hold you in it)")
	case st.Complete:
		fmt.Fprintln(w, "Setup: complete")
	default:
		fmt.Fprintf(w, "Setup: in progress (next: %s). The dashboard holds the owner in the walkthrough until it is done; the CLI and API are not gated.\n", st.Current)
	}
	for _, s := range st.Steps {
		printSetupStep(w, s)
	}
}
