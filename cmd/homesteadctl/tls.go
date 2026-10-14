package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"homestead/internal/apiclient"
	"homestead/internal/cert"
)

// runTLS prints certificate renewal status (S263). Reporting only: an
// expired or unreadable certificate exits 1, renewal-due and plan-required exit 0
// (the detail says what to do).
func runTLS(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "issuance" {
		return runTLSIssuance(ctx, client, args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "renewal" {
		fmt.Fprintln(stderr, "usage: homesteadctl tls <renewal | issuance ...>")
		return exitUsage
	}
	report, err := client.TLSRenewal(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	code := exitOK
	if report.Recorded == 0 {
		fmt.Fprintln(stdout, report.Summary)
	}
	for _, status := range report.Certificates {
		expires := "-"
		if !status.NotAfter.IsZero() {
			expires = status.NotAfter.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(stdout, "%s: %s days=%d expires=%s\n  %s\n",
			status.Certificate, status.Status, status.DaysRemaining, expires, status.Detail)
		if status.Status == cert.RenewalExpired || status.Status == cert.RenewalError {
			code = exitError
		}
	}
	return code
}
