package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"homestead/internal/apiclient"
)

// Exit codes: 0 success, 1 runtime/API/validation failure, 2 usage error.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// defaultServerURL is the loopback daemon (ADR-006); HOMESTEAD_SERVER
// overrides it without repeating --server on every invocation.
const defaultServerURL = "http://127.0.0.1:8080"

func serverURL() string {
	if env := os.Getenv("HOMESTEAD_SERVER"); env != "" {
		return env
	}
	return defaultServerURL
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	top := flag.NewFlagSet("homesteadctl", flag.ContinueOnError)
	top.SetOutput(stderr)
	server := top.String("server", serverURL(), "daemon base URL")
	if err := top.Parse(args); err != nil {
		return exitUsage
	}
	rest := top.Args()
	if len(rest) == 0 {
		usage(stderr)
		return exitUsage
	}
	client := apiclient.New(*server)
	switch rest[0] {
	case "status":
		return runStatus(ctx, client, stdout, stderr)
	case "doctor":
		return runDoctor(ctx, client, stdout, stderr)
	case "audit":
		return runAudit(ctx, client, rest[1:], stdout, stderr)
	case "operations":
		return runOperations(ctx, client, rest[1:], stdout, stderr)
	case "firewall":
		return runFirewall(ctx, client, rest[1:], stdout, stderr)
	case "version":
		return runVersion(ctx, client, stdout, stderr)
	case "config":
		return runConfig(ctx, client, rest[1:], stdout, stderr)
	case "claim":
		return runClaim(ctx, client, rest[1:], stdout, stderr)
	case "admin":
		return runAdmin(ctx, client, rest[1:], stdout, stderr)
	case "login":
		return runLogin(ctx, client, rest[1:], stdout, stderr)
	case "logout":
		return runLogout(ctx, client, rest[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", rest[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: homesteadctl [--server URL] <command>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  status                 daemon health and version")
	fmt.Fprintln(w, "  doctor                 system diagnostics (S044)")
	fmt.Fprintln(w, "  audit list             audit trail, newest first (S063)")
	fmt.Fprintln(w, "  operations <get|follow> ID  inspect and follow operations (S062)")
	fmt.Fprintln(w, "  firewall <status|plan|apply>  host firewall setup (S056)")
	fmt.Fprintln(w, "  version                CLI and daemon versions")
	fmt.Fprintln(w, "  config get             print current desired state")
	fmt.Fprintln(w, "  config plan -f FILE    validate settings without persisting")
	fmt.Fprintln(w, "  config apply -f FILE   validate and persist settings")
	fmt.Fprintln(w, "  claim TOKEN              redeem the console claim token (S019)")
	fmt.Fprintln(w, "  admin setup              create the first administrator (once, S020)")
	fmt.Fprintln(w, "  login                    mint a session token (S020)")
	fmt.Fprintln(w, "  logout                   destroy a session token (S020)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "FILE holds JSON or YAML (by extension) with settings,")
	fmt.Fprintln(w, "either bare or under a \"settings\" key.")
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "Error: %v\n", err)
	return exitError
}
