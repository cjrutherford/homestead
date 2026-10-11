package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/operation"
)

// runApps dispatches the catalog group (S168): list curated packages,
// review install plans, execute approved installs, and configure
// services — the dashboard flow (S167) in CLI form over the same
// endpoints.
func runApps(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl apps <list|plan|install|configure|uninstall>")
		return exitUsage
	}
	switch args[0] {
	case "list":
		return runAppsList(ctx, client, stdout, stderr)
	case "plan":
		return runAppsPlan(ctx, client, args[1:], stdout, stderr)
	case "install":
		return runAppsInstall(ctx, client, args[1:], stdout, stderr)
	case "configure":
		return runAppsConfigure(ctx, client, args[1:], stdout, stderr)
	case "uninstall":
		return runAppsUninstall(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown apps subcommand %q\n", args[0])
		return exitUsage
	}
}

func runAppsList(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	packages, err := client.CatalogPackages(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if len(packages) == 0 {
		fmt.Fprintln(stdout, "no packages curated yet")
		return exitOK
	}
	for _, pkg := range packages {
		fmt.Fprintf(stdout, "%s %s — %s\n", pkg.Name, pkg.Version, pkg.Title)
	}
	return exitOK
}

// inputFile scans -f FILE from any position with one positional left
// over: the standard flag package stops at the first positional,
// which would force flags-before-package ordering on every caller.
func inputFile(args []string, stderr io.Writer) (string, []string, bool) {
	var file string
	var positional []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-f" && i+1 < len(args) {
			file = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return "", nil, false
		}
		positional = append(positional, args[i])
	}
	if file == "" {
		return "", nil, false
	}
	return file, positional, true
}

func appsInputFile(args []string, stderr io.Writer) (string, []string, bool) {
	return inputFile(args, stderr)
}

func runAppsPlan(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	file, rest, ok := appsInputFile(args, stderr)
	if !ok {
		return exitUsage
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl apps plan <package> -f FILE")
		return exitUsage
	}
	body, err := loadSettingsFile(file)
	if err != nil {
		return fail(stderr, err)
	}
	plan, err := client.PlanInstall(ctx, rest[0], body)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "package: %s (requirements met: %v)\n", plan.Package, plan.Requirements.Passed)
	for _, route := range plan.Routes {
		fmt.Fprintf(stdout, "route: %s (%s)\n", route.Host, route.Exposure)
	}
	for _, record := range plan.DNS {
		fmt.Fprintf(stdout, "dns: %s %s %s\n", record.Name, record.Type, record.Value)
	}
	for _, tls := range plan.TLS {
		fmt.Fprintf(stdout, "tls: %s (%s)\n", tls.Host, tls.Issuer)
	}
	for _, policy := range plan.Firewall {
		fmt.Fprintf(stdout, "firewall: %s\n", policy.Service.Name)
	}
	for _, dir := range plan.Directories {
		fmt.Fprintf(stdout, "directory: %s\n", dir.Mountpoint)
	}
	fmt.Fprintf(stdout, "actions: %s\n", strings.Join(plan.Actions, ", "))
	return exitOK
}

func runAppsInstall(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var file string
	var positional []string
	follow := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-f" && i+1 < len(args):
			file = args[i+1]
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
	if file == "" || len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl apps install <package> -f FILE [--follow]")
		return exitUsage
	}
	body, err := loadSettingsFile(file)
	if err != nil {
		return fail(stderr, err)
	}
	id, err := client.InstallPackage(ctx, positional[0], body)
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
	switch last.Status {
	case operation.StatusSucceeded:
		return exitOK
	default:
		return exitError
	}
}

// Flag parsing scans manually throughout this group: the standard
// flag package stops at the first positional, which would force
// flags-before-package ordering on every caller.

func runAppsConfigure(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var service string
	var sets, unsets []string
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--service" && i+1 < len(args):
			service = args[i+1]
			i++
		case args[i] == "--set" && i+1 < len(args):
			sets = append(sets, args[i+1])
			i++
		case args[i] == "--unset" && i+1 < len(args):
			unsets = append(unsets, args[i+1])
			i++
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			positional = append(positional, args[i])
		}
	}
	if service == "" || len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl apps configure <package> --service NAME [--set KEY=VALUE] [--unset KEY]")
		return exitUsage
	}
	entry := map[string]any{"Service": service}
	setMap := map[string]any{}
	for _, pair := range sets {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			fmt.Fprintf(stderr, "invalid --set %q (want KEY=VALUE)\n", pair)
			return exitUsage
		}
		setMap[key] = value
	}
	entry["Set"] = setMap
	var unsetList []string
	for _, key := range unsets {
		if key == "" {
			fmt.Fprintln(stderr, "invalid --unset (want KEY)")
			return exitUsage
		}
		unsetList = append(unsetList, key)
	}
	if len(unsetList) > 0 {
		entry["Unset"] = unsetList
	}
	id, err := client.ConfigurePackage(ctx, positional[0], []map[string]any{entry})
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "operation: %d\n", id)
	return exitOK
}

func runAppsUninstall(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var dataDirs []string
	removeData := false
	follow := false
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--remove-data":
			removeData = true
		case args[i] == "--data-dir" && i+1 < len(args):
			dataDirs = append(dataDirs, args[i+1])
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
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl apps uninstall <package> [--remove-data --data-dir MOUNT:PATH]... [--follow]")
		return exitUsage
	}
	body := map[string]any{"remove_data": removeData}
	var dirs []map[string]any
	for _, dir := range dataDirs {
		mountpoint, path, found := strings.Cut(dir, ":")
		if !found || mountpoint == "" || path == "" {
			fmt.Fprintln(stderr, "invalid --data-dir (want MOUNT:PATH)")
			return exitUsage
		}
		dirs = append(dirs, map[string]any{"mountpoint": mountpoint, "path": path})
	}
	if dirs != nil {
		body["data_dirs"] = dirs
	}
	id, err := client.UninstallPackage(ctx, positional[0], body)
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
	switch last.Status {
	case operation.StatusSucceeded:
		return exitOK
	default:
		return exitError
	}
}
