package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/cert"
)

const tlsIssuanceUsage = `usage: homesteadctl tls issuance <subcommand>
  credentials PROVIDER NAME=@FILE|NAME=-
                                   store DNS provider credentials (values
                                   come from a file or stdin, never argv)
  plan [CERT]                      preview first issuance (contacts no CA)
  approvals                        list recorded approvals
  approve CERT --digest D --agree-terms [--confirm-production]
              [--renewal auto|manual]
                                   approve the reviewed plan digest; renewal
                                   defaults to auto (unattended re-issue)
  apply CERT [--follow]            run the approved issuance
  reregister-account CERT [--agree-terms]
                                   register a new ACME account when the CA
                                   forgot it (owner action; production needs
                                   --agree-terms to the terms it prints)`

// runTLSIssuance drives owner-approved first issuance (S278): plan,
// approve, apply, the same API the dashboard uses.
func runTLSIssuance(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, tlsIssuanceUsage)
		return exitUsage
	}
	switch args[0] {
	case "credentials":
		return runTLSIssuanceCredentials(ctx, client, args[1:], stdin(), stdout, stderr)
	case "plan":
		return runTLSIssuancePlan(ctx, client, args[1:], stdout, stderr)
	case "approvals":
		if len(args) != 1 {
			fmt.Fprintln(stderr, tlsIssuanceUsage)
			return exitUsage
		}
		approvals, err := client.TLSIssuanceApprovals(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		if len(approvals) == 0 {
			fmt.Fprintln(stdout, "no issuance approvals recorded")
		}
		for _, a := range approvals {
			fmt.Fprintf(stdout, "%s: approved by %s digest %s directory %s (%s)\n",
				a.Certificate, a.Actor, a.Digest, a.Plan.Directory, a.Plan.DirectoryClass)
		}
		return exitOK
	case "approve":
		return runTLSIssuanceApprove(ctx, client, args[1:], stdout, stderr)
	case "apply":
		return runTLSIssuanceApply(ctx, client, args[1:], stdout, stderr)
	case "reregister-account":
		return runTLSReregister(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown tls issuance subcommand %q\n", args[0])
		return exitUsage
	}
}

func runTLSIssuancePlan(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, tlsIssuanceUsage)
		return exitUsage
	}
	plans, err := client.TLSIssuancePlans(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	shown := 0
	for _, plan := range plans {
		if len(args) == 1 && plan.Certificate != args[0] {
			continue
		}
		shown++
		printIssuancePlan(stdout, plan)
	}
	if shown == 0 {
		if len(args) == 1 {
			fmt.Fprintf(stderr, "no issuable certificate %q (needs an ACME dns-01 certificate in the saved TLS strategy)\n", args[0])
			return exitError
		}
		fmt.Fprintln(stdout, "no ACME dns-01 certificates in the saved TLS strategy")
	}
	return exitOK
}

func printIssuancePlan(w io.Writer, p cert.IssuancePlan) {
	state := "not approved"
	if p.Approved {
		state = "approved"
	}
	fmt.Fprintf(w, "%s (%s)\n", p.Certificate, state)
	fmt.Fprintf(w, "  names:     %s\n", strings.Join(p.Names, ", "))
	fmt.Fprintf(w, "  challenge: %s via %s, zone %s\n", p.Challenge, p.Provider, p.Zone)
	fmt.Fprintf(w, "  directory: %s (%s)\n", p.Directory, p.DirectoryClass)
	fmt.Fprintf(w, "  account:   %s - %s\n", p.Account.State, p.Account.Detail)
	fmt.Fprintf(w, "  terms:     %s\n", p.Terms)
	fmt.Fprintf(w, "  place:     chain %s, key %s (mode %s)\n", p.Placement.ChainFile, p.Placement.KeyFile, p.Placement.KeyMode)
	if len(p.ServedRoutes) == 0 {
		fmt.Fprintln(w, "  serves:    no edge route uses it yet (automatic-TLS routes covered by these names will)")
	} else {
		fmt.Fprintf(w, "  serves:    %s\n", strings.Join(p.ServedRoutes, ", "))
	}
	if p.Ready {
		fmt.Fprintln(w, "  ready:     yes")
	} else {
		fmt.Fprintf(w, "  ready:     no - %s\n", strings.Join(p.Blockers, "; "))
	}
	printIssuerMoves(w, "  ", p.IssuerMoves)
	fmt.Fprintf(w, "  digest:    %s\n", p.Digest)
}

func runTLSReregister(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var name string
	agree := false
	for _, a := range args {
		switch {
		case a == "--agree-terms":
			agree = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", a)
			return exitUsage
		case name == "":
			name = a
		default:
			fmt.Fprintln(stderr, tlsIssuanceUsage)
			return exitUsage
		}
	}
	if name == "" {
		fmt.Fprintln(stderr, tlsIssuanceUsage)
		return exitUsage
	}
	terms, err := client.TLSAccountTerms(ctx, name)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "CA %s (%s) terms of service: %s\n", terms.Directory, terms.DirectoryClass, terms.CurrentURL)
	if terms.Changed {
		fmt.Fprintf(stdout, "  changed since the account was registered (was %s)\n", terms.RecordedURL)
	}
	if terms.AgreementRequired && !agree {
		fmt.Fprintln(stderr, "a production re-registration needs a fresh agreement: re-run with --agree-terms")
		return exitUsage
	}
	if err := client.TLSReregisterAccount(ctx, name, agree); err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "re-registered the ACME account for %s; unattended renewal may resume\n", name)
	return exitOK
}

