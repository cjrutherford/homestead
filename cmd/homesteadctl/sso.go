package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
	"homestead/internal/sso"
)

const ssoUsage = `usage: homesteadctl sso <status|plan|approve|apply|link|unlink|disable> ...
  status
  plan [--authentik-url URL] [--origin URL]... [--signing-key NAME] [--token-file FILE]
  approve --digest D --token-file FILE [--keep-token]
  apply [--follow]
  link [--origin URL] [--password-file FILE]
  unlink [--password-file FILE]
  disable

Sign-in with Authentik is for the dashboard. The CLI always signs in with
the local owner account (homesteadctl login); that is also the way back in
if Authentik is lost.`

// runSSO drives dashboard sign-in with Authentik (S286, ADR-033): the same
// plan, approve and apply flow as the dashboard, link and unlink with a
// fresh password, and disable.
func runSSO(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, ssoUsage)
		return exitUsage
	}
	switch args[0] {
	case "status":
		st, err := client.SSOStatus(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		printSSO(stdout, st)
		return exitOK
	case "plan":
		return runSSOPlan(ctx, client, args[1:], stdout, stderr)
	case "approve":
		return runSSOApprove(ctx, client, args[1:], stdout, stderr)
	case "apply":
		return runSSOApply(ctx, client, args[1:], stdout, stderr)
	case "link":
		return runSSOLink(ctx, client, args[1:], stdout, stderr)
	case "unlink":
		return runSSOUnlink(ctx, client, args[1:], stdout, stderr)
	case "disable":
		st, err := client.SSODisable(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, "Sign-in with Authentik is off. The local owner account is unchanged.")
		fmt.Fprintln(stdout, "The Homestead provider and application are still in Authentik; delete them there if you no longer want them.")
		printSSO(stdout, st)
		return exitOK
	}
	fmt.Fprintf(stderr, "unknown sso subcommand %q\n%s\n", args[0], ssoUsage)
	return exitUsage
}

func printSSO(w io.Writer, st apiclient.SSOState) {
	switch {
	case !st.Configured:
		fmt.Fprintln(w, "Sign-in with Authentik: not set up")
		if !st.Installed {
			fmt.Fprintln(w, "  Authentik is not installed (install it from the catalog first)")
		}
		if st.Plan != nil {
			state := "planned, not approved"
			if st.Plan.Approved {
				state = "approved, not applied"
			}
			fmt.Fprintf(w, "  plan %s: %s (expires %s)\n", short(st.Plan.Digest), state, st.Plan.ExpiresAt.Format(time.RFC3339))
		}
		return
	case st.Offered:
		fmt.Fprintln(w, "Sign-in with Authentik: on (the login page offers it)")
	case st.Dormant:
		fmt.Fprintln(w, "Sign-in with Authentik: dormant (Authentik is not installed)")
	case !st.Linked:
		fmt.Fprintln(w, "Sign-in with Authentik: configured, not linked (run `homesteadctl sso link`)")
	}
	fmt.Fprintf(w, "  issuer:    %s\n", st.Spec.Issuer)
	fmt.Fprintf(w, "  addresses: %s\n", strings.Join(st.Spec.Origins, ", "))
	if st.Linked {
		fmt.Fprintf(w, "  linked to: %s\n", st.Subject)
	}
	token := "forgotten after apply"
	if st.TokenKept {
		token = "kept (sealed)"
	}
	fmt.Fprintf(w, "  API token: %s; client secret sealed: %t\n", token, st.SecretSealed)
}

