package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
)

const dashboardUsage = `usage: homesteadctl dashboard address [get]
       homesteadctl dashboard address set NAME [--exposure local|overlay] [--follow]
  address get   the dashboard's local network addresses; the .local one is
                first because it needs no DNS setup
  address set   the owner-set local name (default homestead.lan). The route
                is local or overlay only; the tunnel is the public path.

To use the CLI from another machine: --server https://homestead.local
--ca-file homestead-local-ca.pem (or HOMESTEAD_CA_FILE) and a token from
homesteadctl login. The certificate is always verified.`

// runDashboard drives the dashboard address (S289, ADR-035), the same API
// the dashboard's Trust page uses.
func runDashboard(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "address" {
		fmt.Fprintln(stderr, dashboardUsage)
		return exitUsage
	}
	args = args[1:]
	if len(args) == 0 || args[0] == "get" {
		if len(args) > 1 {
			fmt.Fprintln(stderr, dashboardUsage)
			return exitUsage
		}
		return runDashboardAddressGet(ctx, client, stdout, stderr)
	}
	if args[0] != "set" {
		fmt.Fprintln(stderr, dashboardUsage)
		return exitUsage
	}
	name, exposure, follow := "", "", false
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--follow":
			follow = true
		case a == "--exposure" && i+1 < len(rest):
			i++
			exposure = rest[i]
		case strings.HasPrefix(a, "--exposure="):
			exposure = strings.TrimPrefix(a, "--exposure=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", a)
			return exitUsage
		case name == "":
			name = a
		default:
			fmt.Fprintln(stderr, dashboardUsage)
			return exitUsage
		}
	}
	if name == "" {
		fmt.Fprintln(stderr, dashboardUsage)
		return exitUsage
	}
	if exposure == "public" {
		fmt.Fprintln(stderr, "Error: the dashboard is local or overlay only; the Cloudflare tunnel is the public path")
		return exitError
	}
	id, err := client.SetDashboardAddress(ctx, name, exposure)
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

func runDashboardAddressGet(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	a, err := client.DashboardAddress(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if !a.Configured {
		fmt.Fprintf(stdout, "not configured yet; the default is %s (set it with: homesteadctl dashboard address set NAME)\n", a.Default)
		return exitOK
	}
	fmt.Fprintf(stdout, "name: %s\nexposure: %s\n", a.Name, a.Exposure)
	for _, u := range a.URLs {
		fmt.Fprintf(stdout, "url: %s\n", u)
	}
	fmt.Fprintf(stdout, "root certificate: homesteadctl tls root export (public: %s)\n", a.RootPath)
	return exitOK
}
