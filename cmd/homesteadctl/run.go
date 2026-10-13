package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

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
	// Credentials: HOMESTEAD_TOKEN wins, else the session stored by
	// `homesteadctl login` for this server (ADR-020).
	token := os.Getenv("HOMESTEAD_TOKEN")
	if token == "" {
		var hint string
		token, hint = loadSessionHint(strings.TrimRight(*server, "/"))
		if hint != "" && needsSession(rest[0]) {
			fmt.Fprintln(stderr, "hint:", hint)
		}
	}
	client := apiclient.New(*server, apiclient.WithToken(token))
	switch rest[0] {
	case "status":
		return runStatus(ctx, client, stdout, stderr)
	case "doctor":
		return runDoctor(ctx, client, stdout, stderr)
	case "docker":
		return runDocker(ctx, client, rest[1:], stdout, stderr)
	case "audit":
		return runAudit(ctx, client, rest[1:], stdout, stderr)
	case "apps":
		return runApps(ctx, client, rest[1:], stdout, stderr)
	case "operations":
		return runOperations(ctx, client, rest[1:], stdout, stderr)
	case "firewall":
		return runFirewall(ctx, client, rest[1:], stdout, stderr)
	case "exposure":
		return runExposure(ctx, client, rest[1:], stdout, stderr)
	case "tunnel":
		return runTunnel(ctx, client, rest[1:], stdout, stderr)
	case "drift":
		return runDrift(ctx, client, rest[1:], stdout, stderr)
	case "data":
		return runData(ctx, client, rest[1:], stdout, stderr)
	case "updates":
		return runUpdates(ctx, client, rest[1:], stdout, stderr)
	case "secrets":
		return runSecrets(ctx, client, rest[1:], stdout, stderr)
	case "backup":
		return runBackup(ctx, client, rest[1:], stdout, stderr)
	case "tls":
		return runTLS(ctx, client, rest[1:], stdout, stderr)
	case "scheduler":
		return runScheduler(ctx, client, rest[1:], stdout, stderr)
	case "edge":
		return runEdge(ctx, client, rest[1:], stdout, stderr)
	case "storage":
		return runStorage(ctx, client, rest[1:], stdout, stderr)
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

// needsSession is false for commands that work without a session.
func needsSession(command string) bool {
	switch command {
	case "status", "version", "claim", "login":
		return false
	}
	return true
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: homesteadctl [--server URL] <command>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  status                 daemon health and version")
	fmt.Fprintln(w, "  doctor                 system diagnostics (S044)")
	fmt.Fprintln(w, "  docker health          Docker engine and inventory, read-only (S073)")
	fmt.Fprintln(w, "  audit list             audit trail, newest first (S063)")
	fmt.Fprintln(w, "  apps list              curated packages (S167)")
	fmt.Fprintln(w, "  apps plan <pkg> -f FILE  review install plan (S168)")
	fmt.Fprintln(w, "  apps install <pkg> -f FILE [--follow]  install package (S168)")
	fmt.Fprintln(w, "  apps configure <pkg> --service NAME [--set K=V] [--unset K] [--rotate-secret NAME] | --adopt-secrets  configure service (S168, S266)")
	fmt.Fprintln(w, "  apps secrets <pkg>     list secret-backed variables (S266)")
	fmt.Fprintln(w, "  apps secrets register <pkg> <secret> --value-file PATH|- [--skip-verify] [--force]  register an existing secret (S266)")
	fmt.Fprintln(w, "  apps uninstall <pkg> [--remove-data --data-dir M:P]... [--abandon-volumes] [--follow]  uninstall package (S236)")
	fmt.Fprintln(w, "  operations <get|list|follow>  inspect and follow operations (S062/S064)")
	fmt.Fprintln(w, "  firewall <status|plan|apply>  host firewall setup (S056)")
	fmt.Fprintln(w, "  exposure <review|approvals|approve|apply>  public exposure review and apply (S245)")
	fmt.Fprintln(w, "  tunnel <status|setup|teardown>  tunnel remote access (S246)")
	fmt.Fprintln(w, "  drift <review|accept|restore> <package> [--follow]  managed-state drift choices (S244)")
	fmt.Fprintln(w, "  drift schedule [--interval 6h] [--enabled true|false]  periodic scan cadence and last result (S261)")
	fmt.Fprintln(w, "  data <status|plan|approve|remove-volumes>  app data placement, migration off Docker volumes, and old-volume removal (S277)")
	fmt.Fprintln(w, "  updates <list|apply|platform|verify|trust>  application updates (S243); platform release verification and trusted keys (S264)")
	fmt.Fprintln(w, "  secrets <status|rekey>  secret-store key version and confirmed re-key (S258)")
	fmt.Fprintln(w, "  backup <destination|schedules|schedule|unschedule>  backup configuration (S238)")
	fmt.Fprintln(w, "  tls renewal  certificate renewal status, reporting only (S263)")
	fmt.Fprintln(w, "  scheduler jobs scheduled jobs, last run and next due (S260)")
	fmt.Fprintln(w, "  edge deploy [--follow]  deploy the Caddy edge proxy (S237)")
	fmt.Fprintln(w, "  edge routes set -f FILE [--follow]  reconcile the proxy route table (S237)")
	fmt.Fprintln(w, "  storage <mounts|register|registrations|assign|assignments>  mounted storage setup (S235)")
	fmt.Fprintln(w, "  version                CLI and daemon versions")
	fmt.Fprintln(w, "  config get             print current desired state")
	fmt.Fprintln(w, "  config plan -f FILE    validate settings without persisting")
	fmt.Fprintln(w, "  config apply -f FILE   validate and persist settings")
	fmt.Fprintln(w, "  claim TOKEN              redeem the console claim token (S019)")
	fmt.Fprintln(w, "  admin setup              create the first administrator (once, S020)")
	fmt.Fprintln(w, "  login                    log in and store the session (S020, S256)")
	fmt.Fprintln(w, "  logout                   revoke the session and delete the stored token (S020, S256)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "FILE holds JSON or YAML (by extension) with settings,")
	fmt.Fprintln(w, "either bare or under a \"settings\" key.")
}

func fail(stderr io.Writer, err error) int {
	var statusErr *apiclient.StatusError
	if errors.As(err, &statusErr) {
		switch {
		case statusErr.Status == 401:
			fmt.Fprintln(stderr, "Error: not logged in, or the session expired or was revoked; run `homesteadctl login`")
			return exitError
		case statusErr.Status == 403 && strings.Contains(statusErr.Body, "not claimed"):
			fmt.Fprintln(stderr, "Error: the appliance is not claimed yet; run `homesteadctl claim TOKEN` first")
			return exitError
		}
	}
	fmt.Fprintf(stderr, "Error: %v\n", err)
	return exitError
}