func runTLSIssuanceApprove(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var name, digest, renewal string
	var agree, production bool
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--digest" && i+1 < len(args):
			digest = args[i+1]
			i++
		case args[i] == "--renewal" && i+1 < len(args):
			renewal = args[i+1]
			i++
		case args[i] == "--agree-terms":
			agree = true
		case args[i] == "--confirm-production":
			production = true
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		case name == "":
			name = args[i]
		default:
			fmt.Fprintln(stderr, tlsIssuanceUsage)
			return exitUsage
		}
	}
	if name == "" || digest == "" {
		fmt.Fprintln(stderr, tlsIssuanceUsage)
		return exitUsage
	}
	approval, err := client.TLSIssuanceApprove(ctx, name, digest, agree, production, renewal)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "approved: %s digest %s by %s (renewal %s)\n", approval.Certificate, approval.Digest, approval.Actor, approval.RenewalMode)
	return exitOK
}

func runTLSIssuanceApply(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var name string
	follow := false
	for _, arg := range args {
		switch {
		case arg == "--follow":
			follow = true
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", arg)
			return exitUsage
		case name == "":
			name = arg
		default:
			fmt.Fprintln(stderr, tlsIssuanceUsage)
			return exitUsage
		}
	}
	if name == "" {
		fmt.Fprintln(stderr, tlsIssuanceUsage)
		return exitUsage
	}
	id, err := client.TLSIssuanceApply(ctx, name)
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

// stdin is the credential-from-stdin source; tests replace it.
var stdin = func() io.Reader { return os.Stdin }

// maxCredentialBytes caps a credential value read from a file or stdin.
const maxCredentialBytes = 64 << 10

// readCapped reads at most maxCredentialBytes and refuses anything larger
// instead of silently truncating a value.
func readCapped(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxCredentialBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxCredentialBytes {
		return nil, fmt.Errorf("value is larger than %d KiB", maxCredentialBytes>>10)
	}
	return raw, nil
}

// readCredential resolves NAME=@FILE or NAME=- into a value. Values never
// travel on the command line, where they would land in shell history and
// the process list. One trailing newline is trimmed.
func readCredential(spec string, in io.Reader) (name, value string, err error) {
	name, source, ok := strings.Cut(spec, "=")
	if !ok || name == "" || source == "" {
		return "", "", fmt.Errorf("credential %q: want NAME=@FILE or NAME=-", spec)
	}
	var raw []byte
	switch {
	case source == "-":
		raw, err = readCapped(in)
	case strings.HasPrefix(source, "@") && len(source) > 1:
		var f *os.File
		if f, err = os.Open(source[1:]); err == nil {
			raw, err = readCapped(f)
			f.Close()
		}
	default:
		return "", "", fmt.Errorf("credential %q: values come from @FILE or - (stdin), never the command line", name)
	}
	if err != nil {
		return "", "", fmt.Errorf("credential %q: %w", name, err)
	}
	return name, strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r"), nil
}

func runTLSIssuanceCredentials(ctx context.Context, client *apiclient.Client, args []string, in io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, tlsIssuanceUsage)
		return exitUsage
	}
	values := map[string]string{}
	stdinUsed := false
	for _, spec := range args[1:] {
		if strings.HasSuffix(spec, "=-") {
			if stdinUsed {
				fmt.Fprintln(stderr, "only one credential can come from stdin")
				return exitUsage
			}
			stdinUsed = true
		}
		name, value, err := readCredential(spec, in)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		values[name] = value
	}
	stored, warning, err := client.TLSIssuanceCredentials(ctx, args[0], values)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "stored %s credentials: %s\n", args[0], strings.Join(stored, ", "))
	if warning != "" {
		fmt.Fprintf(stderr, "warning: %s\n", warning)
	}
	return exitOK
}

// printIssuerMoves prints the apps whose sign-in moves to Authentik's
// public address with the certificate (ADR-048 §6).
func printIssuerMoves(w io.Writer, indent string, m *cert.MovePlan) {
	if m == nil || len(m.Apps) == 0 {
		return
	}
	fmt.Fprintf(w, "%ssign-in moves: %s\n", indent, m.Note)
	for _, a := range m.Apps {
		fmt.Fprintf(w, "%s  %s: issuer %s -> %s, host entry %s", indent, a.Title, a.From, a.To, a.HostEntry)
		if a.ReplacedHostEntry != "" {
			fmt.Fprintf(w, " (replaces %s)", a.ReplacedHostEntry)
		}
		fmt.Fprintln(w)
		if !m.TokenKept {
			fmt.Fprintf(w, "%s    re-plan: homesteadctl apps sign-in plan %s\n", indent, a.App)
		}
	}
}
