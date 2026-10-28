package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/docker"
	"homestead/internal/operation"
)

// runApps dispatches the catalog group (S168): list curated packages,
// review install plans, execute approved installs, and configure
// services — the dashboard flow (S167) in CLI form over the same
// endpoints.
func runApps(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl apps <list|names|plan|install|reach|configure|secrets|sign-in|public|move|setup|uninstall>")
		return exitUsage
	}
	switch args[0] {
	case "list":
		return runAppsList(ctx, client, stdout, stderr)
	case "names":
		return runAppsNames(ctx, client, stdout, stderr)
	case "plan":
		return runAppsPlan(ctx, client, args[1:], stdout, stderr)
	case "install":
		return runAppsInstall(ctx, client, args[1:], stdout, stderr)
	case "reach":
		return runAppsReach(ctx, client, args[1:], stdout, stderr)
	case "configure":
		return runAppsConfigure(ctx, client, args[1:], stdout, stderr)
	case "secrets":
		return runAppsSecrets(ctx, client, args[1:], stdout, stderr)
	case "sign-in":
		return runAppsSignIn(ctx, client, args[1:], stdout, stderr)
	case "public":
		return runAppsPublic(ctx, client, args[1:], stdout, stderr)
	case "setup":
		return runAppsSetup(ctx, client, args[1:], stdout, stderr)
	case "move":
		return runAppsMove(ctx, client, args[1:], stdout, stderr)
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
		if route.Path != "" {
			fmt.Fprintf(stdout, "route: %s at %s (%s)\n", route.Host, route.Path, route.Exposure)
			continue
		}
		fmt.Fprintf(stdout, "route: %s (%s)\n", route.Host, route.Exposure)
	}
	printInstallAddresses(stdout, plan.Addresses)
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
	for _, mount := range plan.Mounts {
		fmt.Fprintf(stdout, "mount: %s\n", formatMount(mount.Service, mount.Target, mount.Source,
			mount.Class, mount.Database, mount.Ephemeral, mount.ReadOnly, mount.Declared))
	}
	for _, advisory := range plan.BrowserAdvisories {
		fmt.Fprintf(stdout, "browser: %s [%s] (advisory) - %s\n", advisory.Label, advisory.Capability, advisory.Reason)
	}
	for _, note := range plan.Notes {
		fmt.Fprintf(stdout, "note: %s\n", note)
	}
	fmt.Fprintf(stdout, "actions: %s\n", strings.Join(plan.Actions, ", "))
	return exitOK
}

// formatMount renders one planned mount: service target <- host path,
// with class and flags, so owners review where data lands (S276).
func formatMount(service, target, source, class string, database, ephemeral, readOnly, declared bool) string {
	if ephemeral {
		return fmt.Sprintf("%s %s <- tmpfs (ephemeral)", service, target)
	}
	flags := []string{class}
	if database {
		flags = append(flags, "database")
	}
	if readOnly {
		flags = append(flags, "read-only")
	}
	if !declared {
		flags = append(flags, "owner")
	}
	return fmt.Sprintf("%s %s <- %s (%s)", service, target, source, strings.Join(flags, ", "))
}

const appsInstallUsage = `usage: homesteadctl apps install <package> -f FILE [--follow]
       homesteadctl apps install <package> -f FILE --public [NAME] [--approve-public DIGEST]
  --public [NAME]          also publish the app at NAME through the tunnel
                           (default <package>.<domain>); without
                           --approve-public this plans the install and the
                           public address, prints both digests and changes nothing
  --approve-public DIGEST  install; the appliance then adds the app's address
                           and publishes it by the reviewed digest (follows
                           the install and the setup)`

func runAppsInstall(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var file, publicName, approvePublic string
	var positional []string
	follow, public := false, false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-f" && i+1 < len(args):
			file = args[i+1]
			i++
		case args[i] == "--follow":
			follow = true
		case args[i] == "--public":
			public = true
			// The name is optional; a following word with a dot is it.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && strings.Contains(args[i+1], ".") {
				publicName = strings.ToLower(args[i+1])
				i++
			}
		case args[i] == "--approve-public" && i+1 < len(args):
			approvePublic = args[i+1]
			i++
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			positional = append(positional, args[i])
		}
	}
	if file == "" || len(positional) != 1 || (approvePublic != "" && !public) {
		fmt.Fprintln(stderr, appsInstallUsage)
		return exitUsage
	}
	body, err := loadSettingsFile(file)
	if err != nil {
		return fail(stderr, err)
	}
	pkg := positional[0]
	if public {
		return installWithPublic(ctx, client, pkg, file, body, publicName, approvePublic, stdout, stderr)
	}
	id, err := client.InstallPackage(ctx, pkg, body)
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

