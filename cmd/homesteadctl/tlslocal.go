package main

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"homestead/internal/apiclient"
	"homestead/internal/cert"
)

const tlsLocalUsage = `usage: homesteadctl tls root show
       homesteadctl tls root export [--format pem|der] [--out FILE]
       homesteadctl tls local <status | create | leaves | reissue [HOST] [--follow]
                               | rotate [--digest D] [--follow]>
  root show     fingerprint (SHA-256), serial and validity of the appliance's
                internal root; verify any downloaded copy against it
  root export   download the PUBLIC root certificate (PEM to stdout by
                default) and check it against the authenticated fingerprint
  local ...     CA status, local leaf certificates, re-issue, and the
                previewed root rotation (rotate prints the plan; with
                --digest D it approves that reviewed plan)`

// runTLSLocal drives the internal CA (S280, ADR-030): the same API the
// dashboard Trust page uses. Only public material is ever printed.
func runTLSLocal(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	switch args[0] {
	case "root":
		if len(args) < 2 {
			fmt.Fprintln(stderr, tlsLocalUsage)
			return exitUsage
		}
		switch args[1] {
		case "show":
			return runTLSRootShow(ctx, client, stdout, stderr)
		case "export":
			return runTLSRootExport(ctx, client, args[2:], stdout, stderr)
		}
	case "local":
		if len(args) < 2 {
			fmt.Fprintln(stderr, tlsLocalUsage)
			return exitUsage
		}
		switch args[1] {
		case "status":
			return runTLSLocalStatus(ctx, client, stdout, stderr)
		case "create":
			info, created, err := client.LocalCACreate(ctx)
			if err != nil {
				return fail(stderr, err)
			}
			if created {
				fmt.Fprintln(stdout, "internal CA created")
			} else {
				fmt.Fprintln(stdout, "internal CA already exists (unchanged)")
			}
			printCA(stdout, info)
			return exitOK
		case "leaves":
			leaves, err := client.LocalCALeaves(ctx)
			if err != nil {
				return fail(stderr, err)
			}
			printLeaves(stdout, leaves)
			return exitOK
		case "reissue":
			return runTLSLocalReissue(ctx, client, args[2:], stdout, stderr)
		case "rotate":
			return runTLSLocalRotate(ctx, client, args[2:], stdout, stderr)
		}
	}
	fmt.Fprintln(stderr, tlsLocalUsage)
	return exitUsage
}

func printCA(w io.Writer, ca cert.CAInfo) {
	fmt.Fprintf(w, "subject:     %s\nSHA-256:     %s\nserial:      %s\nvalid:       %s to %s\nconstrained: %s\ngeneration:  %d\n",
		ca.Subject, ca.FingerprintSHA256, ca.Serial, ca.NotBefore.UTC().Format(time.RFC3339),
		ca.NotAfter.UTC().Format(time.RFC3339), strings.Join(ca.PermittedDNS, ", "), ca.Generation)
	if !ca.KeyPresent {
		fmt.Fprintln(w, "WARNING: the CA private key is missing from the secret store")
	}
}

func printLeaves(w io.Writer, leaves []cert.Leaf) {
	if len(leaves) == 0 {
		fmt.Fprintln(w, "no local-only HTTPS routes use the internal CA")
	}
	for _, l := range leaves {
		expires := "-"
		if !l.NotAfter.IsZero() {
			expires = l.NotAfter.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%s: %s expires=%s\n", l.Host, l.Status, expires)
		if l.Detail != "" {
			fmt.Fprintf(w, "  %s\n", l.Detail)
		}
	}
}

func runTLSRootShow(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	report, err := client.LocalCAReport(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if !report.CA.Present {
		fmt.Fprintln(stdout, "the internal CA has not been created yet (homesteadctl tls local create)")
		return exitOK
	}
	printCA(stdout, report.CA)
	fmt.Fprintln(stdout, "compare with: openssl x509 -in homestead-local-ca.pem -noout -fingerprint -sha256")
	return exitOK
}

func runTLSRootExport(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	format, out := "pem", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--format", "--out":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, tlsLocalUsage)
				return exitUsage
			}
			if args[i] == "--format" {
				format = args[i+1]
			} else {
				out = args[i+1]
			}
			i++
		default:
			fmt.Fprintln(stderr, tlsLocalUsage)
			return exitUsage
		}
	}
	if format != "pem" && format != "der" {
		fmt.Fprintln(stderr, "--format must be pem or der")
		return exitUsage
	}
	if format == "der" && out == "" {
		fmt.Fprintln(stderr, "DER is binary: give --out FILE")
		return exitUsage
	}
	report, err := client.LocalCAReport(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if !report.CA.Present {
		fmt.Fprintln(stderr, "the internal CA has not been created yet (homesteadctl tls local create)")
		return exitError
	}
	body, err := client.LocalCARoot(ctx, format)
	if err != nil {
		return fail(stderr, err)
	}
	der := body
	if format == "pem" {
		block, _ := pem.Decode(body)
		if block == nil || block.Type != "CERTIFICATE" {
			fmt.Fprintln(stderr, "refusing: the download is not a certificate")
			return exitError
		}
		der = block.Bytes
	}
	got := cert.CertFingerprint(der)
	if got != report.CA.FingerprintSHA256 {
		fmt.Fprintf(stderr, "refusing: the downloaded root has SHA-256 %s, the authenticated fingerprint is %s\n", got, report.CA.FingerprintSHA256)
		return exitError
	}
	if out == "" {
		_, _ = stdout.Write(body)
	} else if err := os.WriteFile(out, body, 0o644); err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	fmt.Fprintf(stderr, "root certificate SHA-256 %s (matches the authenticated fingerprint)\n", got)
	return exitOK
}

func runTLSLocalStatus(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	report, err := client.LocalCAReport(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if report.CA.Present {
		printCA(stdout, report.CA)
	} else {
		fmt.Fprintln(stdout, "the internal CA has not been created yet")
	}
	printLeaves(stdout, report.Leaves)
	return exitOK
}

func runTLSLocalReissue(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	host, follow := "", false
	for _, a := range args {
		switch {
		case a == "--follow":
			follow = true
		case strings.HasPrefix(a, "-") || host != "":
			fmt.Fprintln(stderr, tlsLocalUsage)
			return exitUsage
		default:
			host = a
		}
	}
	id, err := client.LocalCAReissue(ctx, host)
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

func runTLSLocalRotate(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	digest, follow := "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--follow":
			follow = true
		case "--digest":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, tlsLocalUsage)
				return exitUsage
			}
			digest = args[i+1]
			i++
		default:
			fmt.Fprintln(stderr, tlsLocalUsage)
			return exitUsage
		}
	}
	if digest == "" {
		plan, err := client.LocalCARotationPlan(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "current root: SHA-256 %s (generation %d, expires %s)\n", plan.CurrentFingerprint, plan.Generation, plan.CurrentNotAfter.UTC().Format(time.RFC3339))
		fmt.Fprintf(stdout, "leaves to re-issue: %s\n", strings.Join(plan.Leaves, ", "))
		for _, step := range plan.Steps {
			fmt.Fprintf(stdout, "- %s\n", step)
		}
		fmt.Fprintf(stdout, "%s\napprove with: homesteadctl tls local rotate --digest %s\n", plan.DevicesMustRetrust, plan.Digest)
		return exitOK
	}
	id, err := client.LocalCARotate(ctx, digest)
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}
