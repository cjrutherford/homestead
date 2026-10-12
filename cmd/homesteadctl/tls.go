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
// expired certificate exits 1, renewal-due and plan-required exit 0
// (the detail says what to do).
func runTLS(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "renewal" {
		fmt.Fprintln(stderr, "usage: homesteadctl tls renewal")
		return exitUsage
	}
	statuses, err := client.TLSRenewal(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	code := exitOK
	for _, status := range statuses {
		expires := "-"
		if !status.NotAfter.IsZero() {
			expires = status.NotAfter.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(stdout, "%s: %s days=%d expires=%s\n  %s\n",
			status.Certificate, status.Status, status.DaysRemaining, expires, status.Detail)
		if status.Status == cert.RenewalExpired {
			code = exitError
		}
	}
	return code
}