// installWithPublic is `apps install --public` (S313). Without approval
// it plans the install and the public address and prints both digests;
// nothing changes. With --approve-public it re-plans, refuses a changed
// public plan, installs by the install digest, adds the app's address to
// the edge, publishes by the approved digest, and checks each address.
func installWithPublic(ctx context.Context, client *apiclient.Client, pkg, file string, body map[string]any,
	name, approve string, stdout, stderr io.Writer) int {
	body["public_name"] = name
	install, err := client.PlanInstall(ctx, pkg, body)
	if err != nil {
		return fail(stderr, err)
	}
	route := firstLocalRoute(install.Routes)
	if route == nil {
		return fail(stderr, fmt.Errorf("%s has no address to publish", pkg))
	}
	enabled := true
	req := apiclient.AppPublicRequest{Enabled: &enabled, Hostname: name, Route: string(route.Host), BeforeInstall: true}
	plan, err := client.AppPublicPlan(ctx, pkg, req)
	if err != nil {
		return fail(stderr, err)
	}
	if name == "" && plan.Hostname != "" {
		// The sign-in preview and the install digest follow the proposed name.
		body["public_name"] = plan.Hostname
		if install, err = client.PlanInstall(ctx, pkg, body); err != nil {
			return fail(stderr, err)
		}
	}
	if approve == "" {
		at := routeHosts(install.Routes)
		if len(install.Addresses) > 0 {
			at = install.Addresses
		}
		fmt.Fprintf(stdout, "install: %s at %s\n", install.Package, strings.Join(at, ", "))
		fmt.Fprintf(stdout, "install digest: %s\n", install.Digest)
		printPublicBeforeInstall(stdout, plan)
		named := " " + plan.Hostname
		if plan.Path != "" {
			// A path app has no name of its own to pass (ADR-044).
			named = ""
		}
		fmt.Fprintf(stdout, "approve: homesteadctl apps install %s -f %s --public%s --approve-public %s\n", pkg, file, named, plan.Digest)
		return exitOK
	}
	if approve != plan.Digest {
		fmt.Fprintln(stderr, "the public address plan changed since it was reviewed; run without --approve-public and review it again")
		return exitError
	}
	delete(body, "public_name")
	body["public_name"] = plan.Hostname
	body["digest"] = install.Digest
	// The appliance adds the app's address and publishes it by the
	// approved digest once the install succeeds (S324, ADR-048).
	body["setup"] = map[string]any{"public": map[string]any{"hostname": plan.Hostname, "route": string(route.Host), "digest": approve}}
	id, err := client.InstallPackage(ctx, pkg, body)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, "install:")
	if code := followOperation(ctx, client, id, stdout, stderr); code != exitOK {
		return code
	}
	setup, err := client.AppSetupStatus(ctx, pkg)
	if err != nil {
		return fail(stderr, fmt.Errorf("the app is installed; read its setup with homesteadctl apps setup status %s: %w", pkg, err))
	}
	if setup.Operation != 0 && (setup.Status == "running" || setup.Status == "waiting") {
		fmt.Fprintln(stdout, "setup (address, then public address):")
		_ = followOperation(ctx, client, setup.Operation, stdout, stderr)
		if setup, err = client.AppSetupStatus(ctx, pkg); err != nil {
			return fail(stderr, err)
		}
	}
	if setup.Status == "stopped" {
		printAppSetup(stdout, setup)
		return fail(stderr, fmt.Errorf("the app is installed; its setup stopped, so nothing more was published: review it with homesteadctl apps setup status %s", pkg))
	}
	printReach(ctx, client, pkg, stdout)
	return exitOK
}

// firstLocalRoute is the planned route the public name serves: the first
// that is not itself public.
func firstLocalRoute(routes []docker.Route) *docker.Route {
	for i := range routes {
		if routes[i].Exposure != docker.ExposurePublic {
			return &routes[i]
		}
	}
	return nil
}

func routeHosts(routes []docker.Route) []string {
	var out []string
	for _, r := range routes {
		out = append(out, string(r.Host))
	}
	return out
}

// printInstallAddresses prints where the app will answer locally; the
// .lan names need the appliance resolver or a router entry (ADR-039).
func printInstallAddresses(w io.Writer, addresses []string) {
	lan := false
	for _, a := range addresses {
		fmt.Fprintf(w, "address: %s\n", a)
		lan = lan || strings.Contains(a, ".lan/")
	}
	if lan {
		fmt.Fprintln(w, "note: .lan names answer on devices that use the appliance's name resolver (homesteadctl names) or a router entry; .local works on most devices without one")
	}
}

