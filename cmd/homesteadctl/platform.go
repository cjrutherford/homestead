package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
)

// runUpdatesPlatform shows the platform plan with per-release
// artifact verification state (S264, ADR-025).
func runUpdatesPlatform(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	view, err := client.PlatformUpdates(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if len(view.Releases) == 0 {
		fmt.Fprintln(stdout, "no platform releases verified yet; run `homesteadctl updates verify`")
	}
	for _, rel := range view.Releases {
		fmt.Fprintf(stdout, "%s %s (counter %d): %s\n", rel.Component, orDash(rel.Version), rel.Counter, rel.State)
		if rel.State != "verified" && rel.Detail != "" {
			fmt.Fprintf(stdout, "  %s\n", rel.Detail)
		}
		if rel.Override {
			fmt.Fprintln(stdout, "  older release allowed by owner override (audited)")
		}
		if rel.PlanError != "" && !strings.HasPrefix(rel.PlanError, "refused:") {
			fmt.Fprintf(stdout, "  not planned: %s\n", rel.PlanError)
		}
	}
	for _, rel := range view.Refused {
		fmt.Fprintf(stdout, "refused (never planned): %s %s: %s\n", rel.Component, orDash(rel.Version), rel.PlanError)
	}
	for _, move := range view.Plan.Components {
		fmt.Fprintf(stdout, "plan: %s %s -> %s [%s]\n", move.Component, move.Current, move.Target, move.Verification)
	}
	for _, warning := range view.Plan.Warnings {
		fmt.Fprintf(stdout, "  warning: %s\n", warning)
	}
	if view.Plan.Backup {
		fmt.Fprintf(stdout, "  backup required: %s\n", view.Plan.BackupWhy)
	}
	return exitOK
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

const verifyUsage = "usage: homesteadctl updates verify --component NAME --artifact FILE --checksums FILE --signature FILE [--allow-older]"

// runUpdatesVerify verifies owner-supplied local release files. Files
// resolve to absolute paths because the daemon reads them on the
// appliance. A failed verification exits non-zero.
func runUpdatesVerify(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	values := map[string]string{}
	allowOlder := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--allow-older":
			allowOlder = true
		case "--component", "--artifact", "--checksums", "--signature":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, verifyUsage)
				return exitUsage
			}
			values[args[i]] = args[i+1]
			i++
		default:
			fmt.Fprintf(stderr, "unknown argument %q\n%s\n", args[i], verifyUsage)
			return exitUsage
		}
	}
	for _, flag := range []string{"--component", "--artifact", "--checksums", "--signature"} {
		if values[flag] == "" {
			fmt.Fprintln(stderr, verifyUsage)
			return exitUsage
		}
	}
	abs := map[string]string{}
	for _, flag := range []string{"--artifact", "--checksums", "--signature"} {
		path, err := filepath.Abs(values[flag])
		if err != nil {
			return fail(stderr, err)
		}
		abs[flag] = path
	}
	rel, err := client.PlatformVerify(ctx, values["--component"], abs["--artifact"], abs["--checksums"], abs["--signature"], allowOlder)
	if err != nil {
		return fail(stderr, err)
	}
	if rel.State != "verified" {
		fmt.Fprintf(stderr, "verification failed: %s\n", rel.Detail)
		if rel.Refusal == "rollback" {
			fmt.Fprintf(stderr, "release %s (counter %d) is older than the installed release; re-run with --allow-older to override (audited)\n", rel.Version, rel.Counter)
		}
		return exitError
	}
	fmt.Fprintf(stdout, "%s %s (counter %d): verified with key %s\n", rel.Component, rel.Version, rel.Counter, rel.KeyID)
	if rel.Override {
		fmt.Fprintln(stdout, "older release allowed by owner override (audited)")
	}
	return exitOK
}

const trustUsage = "usage: homesteadctl updates trust <list|add|replace|remove> [KEY_ID] [--key-file FILE] [--label TEXT]"

