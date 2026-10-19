package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"homestead/internal/apiclient"
)

func runTunnel(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl tunnel <status|setup|teardown> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "status":
		return runTunnelStatus(ctx, client, stdout, stderr)
	case "setup":
		return runTunnelSetup(ctx, client, args[1:], stdout, stderr)
	case "teardown":
		return runTunnelTeardown(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown tunnel subcommand %q\n", args[0])
		return exitUsage
	}
}

func runTunnelStatus(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	status, err := client.TunnelStatus(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "preferred: %s", status.Preferred)
	if status.Gateway != "" {
		fmt.Fprintf(stdout, " (gateway %s on %s)", status.Gateway, status.Interface)
	}
	fmt.Fprintln(stdout)
	if len(status.Protocols) > 0 {
		fmt.Fprintf(stdout, "router protocols: %s\n", strings.Join(status.Protocols, ", "))
	}
	fmt.Fprintf(stdout, "connector: installed=%v active=%v\n",
		status.Connector.Installed, status.Connector.Active)
	for _, tunnel := range status.Tunnels {
		fmt.Fprintf(stdout, "%s %s: %s (%d connections)\n",
			tunnel.ID, tunnel.Name, tunnel.Status, len(tunnel.Connections))
	}
	for _, route := range status.Routes {
		fmt.Fprintf(stdout, "%s -> %s via %s\n", route.Hostname, route.Service, route.TunnelID)
	}
	return exitOK
}

func runTunnelSetup(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var name, hostname, service, apiToken, accountID, accountIDFile, apiTokenFile string
	var follow bool
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--name" && i+1 < len(args):
			name = args[i+1]
			i++
		case args[i] == "--hostname" && i+1 < len(args):
			hostname = args[i+1]
			i++
		case args[i] == "--service" && i+1 < len(args):
			service = args[i+1]
			i++
		case args[i] == "--api-token" && i+1 < len(args):
			apiToken = args[i+1]
			warnArgvSecret(stderr, "--api-token", "--api-token-file")
			i++
		case args[i] == "--account-id" && i+1 < len(args):
			accountID = args[i+1]
			warnArgvSecret(stderr, "--account-id", "--account-id-file")
			i++
		case args[i] == "--account-id-file" && i+1 < len(args):
			accountIDFile = args[i+1]
			i++
		case args[i] == "--api-token-file" && i+1 < len(args):
			apiTokenFile = args[i+1]
			i++
		case args[i] == "--follow":
			follow = true
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			fmt.Fprintf(stderr, "unexpected argument %q\n", args[i])
			return exitUsage
		}
	}
	if accountIDFile != "" && apiTokenFile == accountIDFile {
		fmt.Fprintln(stderr, "the account ID and the API token cannot both come from the same file")
		return exitUsage
	}
	var err error
	if apiTokenFile != "" {
		if apiToken, err = readValueFile(apiTokenFile); err != nil {
			return fail(stderr, err)
		}
	}
	if accountIDFile != "" {
		if accountID, err = readValueFile(accountIDFile); err != nil {
			return fail(stderr, err)
		}
	}
	if name == "" || hostname == "" || service == "" || apiToken == "" || accountID == "" {
		fmt.Fprintln(stderr, "usage: homesteadctl tunnel setup --name NAME --hostname HOST --service URL --api-token-file FILE|- --account-id-file FILE|- [--follow]")
		fmt.Fprintln(stderr, "       (use - to read a value from stdin; the --api-token and --account-id arguments land in shell history and are deprecated)")
		return exitUsage
	}
	id, err := client.TunnelSetup(ctx, name, hostname, service, apiToken, accountID)
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

func runTunnelTeardown(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var accountID, accountIDFile string
	var follow bool
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--account-id" && i+1 < len(args):
			accountID = args[i+1]
			warnArgvSecret(stderr, "--account-id", "--account-id-file")
			i++
		case args[i] == "--account-id-file" && i+1 < len(args):
			accountIDFile = args[i+1]
			i++
		case args[i] == "--follow":
			follow = true
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			positional = append(positional, args[i])
		}
	}
	if accountIDFile != "" {
		var err error
		if accountID, err = readValueFile(accountIDFile); err != nil {
			return fail(stderr, err)
		}
	}
	if len(positional) != 1 || accountID == "" {
		fmt.Fprintln(stderr, "usage: homesteadctl tunnel teardown <tunnel-id> --account-id-file FILE|- [--follow]")
		fmt.Fprintln(stderr, "       (use - to read the account ID from stdin; the --account-id argument lands in shell history and is deprecated)")
		return exitUsage
	}
	id, err := client.TunnelTeardown(ctx, positional[0], accountID)
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

// stdinReader is where "-" value files read from; tests replace it.
var stdinReader io.Reader = os.Stdin

// warnArgvSecret tells the owner a credential given as an argument is in
// argv and shell history (S288).
func warnArgvSecret(stderr io.Writer, flagName, fileFlag string) {
	fmt.Fprintf(stderr, "warning: %s on the command line is visible in the process list and shell history; use %s (a file, or - for stdin)\n", flagName, fileFlag)
}

// readValueFile reads one trimmed value from a file, or from stdin for "-".
func readValueFile(path string) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdinReader)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("empty value in %s", path)
	}
	return value, nil
}