func printPublicBeforeInstall(w io.Writer, p apiclient.AppPublicPlan) {
	if p.Path != "" {
		fmt.Fprintf(w, "public: %s (the dashboard's public name, tunnel %s)\n", p.URL, p.Tunnel)
	} else {
		fmt.Fprintf(w, "public: https://%s -> %s (tunnel %s)\n", p.Hostname, p.Route, p.Tunnel)
	}
	for _, c := range p.DNS {
		fmt.Fprintf(w, "dns %s: %s %s -> %s\n", c.Action, c.Type, c.Name, c.Content)
		for _, old := range c.Replaces {
			fmt.Fprintf(w, "  deletes %s %s -> %s\n", old.Type, old.Name, old.Content)
		}
	}
	fmt.Fprintf(w, "connector after: %s\n", strings.Join(p.After, ", "))
	for _, s := range p.AppSettings {
		fmt.Fprintf(w, "app setting: %s\n", s)
	}
	for _, s := range p.Warnings {
		fmt.Fprintf(w, "warning: %s\n", s)
	}
	if p.SignIn != "" {
		fmt.Fprintf(w, "sign-in: %s\n", p.SignIn)
	}
	fmt.Fprintf(w, "public digest: %s\n", p.Digest)
}

// printReach prints whether each address answers. It is advisory: an
// address that does not answer yet never fails the command.
func printReach(ctx context.Context, client *apiclient.Client, pkg string, w io.Writer) {
	reach, err := client.AppReach(ctx, pkg)
	if err != nil {
		fmt.Fprintf(w, "reach: could not check (%v)\n", err)
		return
	}
	for _, a := range reach.Addresses {
		if a.Answers {
			fmt.Fprintf(w, "%s: answers\n", a.Address)
		} else {
			fmt.Fprintf(w, "%s: does not answer yet\n", a.Address)
		}
		if a.Reason != "" {
			fmt.Fprintf(w, "  %s\n", a.Reason)
		}
		if a.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", a.Fix)
		}
	}
}

// runAppsReach is `apps reach <package>` (S313).
func runAppsReach(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: homesteadctl apps reach <package>")
		return exitUsage
	}
	printReach(ctx, client, args[0], stdout)
	return exitOK
}

// Flag parsing scans manually throughout this group: the standard
// flag package stops at the first positional, which would force
// flags-before-package ordering on every caller.

func runAppsConfigure(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var service string
	var sets, unsets, rotates []string
	var positional []string
	adopt := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--adopt-secrets":
			adopt = true
		case args[i] == "--rotate-secret" && i+1 < len(args):
			rotates = append(rotates, args[i+1])
			i++
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
	if len(positional) == 1 && adopt && service == "" && len(rotates) == 0 && len(sets) == 0 && len(unsets) == 0 {
		id, err := client.AdoptPackageSecrets(ctx, positional[0])
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	if adopt || len(positional) != 1 || (service == "" && len(rotates) == 0) {
		fmt.Fprintln(stderr, "usage: homesteadctl apps configure <package> --service NAME [--set KEY=VALUE] [--unset KEY] | --rotate-secret NAME (one) | --adopt-secrets")
		return exitUsage
	}
	if service == "" && (len(sets) > 0 || len(unsets) > 0) {
		fmt.Fprintln(stderr, "--set and --unset need --service")
		return exitUsage
	}
	if service == "" {
		id, err := client.RotatePackageSecrets(ctx, positional[0], rotates)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
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
	var id int64
	var err error
	if len(rotates) > 0 {
		id, err = client.ConfigurePackageRotating(ctx, positional[0], []map[string]any{entry}, rotates)
	} else {
		id, err = client.ConfigurePackage(ctx, positional[0], []map[string]any{entry})
	}
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "operation: %d\n", id)
	return exitOK
}

// runAppsSecrets lists a package's secret-backed variables, or registers
// an existing value (S266):
//
//	apps secrets <pkg>
//	apps secrets register <pkg> <secret> --value-file PATH|- [--skip-verify] [--force]
//
// The value comes from a file or stdin, never argv, so it stays out of
// shell history and process listings.
func runAppsSecrets(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) >= 1 && args[0] == "register" {
		return runAppsSecretRegister(ctx, client, args[1:], stdout, stderr)
	}
	if len(args) >= 1 && args[0] == "reveal" {
		return runAppsSecretReveal(ctx, client, args[1:], stdout, stderr)
	}
	if len(args) != 1 {
		fmt.Fprintln(stderr, appsSecretsUsage)
		return exitUsage
	}
	secrets, err := client.AppSecrets(ctx, args[0])
	if err != nil {
		return fail(stderr, err)
	}
	if len(secrets) == 0 {
		fmt.Fprintln(stdout, "no secret-backed variables")
		return exitOK
	}
	for _, s := range secrets {
		state := "missing"
		if s.Present {
			state = "present"
		}
		flags := ""
		if s.Database {
			flags += " database-password"
		}
		if s.Immutable {
			flags += " not-rotatable"
		}
		if s.Installed && !s.Wired {
			flags += " not-wired (pre-S266: register, then apps configure --adopt-secrets)"
		}
		if s.Reveal && s.Present {
			flags += " readable (apps secrets reveal " + args[0] + " " + s.Name + ")"
		}
		services := strings.Join(s.Services, ", ")
		if services == "" {
			services = "none, kept by Homestead"
		}
		fmt.Fprintf(stdout, "%s: %s (%s; services: %s)%s\n", s.Name, state, s.StoreName, services, flags)
		if s.Note != "" {
			fmt.Fprintf(stdout, "  %s\n", s.Note)
		}
	}
	return exitOK
}

