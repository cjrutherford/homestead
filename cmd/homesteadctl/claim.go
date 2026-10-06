package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"homestead/internal/apiclient"
)

// runClaim redeems the single-use console token (S019). Credentials are
// not involved: the first administrator is created by S020 after claim.
func runClaim(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl claim TOKEN")
		return exitUsage
	}
	state, err := client.Claim(ctx, args[0])
	if err != nil {
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) {
			switch statusErr.Status {
			case 409: // already claimed
				fmt.Fprintln(stderr, "appliance already claimed")
				return exitError
			case 401: // unknown or expired token
				fmt.Fprintln(stderr, "invalid or expired claim token")
				return exitError
			}
		}
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "state: %s\n", state)
	return exitOK
}
