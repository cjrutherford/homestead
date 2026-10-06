package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"homestead/internal/apiclient"
	"homestead/internal/server"
)

// runAdminRecover resets an administrator password through the root-only
// local socket (S021). Root possession of the socket is the authorization:
// 0600 file mode rejects non-root callers at connect time.
func runAdminRecover(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("admin recover", flag.ContinueOnError)
	set.SetOutput(stderr)
	username, passwordFile := credentialFlags(set)
	socketPath := set.String("socket", server.DefaultSocketPath, "daemon Unix socket")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *username == "" || *passwordFile == "" {
		fmt.Fprintln(stderr, "admin recover requires --username and --password-file")
		return exitUsage
	}
	password, err := readSecretFile(*passwordFile)
	if err != nil {
		return fail(stderr, err)
	}
	client := apiclient.New("http://localhost", apiclient.WithHTTPClient(apiclient.UnixClient(*socketPath)))
	admin, err := client.Recover(ctx, *username, password)
	if err != nil {
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) {
			switch statusErr.Status {
			case 404:
				fmt.Fprintln(stderr, "administrator not found")
				return exitError
			case 422:
				fmt.Fprintf(stderr, "invalid administrator: %s\n", statusErr.Body)
				return exitError
			}
		}
		return fail(stderr, err)
	}
	writeJSON(stdout, admin)
	return exitOK
}
