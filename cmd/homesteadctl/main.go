// Command homesteadctl is the Homestead command-line client.
//
// Every operation goes through the daemon API via internal/apiclient
// (ADR-004); the CLI never touches state files or the database directly.
// Claim (S019) and admin recovery (S021) extend this foundation.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if code := run(ctx, os.Args[1:], os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}