const appsSecretsUsage = "usage: homesteadctl apps secrets <package> | register <package> <secret> --value-file PATH|- [--skip-verify] [--force] | reveal <package> <secret>"

// runAppsSecretReveal prints a credential the package marks readable
// (S312), such as Nextcloud's admin password. The value goes to stdout
// alone, so it can be piped; the appliance audits the reveal.
func runAppsSecretReveal(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		fmt.Fprintln(stderr, "usage: homesteadctl apps secrets reveal <package> <secret>")
		return exitUsage
	}
	out, err := client.RevealAppSecret(ctx, args[0], args[1])
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, out.Value)
	if out.Note != "" {
		fmt.Fprintln(stderr, out.Note)
	}
	fmt.Fprintln(stderr, "This reveal is audited.")
	return exitOK
}

// maxSecretValueBytes bounds a registered value read from disk or stdin.
const maxSecretValueBytes = 64 << 10

// readBounded reads at most limit+1 bytes of a file so oversized input
// is detected without slurping it.
func readBounded(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, int64(limit)+1))
}

func runAppsSecretRegister(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var valueFile string
	skip, force := false, false
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--force":
			force = true
		case args[i] == "--value-file" && i+1 < len(args):
			valueFile = args[i+1]
			i++
		case args[i] == "--skip-verify":
			skip = true
		case strings.HasPrefix(args[i], "-") && args[i] != "-":
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			positional = append(positional, args[i])
		}
	}
	if valueFile == "" || len(positional) != 2 {
		fmt.Fprintln(stderr, "usage: homesteadctl apps secrets register <package> <secret> --value-file PATH|- [--skip-verify] [--force]")
		return exitUsage
	}
	var raw []byte
	var err error
	if valueFile == "-" {
		raw, err = io.ReadAll(io.LimitReader(os.Stdin, maxSecretValueBytes+1))
	} else {
		raw, err = readBounded(valueFile, maxSecretValueBytes)
	}
	if err != nil {
		return fail(stderr, err)
	}
	if len(raw) > maxSecretValueBytes {
		fmt.Fprintln(stderr, "secret value exceeds 64 KiB")
		return exitUsage
	}
	value := strings.TrimRight(string(raw), "\r\n")
	if value == "" {
		fmt.Fprintln(stderr, "secret value is empty")
		return exitUsage
	}
	store, err := client.RegisterAppSecret(ctx, positional[0], positional[1], value, skip, force)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "registered: %s\n", store)
	return exitOK
}

func runAppsUninstall(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var dataDirs []string
	removeData := false
	follow := false
	abandon := false
	leaveSignIn := false
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--abandon-volumes":
			abandon = true
		case args[i] == "--leave-sign-in":
			leaveSignIn = true
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
		fmt.Fprintln(stderr, "usage: homesteadctl apps uninstall <package> [--remove-data --data-dir MOUNT:PATH]... [--abandon-volumes] [--leave-sign-in] [--follow]")
		return exitUsage
	}
	body := map[string]any{"remove_data": removeData}
	if abandon {
		body["abandon_volumes"] = true
	}
	if leaveSignIn {
		body["leave_sign_in"] = true
	}
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