func short(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func runSSOPlan(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("sso plan", flag.ContinueOnError)
	set.SetOutput(stderr)
	url := set.String("authentik-url", "", "address of your Authentik (default: its route in the edge table)")
	var origins listFlag
	set.Var(&origins, "origin", "address you open the dashboard at; repeat for several (default: derived from this appliance)")
	key := set.String("signing-key", "", "Authentik signing key name")
	tokenFile := set.String("token-file", "", "file holding an Authentik API token, used once to read what exists")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	req := sso.PlanRequest{AuthentikURL: *url, Origins: origins, SigningKey: *key}
	if *tokenFile != "" {
		t, err := readSecretFile(*tokenFile)
		if err != nil {
			return fail(stderr, err)
		}
		req.APIToken = t
	}
	plan, err := client.SSOPlan(ctx, req)
	if err != nil {
		return fail(stderr, err)
	}
	printSSOPlan(stdout, plan)
	return exitOK
}

func printSSOPlan(w io.Writer, p sso.Plan) {
	fmt.Fprintf(w, "plan %s (expires %s)\n", p.Digest, p.ExpiresAt.Format(time.RFC3339))
	fmt.Fprintf(w, "Authentik: %s (catalog pins %s)\n", p.Spec.AuthentikURL, p.Spec.AuthentikVersion)
	fmt.Fprintf(w, "issuer:    %s\n", p.Spec.Issuer)
	fmt.Fprintln(w, "will configure in Authentik:")
	for _, o := range p.Objects {
		fmt.Fprintf(w, "  %-24s %-22s %s", o.Kind, o.Name, o.Action)
		if len(o.Changes) > 0 {
			fmt.Fprintf(w, " (%s)", strings.Join(o.Changes, ", "))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "  flows: %s, %s; signing key: %s; scopes: %s\n", p.Spec.AuthorizationFlow, p.Spec.InvalidationFlow, p.Spec.SigningKey, strings.Join(p.Spec.Scopes, " "))
	fmt.Fprintln(w, "redirect URIs (exact):")
	for _, u := range p.Spec.RedirectURIs {
		fmt.Fprintf(w, "  %s\n", u)
	}
	fmt.Fprintln(w, "left untouched:")
	for _, u := range p.Untouched {
		fmt.Fprintf(w, "  %s\n", u)
	}
	for _, line := range p.Warnings {
		fmt.Fprintln(w, "warning:", line)
	}
	if !p.Inspected {
		fmt.Fprintln(w, "note: Authentik was not read; give --token-file to see what already exists.")
	}
	fmt.Fprintf(w, "next: homesteadctl sso approve --digest %s --token-file FILE\n", p.Digest)
}

func runSSOApprove(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("sso approve", flag.ContinueOnError)
	set.SetOutput(stderr)
	digest := set.String("digest", "", "digest of the plan you reviewed")
	tokenFile := set.String("token-file", "", "file holding the Authentik API token (optional when one is already sealed)")
	keep := set.Bool("keep-token", false, "keep the sealed token after apply (default: forget it)")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *digest == "" {
		fmt.Fprintln(stderr, "approve needs --digest")
		return exitUsage
	}
	// The token is optional: the server accepts an empty one when a token
	// is already sealed.
	token := ""
	if *tokenFile != "" {
		var err error
		token, err = readSecretFile(*tokenFile)
		if err != nil {
			return fail(stderr, err)
		}
	}
	st, err := client.SSOApprove(ctx, sso.ApproveRequest{Digest: *digest, APIToken: token, KeepToken: *keep})
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, "plan approved; run `homesteadctl sso apply`")
	printSSO(stdout, st)
	return exitOK
}

func runSSOApply(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("sso apply", flag.ContinueOnError)
	set.SetOutput(stderr)
	follow := set.Bool("follow", false, "follow the operation to the end")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	id, err := client.SSOApply(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "operation: %d\n", id)
	if !*follow {
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
	if last.Status != operation.StatusSucceeded {
		fmt.Fprintln(stderr, "Error:", last.Error)
		return exitError
	}
	return exitOK
}

// ssoPassword reads the owner's password for fresh re-authentication: from
// a file, else from the terminal without echo, else from one line of stdin.
func ssoPassword(file string, stderr io.Writer) (string, error) {
	if file != "" {
		return readSecretFile(file)
	}
	fmt.Fprint(stderr, "Local owner password: ")
	restore := func() {}
	if tty, err := os.Open("/dev/tty"); err == nil {
		defer tty.Close()
		off := exec.Command("stty", "-echo")
		off.Stdin = tty
		if off.Run() == nil {
			restore = func() {
				on := exec.Command("stty", "echo")
				on.Stdin = tty
				_ = on.Run()
				fmt.Fprintln(stderr)
			}
		}
	}
	defer restore()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no password given")
	}
	p := strings.TrimRight(line, "\r\n")
	if p == "" {
		return "", fmt.Errorf("no password given")
	}
	return p, nil
}

func runSSOLink(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("sso link", flag.ContinueOnError)
	set.SetOutput(stderr)
	origin := set.String("origin", "", "which sign-in address the browser returns to (default: this machine)")
	file := set.String("password-file", "", "file holding the local owner password (default: prompt)")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	pw, err := ssoPassword(*file, stderr)
	if err != nil {
		return fail(stderr, err)
	}
	intent, err := client.SSOLink(ctx, pw, *origin)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "open this address in your browser within %s (one use) and sign in to Authentik:\n%s\n",
		time.Until(intent.ExpiresAt).Round(time.Second), intent.URL)
	return exitOK
}

func runSSOUnlink(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("sso unlink", flag.ContinueOnError)
	set.SetOutput(stderr)
	file := set.String("password-file", "", "file holding the local owner password (default: prompt)")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	pw, err := ssoPassword(*file, stderr)
	if err != nil {
		return fail(stderr, err)
	}
	st, err := client.SSOUnlink(ctx, pw)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, "unlinked; sign-in with Authentik no longer works until you link again")
	printSSO(stdout, st)
	return exitOK
}