// runUpdatesTrust manages trusted release-signing keys over the API.
func runUpdatesTrust(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, trustUsage)
		return exitUsage
	}
	action := args[0]
	var keyFile, label string
	var positional []string
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--key-file" && i+1 < len(args):
			keyFile = args[i+1]
			i++
		case args[i] == "--label" && i+1 < len(args):
			label = args[i+1]
			i++
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			positional = append(positional, args[i])
		}
	}
	readKey := func() (string, bool) {
		if keyFile == "" {
			fmt.Fprintln(stderr, trustUsage)
			return "", false
		}
		data, err := os.ReadFile(keyFile)
		if err != nil {
			fail(stderr, fmt.Errorf("read %s: %w", keyFile, err))
			return "", false
		}
		return string(data), true
	}
	switch action {
	case "list":
		keys, err := client.TrustKeys(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		for _, key := range keys {
			mark := ""
			if key.Placeholder {
				mark = "  [PLACEHOLDER: verifies no real release]"
			}
			fmt.Fprintf(stdout, "%s  %s  %s%s\n", key.KeyID, key.Source, key.Label, mark)
		}
		return exitOK
	case "add":
		text, ok := readKey()
		if !ok || len(positional) != 0 {
			return exitUsage
		}
		key, err := client.TrustKeyAdd(ctx, text, label)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "trusted key %s\n", key.KeyID)
		return exitOK
	case "replace":
		if len(positional) != 1 {
			fmt.Fprintln(stderr, trustUsage)
			return exitUsage
		}
		text, ok := readKey()
		if !ok {
			return exitUsage
		}
		key, err := client.TrustKeyReplace(ctx, positional[0], text, label)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "trusted key %s replaced by %s\n", positional[0], key.KeyID)
		return exitOK
	case "remove":
		if len(positional) != 1 {
			fmt.Fprintln(stderr, trustUsage)
			return exitUsage
		}
		if err := client.TrustKeyRemove(ctx, positional[0]); err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "removed trusted key %s\n", positional[0])
		return exitOK
	}
	fmt.Fprintf(stderr, "unknown trust subcommand %q\n%s\n", action, trustUsage)
	return exitUsage
}

const applyPlatformUsage = "usage: homesteadctl updates apply-platform --component NAME --artifact FILE [--allow-older] [--follow]"

// Poll cadence and bound for following an apply across the daemon
// restart it causes; variables so tests can shorten them.
var (
	applyFollowPoll    = 2 * time.Second
	applyFollowTimeout = 5 * time.Minute
)

// runUpdatesApplyPlatform applies a verified platform release (S265):
// the artifact must be the file `updates verify` verified, which the
// daemon re-checks against the recorded digest. Applying the daemon
// restarts it; --follow rides out the restart and reports the
// finalized outcome. --allow-older is the explicit, audited override
// for an older counter.
func runUpdatesApplyPlatform(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	values := map[string]string{}
	allowOlder, follow := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--allow-older":
			allowOlder = true
		case "--follow":
			follow = true
		case "--component", "--artifact":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, applyPlatformUsage)
				return exitUsage
			}
			values[args[i]] = args[i+1]
			i++
		default:
			fmt.Fprintf(stderr, "unknown argument %q\n%s\n", args[i], applyPlatformUsage)
			return exitUsage
		}
	}
	if values["--component"] == "" || values["--artifact"] == "" {
		fmt.Fprintln(stderr, applyPlatformUsage)
		return exitUsage
	}
	artifact, err := filepath.Abs(values["--artifact"])
	if err != nil {
		return fail(stderr, err)
	}
	id, err := client.PlatformApply(ctx, values["--component"], artifact, allowOlder)
	if err != nil {
		if strings.Contains(err.Error(), "older") && !allowOlder {
			fmt.Fprintln(stderr, "re-run with --allow-older to apply an older release (audited with both versions)")
		}
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "operation: %d\n", id)
	if !follow {
		return exitOK
	}
	last, err := followAcrossRestart(ctx, client, id, stdout)
	if err != nil {
		if ctx.Err() != nil {
			return exitOK
		}
		return fail(stderr, err)
	}
	if last.Status != operation.StatusSucceeded {
		if last.Error != "" {
			fmt.Fprintln(stderr, last.Error)
		}
		return exitError
	}
	if values["--component"] == "homestead-server" {
		fmt.Fprintln(stdout, "daemon restarted and passed its health check")
	}
	return exitOK
}

// followAcrossRestart streams the operation and, when the stream ends
// because the daemon is restarting, polls until the new process
// finalizes the record (or the bound passes).
func followAcrossRestart(ctx context.Context, client *apiclient.Client, id int64, stdout io.Writer) (operation.Operation, error) {
	var last operation.Operation
	report := func(op operation.Operation) {
		if op.Status != last.Status || stepSummary(op) != stepSummary(last) {
			fmt.Fprintf(stdout, "%s: %s\n", op.Status, stepSummary(op))
		}
		last = op
	}
	_ = client.StreamOperation(ctx, id, report)
	deadline := time.Now().Add(applyFollowTimeout)
	for !operation.Terminal(last.Status) {
		if time.Now().After(deadline) {
			return last, fmt.Errorf("operation %d did not finish within %s; check `homesteadctl operations get %d`", id, applyFollowTimeout, id)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(applyFollowPoll):
		}
		// The daemon is down while it restarts: errors only mean wait.
		if op, err := client.GetOperation(ctx, id); err == nil {
			report(op)
		}
	}
	return last, nil
}
