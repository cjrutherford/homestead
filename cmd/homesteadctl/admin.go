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

// runAdmin dispatches the admin group: setup (S020) and root-only local
// recovery (S021).
func runAdmin(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl admin <setup|recover> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "setup":
		return runAdminSetup(ctx, client, args[1:], stdout, stderr)
	case "recover":
		return runAdminRecover(ctx, args[1:], stdout, stderr)
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
	admin, err := client.SetupAdmin(ctx, *username, password)
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
	writeJSON(stdout, session)
	return exitOK
}

func runLogout(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("logout", flag.ContinueOnError)
	set.SetOutput(stderr)
	token := set.String("token", os.Getenv("HOMESTEAD_TOKEN"), "session token (or HOMESTEAD_TOKEN)")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *token == "" {
		fmt.Fprintln(stderr, "logout requires --token or HOMESTEAD_TOKEN")
		return exitUsage
	}
	if err := client.Logout(ctx, *token); err != nil {
		var statusErr *apiclient.StatusError
		if errors.As(err, &statusErr) && statusErr.Status == 401 {
			fmt.Fprintln(stderr, "invalid or expired session")
			return exitError
		}
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, "logged out")
	return exitOK
}
