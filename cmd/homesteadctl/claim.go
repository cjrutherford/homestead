package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"homestead/internal/apiclient"
	"homestead/internal/claim"
)

// runClaim redeems the single-use console token (S019). Credentials are
// not involved: the first administrator is created by S020 after claim.
func runClaim(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) >= 1 && args[0] == "token" {
		return runClaimToken(ctx, client, args[1:], stdout, stderr)
	}
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl claim TOKEN | homesteadctl claim token")
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

// runClaimToken shows the live claim token (S288). It is local: it reads the
// root-only file the daemon keeps in its runtime directory, so it needs
// root and no API. The API is asked only to explain a missing file.
func runClaimToken(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("claim token", flag.ContinueOnError)
	set.SetOutput(stderr)
	path := set.String("file", claim.DefaultTokenPath, "claim token file written by the daemon")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if set.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl claim token [--file PATH]")
		return exitUsage
	}
	tf, err := claim.ReadTokenFile(*path)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrPermission):
		fmt.Fprintf(stderr, "cannot read %s: the claim token is root-only; run `sudo homesteadctl claim token`\n", *path)
		return exitError
	case errors.Is(err, os.ErrNotExist):
		return explainNoToken(ctx, client, stderr)
	default:
		return fail(stderr, err)
	}
	remaining := time.Until(tf.Expires).Round(time.Second)
	if remaining <= 0 {
		fmt.Fprintln(stderr, "the claim token has expired; the daemon mints a fresh one within seconds, run this command again")
		return exitError
	}
	// The unit preserves the runtime directory, so the file can outlive the
	// daemon. Print only when the daemon answers and the appliance is
	// still unclaimed.
	id, idErr := client.Identity(ctx)
	if idErr != nil {
		fmt.Fprintln(stderr, "Homestead is not running (or not reachable); start it with `sudo systemctl start homestead-server`. Not printing a claim token the daemon may no longer honor")
		return exitError
	}
	if id.State == string(claim.StateClaimed) {
		fmt.Fprintln(stderr, "this appliance is already claimed; there is no claim token")
		return exitError
	}
	fmt.Fprintf(stdout, "token: %s\nexpires: %s (in %s)\n", tf.Token, tf.Expires.Format(time.RFC3339), remaining)
	return exitOK
}

func explainNoToken(ctx context.Context, client *apiclient.Client, stderr io.Writer) int {
	id, err := client.Identity(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "the Homestead daemon is not running (or not reachable); start it with `sudo systemctl start homestead-server`")
		return exitError
	}
	if id.State == string(claim.StateClaimed) {
		fmt.Fprintln(stderr, "this appliance is already claimed; there is no claim token")
		return exitError
	}
	fmt.Fprintln(stderr, "no claim token file yet; the daemon writes it when it starts, check `journalctl -u homestead-server`")
	return exitError
}
