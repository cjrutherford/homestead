package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"homestead/internal/apiclient"
	"homestead/internal/craftfiles"
)

const craftFilesUsage = `usage: homesteadctl craft files <status|link|rotate|unlink|local-only|launch> ...
  status
  link --url URL --account NAME [--root FOLDER] [--allow-insecure-http] (--password-file FILE | --create-app-password --login-password-file FILE) [--yes]
  rotate (--password-file FILE | --create-app-password --login-password-file FILE) [--root FOLDER] [--allow-insecure-http] [--yes]
  unlink [--yes]
  local-only APP on|off
  launch APP`

// runCraftFiles drives Nextcloud file storage for craft apps (S254,
// ADR-029): link, rotate and unlink go through plan and approve; secrets
// come from files, never arguments, and are never printed.
func runCraftFiles(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, craftFilesUsage)
		return exitUsage
	}
	switch args[0] {
	case "status":
		return runCraftFilesStatus(ctx, client, stdout, stderr)
	case "link", "rotate", "unlink":
		return runCraftFilesChange(ctx, client, args[0], args[1:], stdout, stderr)
	case "local-only":
		if len(args) != 3 || (args[2] != "on" && args[2] != "off") {
			fmt.Fprintln(stderr, "usage: homesteadctl craft files local-only APP on|off")
			return exitUsage
		}
		st, err := client.CraftFilesLocalOnly(ctx, args[1], args[2] == "on")
		if err != nil {
			return fail(stderr, err)
		}
		printCraftFiles(stdout, st)
		return exitOK
	case "launch":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: homesteadctl craft files launch APP")
			return exitUsage
		}
		res, err := client.CraftFilesLaunch(ctx, args[1])
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "open this address in your browser within %s (one use):\n%s\n",
			time.Until(res.ExpiresAt).Round(time.Second), res.URL)
		return exitOK
	}
	fmt.Fprintf(stderr, "unknown craft files subcommand %q\n%s\n", args[0], craftFilesUsage)
	return exitUsage
}

func runCraftFilesStatus(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	st, err := client.CraftFilesStatus(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	printCraftFiles(stdout, st)
	return exitOK
}

func printCraftFiles(w io.Writer, st craftfiles.Status) {
	if !st.Linked {
		fmt.Fprintln(w, "Nextcloud: not linked (craft apps keep documents in the browser)")
	} else {
		cred := "app password sealed"
		if !st.CredentialStored {
			cred = "APP PASSWORD MISSING"
		}
		fmt.Fprintf(w, "Nextcloud: %s account %s folder %s (%s)\n", st.BaseURL, st.Account, st.Root, cred)
	}
	for _, a := range st.Apps {
		state := "Nextcloud"
		switch {
		case a.LocalOnly:
			state = "browser-local (opted out)"
		case !st.Linked:
			state = "browser-local (not linked)"
		case a.Host == "":
			state = "browser-local (no edge route recorded)"
		}
		fmt.Fprintf(w, "  %-12s %-9s %s\n", a.Name, a.Mode, state)
	}
}

func runCraftFilesChange(ctx context.Context, client *apiclient.Client, action string, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("craft files "+action, flag.ContinueOnError)
	set.SetOutput(stderr)
	url := set.String("url", "", "address of your Nextcloud")
	account := set.String("account", "", "Nextcloud account name")
	root := set.String("root", "", "folder in the account that holds the documents")
	passwordFile := set.String("password-file", "", "file holding the Nextcloud app password")
	create := set.Bool("create-app-password", false, "have Homestead create the app password (needs --login-password-file)")
	loginFile := set.String("login-password-file", "", "file holding the account password, used once and not kept")
	insecure := set.Bool("allow-insecure-http", false, "accept a plain http:// Nextcloud address that is not on this machine (the app password then crosses the network unencrypted)")
	yes := set.Bool("yes", false, "approve the plan without asking")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	req := craftfiles.PlanRequest{Action: action, BaseURL: *url, Account: *account, Root: *root, CreateAppPassword: *create, AllowInsecureHTTP: *insecure}
	if action == craftfiles.ActionLink && req.Root == "" {
		req.Root = "Homestead"
	}
	var ap craftfiles.ApproveRequest
	if action != craftfiles.ActionUnlink {
		var err error
		if *create {
			if *loginFile == "" {
				fmt.Fprintln(stderr, "--create-app-password needs --login-password-file")
				return exitUsage
			}
			ap.AccountPassword, err = readSecretFile(*loginFile)
		} else {
			if *passwordFile == "" {
				fmt.Fprintln(stderr, action+" needs --password-file (or --create-app-password)")
				return exitUsage
			}
			ap.AppPassword, err = readSecretFile(*passwordFile)
		}
		if err != nil {
			return fail(stderr, err)
		}
	}
	plan, err := client.CraftFilesPlan(ctx, req)
	if err != nil {
		return fail(stderr, err)
	}
	for _, line := range plan.Summary {
		fmt.Fprintln(stdout, line)
	}
	for _, line := range plan.Warnings {
		fmt.Fprintln(stdout, "warning:", line)
	}
	if !*yes {
		fmt.Fprintf(stdout, "plan %s (expires %s); rerun with --yes to approve\n", plan.ID, plan.ExpiresAt.Format(time.RFC3339))
		return exitOK
	}
	ap.PlanID = plan.ID
	st, err := client.CraftFilesApprove(ctx, ap)
	if err != nil {
		return fail(stderr, err)
	}
	printCraftFiles(stdout, st)
	return exitOK
}
