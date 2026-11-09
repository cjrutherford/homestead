package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/appsso"
	"homestead/internal/sso"
)

const appSignInUsage = `usage: homesteadctl apps sign-in <status|plan|apply> <package> [flags]
  status <package>
  access <package> [--token-file FILE]
  plan <package> [--off]
  apply <package> --digest D [--off] [--token-file FILE] [--follow]`

// runAppsSignIn is `apps sign-in` (S298): app sign-in through Authentik.
func runAppsSignIn(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, appSignInUsage)
		return exitUsage
	}
	sub, pkg, args := args[0], args[1], args[2:]
	set := flag.NewFlagSet("apps sign-in "+sub, flag.ContinueOnError)
	set.SetOutput(stderr)
	off := set.Bool("off", false, "turn sign-in with Authentik off")
	digest := set.String("digest", "", "plan digest to apply (apply only)")
	tokenFile := set.String("token-file", "", "file holding an Authentik API token, used for this apply only (apply only)")
	follow := set.Bool("follow", false, "follow the operation (apply only)")
	if strings.HasPrefix(pkg, "-") || set.Parse(args) != nil || set.NArg() > 0 {
		fmt.Fprintln(stderr, appSignInUsage)
		return exitUsage
	}
	applyOnly := *digest != "" || *tokenFile != "" || *follow
	switch sub {
	case "status":
		if *off || applyOnly {
			fmt.Fprintln(stderr, appSignInUsage)
			return exitUsage
		}
		status, err := client.AppSignIn(ctx, pkg)
		if err != nil {
			return fail(stderr, err)
		}
		printAppSignInStatus(stdout, status)
		return exitOK
	case "plan":
		if applyOnly {
			fmt.Fprintln(stderr, "--digest, --token-file and --follow belong to apply")
			return exitUsage
		}
		plan, err := client.AppSignInPlan(ctx, pkg, !*off)
		if err != nil {
			return fail(stderr, err)
		}
		printAppSignInPlan(stdout, plan, *off)
		return exitOK
	case "access":
		if *off || *digest != "" || *follow {
			fmt.Fprintln(stderr, appSignInUsage)
			return exitUsage
		}
		token := ""
		if *tokenFile != "" {
			t, err := readSecretFile(*tokenFile)
			if err != nil {
				return fail(stderr, err)
			}
			token = t
		}
		rep, err := client.AppSignInAccess(ctx, pkg, token)
		if err != nil {
			return fail(stderr, err)
		}
		printAccessReport(stdout, rep)
		return exitOK
	case "apply":
	default:
		fmt.Fprintf(stderr, "unknown apps sign-in subcommand %q (status, plan, access, apply)\n", sub)
		return exitUsage
	}
	if *digest == "" {
		fmt.Fprintln(stderr, "apps sign-in apply requires --digest from apps sign-in plan (same flags)")
		return exitUsage
	}
	enabled := !*off
	req := apiclient.AppSignInRequest{Enabled: &enabled, Digest: *digest}
	if *tokenFile != "" {
		token, err := readSecretFile(*tokenFile)
		if err != nil {
			return fail(stderr, err)
		}
		req.Token = token
	}
	id, err := client.AppSignInApply(ctx, pkg, req)
	if err != nil {
		return fail(stderr, err)
	}
	if !*follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

func printAppSignInStatus(w io.Writer, s appsso.Status) {
	switch {
	case s.Enabled:
		fmt.Fprintf(w, "sign-in with Authentik: on (%s)\n", s.Adapter)
		if s.SignInURL != "" {
			fmt.Fprintf(w, "sign in at: %s\n", s.SignInURL)
		}
		if s.PublicSignInURL != "" {
			fmt.Fprintf(w, "public sign in at: %s\n", s.PublicSignInURL)
		}
		if s.OpenToAll {
			fmt.Fprintf(w, "warning: open to every Authentik user; plan again to close it: homesteadctl apps sign-in plan %s\n", s.Package)
		}
		if s.Replan {
			fmt.Fprintf(w, "re-plan: the app's names changed; review with homesteadctl apps sign-in plan %s\n", s.Package)
		}
	case s.Offered:
		fmt.Fprintln(w, "sign-in with Authentik: off (available)")
	default:
		fmt.Fprintf(w, "sign-in with Authentik: not available: %s\n", s.Reason)
	}
	if s.Plugin != nil {
		fmt.Fprintf(w, "plugin: %s %s (third-party, pinned by checksum)\n", s.Plugin.Name, s.Plugin.Version)
	}
	if s.Offered || s.Enabled {
		if s.TokenKept {
			fmt.Fprintln(w, "API token: the dashboard sign-in kept one; apply needs none")
		} else {
			fmt.Fprintln(w, "API token: apply needs --token-file")
		}
	}
}

func printAppSignInPlan(w io.Writer, p appsso.Plan, off bool) {
	state := "on"
	if !p.Enabled {
		state = "off"
	}
	fmt.Fprintf(w, "plan: sign-in with Authentik %s for %s\n", state, p.Title)
	for _, c := range p.Changes {
		fmt.Fprintf(w, "  %s\n", c)
	}
	if p.Enabled {
		fmt.Fprintf(w, "provider: %s\napplication: %s\nclient id: %s\nissuer: %s\n", p.Provider, p.Application, p.ClientID, p.Issuer)
		for _, u := range p.RedirectURIs {
			fmt.Fprintf(w, "redirect uri: %s\n", u)
		}
	}
	for _, s := range p.AppSettings {
		fmt.Fprintf(w, "app setting: %s\n", s)
	}
	for _, s := range p.Warnings {
		fmt.Fprintf(w, "note: %s\n", s)
	}
	fmt.Fprintf(w, "digest: %s\n", p.Digest)
	flags := ""
	if off {
		flags = " --off"
	}
	fmt.Fprintf(w, "approve: homesteadctl apps sign-in apply %s%s --digest %s [--token-file FILE]\n", p.Package, flags, p.Digest)
}

// printAccessReport prints a read-back (S314). Drift is reported, never reverted.
func printAccessReport(w io.Writer, r sso.AccessReport) {
	for _, g := range r.Groups {
		state := "missing"
		if g.Exists {
			state = fmt.Sprintf("%d member(s), owner in it: %t", g.Members, g.OwnerMember)
		}
		fmt.Fprintf(w, "group %s: %s\n", g.Name, state)
	}
	for _, b := range r.Bindings {
		fmt.Fprintf(w, "binding %s: bound=%t\n", b.Group, b.Bound)
	}
	if r.ClaimMapping != "" {
		fmt.Fprintf(w, "groups claim mapping: %s\n", r.ClaimMapping)
	}
	if r.Open {
		fmt.Fprintln(w, "warning: open to every Authentik user (no group binding)")
	}
	if len(r.Drift) == 0 {
		fmt.Fprintln(w, "no drift from what Homestead created")
	}
	for _, d := range r.Drift {
		fmt.Fprintf(w, "drift: %s\n", d)
	}
}
