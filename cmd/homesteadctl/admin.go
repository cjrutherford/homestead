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

// runAdmin dispatches the admin group: setup (S020), root-only local
// recovery (S021), self-service rotation and lockout status (S239).
func runAdmin(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl admin <setup|recover|passwd|lockout> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "setup":
		return runAdminSetup(ctx, client, args[1:], stdout, stderr)
	case "recover":
		return runAdminRecover(ctx, args[1:], stdout, stderr)
	case "passwd":
		return runAdminPasswd(ctx, client, args[1:], stdout, stderr)
	case "lockout":
		return runAdminLockout(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown admin subcommand %q\n", args[0])
		return exitUsage
	}
}

func credentialFlags(set *flag.FlagSet) (*string, *string) {
	username := set.String("username", "", "administrator username")
	passwordFile := set.String("password-file", "", "file holding the password")
	return username, passwordFile
}

func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("empty secret in %s", path)
	}
	return secret, nil
}

func runAdminSetup(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("admin setup", flag.ContinueOnError)
	set.SetOutput(stderr)
	username, passwordFile := credentialFlags(set)
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *username == "" || *passwordFile == "" {
		fmt.Fprintln(stderr, "admin setup requires --username and --password-file")
		return exitUsage
	}
	password, err := readSecretFile(*passwordFile)
	if err != nil {
		return fail(stderr, err)
	}
	admin, session, err := client.SetupAdminSession(ctx, *username, password)
	if err != nil {
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) {
			switch statusErr.Status {
			case 403:
				fmt.Fprintln(stderr, "appliance must be claimed first")
				return exitError
			case 409:
				fmt.Fprintln(stderr, "administrator already exists")
				return exitError
			case 422:
				fmt.Fprintf(stderr, "invalid administrator: %s\n", statusErr.Body)
				return exitError
			}
		}
		return fail(stderr, err)
	}
	// Setup logs the owner in: keep the session so the next command
	// works without a separate login (the token is never printed here).
	if session.Token != "" {
		if err := saveSession(storedSession{Server: client.BaseURL(), Token: session.Token, ExpiresAt: session.ExpiresAt}); err != nil {
			fmt.Fprintln(stderr, "warning: session not stored:", err)
		}
	}
	writeJSON(stdout, admin)
	return exitOK
}

func runLogin(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("login", flag.ContinueOnError)
	set.SetOutput(stderr)
	username, passwordFile := credentialFlags(set)
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *username == "" || *passwordFile == "" {
		fmt.Fprintln(stderr, "login requires --username and --password-file")
		return exitUsage
	}
	password, err := readSecretFile(*passwordFile)
	if err != nil {
		return fail(stderr, err)
	}
	session, err := client.Login(ctx, *username, password)
	if err != nil {
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) {
			switch statusErr.Status {
			case 401:
				fmt.Fprintln(stderr, "invalid username or password")
				return exitError
			case 429:
				fmt.Fprintln(stderr, "account temporarily locked after repeated failures")
				return exitError
			}
		}
		return fail(stderr, err)
	}
	// The CSRF token is a browser concern; the CLI neither prints nor keeps it.
	session.CSRFToken = ""
	if err := saveSession(storedSession{Server: client.BaseURL(), Token: session.Token, ExpiresAt: session.ExpiresAt}); err != nil {
		fmt.Fprintln(stderr, "warning: session not stored:", err)
	}
	writeJSON(stdout, session)
	return exitOK
}

func runLogout(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("logout", flag.ContinueOnError)
	set.SetOutput(stderr)
	token := sessionTokenFlag(set)
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *token == "" {
		*token = client.Token() // stored session from `login`
	}
	if *token == "" {
		fmt.Fprintln(stderr, "logout requires --token, HOMESTEAD_TOKEN, or a stored session (homesteadctl login)")
		return exitUsage
	}
	err := client.Logout(ctx, *token)
	var statusErr *apiclient.StatusError
	revoked := errors.As(err, &statusErr) && statusErr.Status == 401
	if (err == nil || revoked) && loadSession(client.BaseURL()) == *token {
		// The session is gone server-side either way; drop the local copy.
		if delErr := deleteSession(); delErr != nil {
			fmt.Fprintln(stderr, "warning: stored session not removed:", delErr)
		}
	}
	if revoked {
		fmt.Fprintln(stderr, "invalid or expired session")
		return exitError
	}
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, "logged out")
	return exitOK
}

func sessionTokenFlag(set *flag.FlagSet) *string {
	return set.String("token", os.Getenv("HOMESTEAD_TOKEN"), "session token (or HOMESTEAD_TOKEN)")
}

func runAdminPasswd(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("admin passwd", flag.ContinueOnError)
	set.SetOutput(stderr)
	currentFile := set.String("current-password-file", "", "file holding the current password")
	newFile := set.String("new-password-file", "", "file holding the replacement password")
	token := sessionTokenFlag(set)
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *token == "" {
		*token = client.Token()
	}
	if *currentFile == "" || *newFile == "" || *token == "" {
		fmt.Fprintln(stderr, "admin passwd requires --current-password-file, --new-password-file and --token (or HOMESTEAD_TOKEN)")
		return exitUsage
	}
	current, err := readSecretFile(*currentFile)
	if err != nil {
		return fail(stderr, err)
	}
	replacement, err := readSecretFile(*newFile)
	if err != nil {
		return fail(stderr, err)
	}
	if err := client.ChangePassword(ctx, *token, current, replacement); err != nil {
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) {
			switch statusErr.Status {
			case 401:
				// The boundary's 401 is about the session; the
				// handler's is about the current password.
				if statusErr.Code == "invalid-current-password" {
					fmt.Fprintln(stderr, "invalid username or password")
					return exitError
				}
			case 422:
				fmt.Fprintln(stderr, "replacement password too weak")
				return exitError
			case 429:
				fmt.Fprintln(stderr, "account temporarily locked after repeated failures")
				return exitError
			}
		}
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, "password changed; other sessions revoked")
	return exitOK
}

func runAdminLockout(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("admin lockout", flag.ContinueOnError)
	set.SetOutput(stderr)
	token := sessionTokenFlag(set)
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *token == "" {
		*token = client.Token()
	}
	if *token == "" {
		fmt.Fprintln(stderr, "admin lockout requires --token (or HOMESTEAD_TOKEN)")
		return exitUsage
	}
	status, err := client.LockoutStatus(ctx, *token)
	if err != nil {
		return fail(stderr, err)
	}
	if status.Locked {
		fmt.Fprintf(stdout, "locked until %s (failures: %d)\n", status.LockedUntil, status.Failures)
		return exitOK
	}
	fmt.Fprintf(stdout, "unlocked (failures: %d)\n", status.Failures)
	return exitOK
}
