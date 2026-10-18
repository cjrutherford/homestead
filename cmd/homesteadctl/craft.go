package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
)

const craftUsage = "usage: homesteadctl craft <status|apps|plan|build|trust|files> ..."

const craftBuildUsage = "usage: homesteadctl craft <plan|build> APP [--manifest FILE --signature FILE] [--artifact FILE] [--allow-older] [--follow]"

const craftTrustUsage = "usage: homesteadctl craft trust <list|add|replace|remove> [KEY_ID] [--key-file FILE] [--label TEXT]"

// runCraft drives the craft build pipeline (S279, ADR-028): status of
// recorded local builds, a verify-only plan, the verify-then-build
// operation, and the separate craft signing key class.
func runCraft(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, craftUsage)
		return exitUsage
	}
	switch args[0] {
	case "status":
		return runCraftStatus(ctx, client, stdout, stderr)
	case "apps":
		return runCraftApps(ctx, client, stdout, stderr)
	case "plan":
		return runCraftBuild(ctx, client, args[1:], false, stdout, stderr)
	case "build":
		return runCraftBuild(ctx, client, args[1:], true, stdout, stderr)
	case "files":
		return runCraftFiles(ctx, client, args[1:], stdout, stderr)
	case "trust":
		return runTrust(ctx, args[1:], stdout, stderr, trustOps{usage: craftTrustUsage,
			list: client.CraftTrustKeys, add: client.CraftTrustKeyAdd, replace: client.CraftTrustKeyReplace, remove: client.CraftTrustKeyRemove})
	}
	fmt.Fprintf(stderr, "unknown craft subcommand %q\n%s\n", args[0], craftUsage)
	return exitUsage
}

func runCraftStatus(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	st, err := client.CraftStatus(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "manifest: %s\ncontext: %s\n", st.ManifestPath, st.ContextDir)
	if len(st.Images) == 0 {
		fmt.Fprintln(stdout, "no craft images built")
	}
	for _, img := range st.Images {
		state := "matches recorded digest"
		switch {
		case !img.Present:
			state = "MISSING locally"
		case !img.Match:
			state = "DIFFERS from recorded digest (" + img.ImageID + ")"
		}
		fmt.Fprintf(stdout, "%s  %s  counter %d  %s\n", img.Tag, img.ImageID, img.Counter, state)
	}
	for _, v := range st.Verdicts {
		line := fmt.Sprintf("%s %s: %s", v.App, v.Version, v.State)
		if v.Refusal != "" {
			line += " (" + v.Refusal + " check)"
		}
		fmt.Fprintf(stdout, "%s: %s\n", line, v.Detail)
	}
	return exitOK
}

// runCraftApps prints the start page (S281, ADR-031): installed craft apps
// with title, version, status and local URL. An app with no route says why.
func runCraftApps(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	apps, err := client.CraftApps(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if len(apps) == 0 {
		fmt.Fprintln(stdout, "no craft apps installed")
		return exitOK
	}
	for _, app := range apps {
		url := app.URL
		if url == "" {
			url = "(no URL) " + app.URLReason
		}
		fmt.Fprintf(stdout, "%s  %s  %s  %s  %s\n", app.Name, app.Title, app.Version, app.Status, url)
	}
	return exitOK
}

func runCraftBuild(ctx context.Context, client *apiclient.Client, args []string, build bool, stdout, stderr io.Writer) int {
	var req apiclient.CraftRequest
	follow := false
	var positional []string
	abs := func(flag, value string) (string, bool) {
		path, err := filepath.Abs(value)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", flag, err)
			return "", false
		}
		return path, true
	}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--allow-older":
			req.AllowOlder = true
		case args[i] == "--follow":
			follow = true
		case (args[i] == "--manifest" || args[i] == "--signature" || args[i] == "--artifact" || args[i] == "--version") && i+1 < len(args):
			value := args[i+1]
			if args[i] != "--version" {
				var ok bool
				if value, ok = abs(args[i], value); !ok {
					return exitUsage
				}
			}
			switch args[i] {
			case "--manifest":
				req.Manifest = value
			case "--signature":
				req.Signature = value
			case "--artifact":
				req.Artifact = value
			case "--version":
				req.Version = value
			}
			i++
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, craftBuildUsage)
		return exitUsage
	}
	req.App = positional[0]
	if !build {
		res, err := client.CraftPlan(ctx, req)
		if err != nil {
			return fail(stderr, err)
		}
		if res.Verdict.State != "verified" {
			fmt.Fprintf(stderr, "refused (%s check): %s\n", res.Verdict.Refusal, res.Verdict.Detail)
			if res.Verdict.Refusal == "counter" {
				fmt.Fprintln(stderr, "the manifest is older than the recorded build; re-run with --allow-older to override (audited)")
			}
			return exitError
		}
		p := res.Plan
		fmt.Fprintf(stdout, "%s %s (counter %d) verified with key %s\n  tag: %s\n  artifact: %s (%d bytes, sha256 %s, %s)\n  build context: %s\n  disk needed: %d MiB\n",
			p.App, p.Version, p.Counter, p.KeyID, p.Tag, p.ArtifactName, p.ArtifactSize, p.ArtifactSHA256, p.Source, p.ContextDigest, p.DiskRequired>>20)
		if p.Override {
			fmt.Fprintf(stdout, "  older manifest allowed by owner override (recorded %s); audited\n", p.Baseline)
		}
		return exitOK
	}
	id, err := client.CraftBuild(ctx, req)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "operation: %d\n", id)
	if !follow {
		return exitOK
	}
	var last operation.Operation
	if err := client.StreamOperation(ctx, id, func(op operation.Operation) {
		last = op
		fmt.Fprintf(stdout, "%s: %s\n", op.Status, stepSummary(op))
	}); err != nil {
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
	return exitOK
}
