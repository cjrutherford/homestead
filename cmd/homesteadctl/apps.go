package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/appaccess"
	"homestead/internal/docker"
	"homestead/internal/operation"
)

// runApps dispatches the catalog group (S168): list curated packages,
// review install plans, execute approved installs, and configure
// services — the dashboard flow (S167) in CLI form over the same
// endpoints.
func runApps(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl apps <list|show|bundles|mine|names|plan|install|reach|configure|secrets|sign-in|public|move|setup|uninstall>")
		return exitUsage
	}
	switch args[0] {
	case "list":
		return runAppsList(ctx, client, stdout, stderr)
	case "mine":
		return runAppsMine(ctx, client, args[1:], stdout, stderr)
	case "names":
		return runAppsNames(ctx, client, stdout, stderr)
	case "show":
		return runAppsShow(ctx, client, args[1:], stdout, stderr)
	case "plan":
		return runAppsPlan(ctx, client, args[1:], stdout, stderr)
	case "install":
		return runAppsInstall(ctx, client, args[1:], stdout, stderr)
	case "reach":
		return runAppsReach(ctx, client, args[1:], stdout, stderr)
	case "configure":
		return runAppsConfigure(ctx, client, args[1:], stdout, stderr)
	case "bundles":
		return runAppsBundles(ctx, client, stdout, stderr)
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
	printFilesLink(stdout, plan.FilesLink)
	printInstallSignIn(stdout, plan.SignIn)
	if err := printInstallAccess(ctx, client, rest[0], body, plan.Access, stdout, nil); err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "actions: %s\n", strings.Join(plan.Actions, ", "))
	return exitOK
}

func printInstallAccess(ctx context.Context, client *apiclient.Client, pkg string, body map[string]any, access *apiclient.InstallAccess, w io.Writer, digestOut *string) error {
	if access == nil {
		return fmt.Errorf("this appliance did not provide an app access review")
	}
	switch access.Mode {
	case "open":
		fmt.Fprintln(w, "access: this app is open to everyone who can reach it")
		return nil
	case "bootstrap":
		fmt.Fprintln(w, "access: identity provider bootstrap")
		return nil
	case "unavailable":
		return fmt.Errorf("app access unavailable: %s", access.Reason)
	case "managed":
	default:
		return fmt.Errorf("unknown app access mode")
	}
	fmt.Fprintln(w, "access: the appliance owner has full access")
	for _, p := range access.People {
		fmt.Fprintf(w, "person: %d %s (%s), %s\n", p.ID, p.DisplayName, p.Username, p.State)
	}
	if _, ok := body["choices"]; !ok {
		fmt.Fprintln(w, "add a choices array to the input file with person_id and role (none, user, or admin), including [] when no other people exist")
		return nil
	}
	digest, err := client.PlanInstallAccess(ctx, pkg, body)
	if err != nil {
		return err
	}
	data, err := json.Marshal(body["choices"])
	if err != nil {
		return fmt.Errorf("invalid access choices")
	}
	var choices []appaccess.Choice
	if err := json.Unmarshal(data, &choices); err != nil {
		return fmt.Errorf("invalid access choices")
	}
	for _, choice := range choices {
		fmt.Fprintf(w, "access choice: person %d = %s\n", choice.PersonID, choice.Role)
	}
	fmt.Fprintf(w, "access digest: %s\n", digest)
	if digestOut != nil {
		*digestOut = digest
	}
	return nil
}

func setInstallAccess(access *apiclient.InstallAccess, body map[string]any, digest string, setup map[string]any) (bool, error) {
	if access == nil {
		return false, fmt.Errorf("this appliance did not provide an app access review")
	}
	switch access.Mode {
	case "open", "bootstrap":
		if _, supplied := body["choices"]; supplied {
			return false, fmt.Errorf("this app does not accept per-person access choices")
		}
		return false, nil
	case "unavailable":
		return false, fmt.Errorf("app access unavailable: %s", access.Reason)
	case "managed":
	default:
		return false, fmt.Errorf("unknown app access mode")
	}
	if _, legacy := setup["people"]; legacy {
		return false, fmt.Errorf("use explicit choices for every person instead of legacy --person grants")
	}
	raw, ok := body["choices"]
	if !ok || raw == nil {
		return false, fmt.Errorf("include an explicit choices array in the input file")
	}
	if digest == "" {
		return false, fmt.Errorf("--approve-access requires the digest from apps plan")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return false, fmt.Errorf("invalid access choices")
	}
	var choices []appaccess.Choice
	if err := json.Unmarshal(data, &choices); err != nil || choices == nil {
		return false, fmt.Errorf("invalid access choices")
	}
	admin := false
	for _, c := range choices {
		if c.Role == appaccess.Admin {
			admin = true
		}
	}
	setup["access"] = map[string]any{"digest": digest, "choices": choices}
	return admin, nil
}

func printInstallSignIn(w io.Writer, offer *apiclient.InstallSignIn) {
	if offer == nil {
		return
	}
	if !offer.Offered || offer.Plan == nil {
		fmt.Fprintf(w, "sign-in with Authentik unavailable: %s\n", offer.Reason)
		return
	}
	p := offer.Plan
	fmt.Fprintf(w, "sign-in with Authentik: %s; provider %s; application %s\n", p.Title, p.Provider, p.Application)
	for _, line := range p.Changes {
		fmt.Fprintf(w, "sign-in: %s\n", line)
	}
	for _, uri := range p.RedirectURIs {
		fmt.Fprintf(w, "sign-in redirect: %s\n", uri)
	}
	for _, line := range p.AppSettings {
		fmt.Fprintf(w, "sign-in setting: %s\n", line)
	}
	for _, line := range p.Warnings {
		fmt.Fprintf(w, "sign-in note: %s\n", line)
	}
	if p.AdminRoles != "" {
		fmt.Fprintf(w, "sign-in roles: %s\n", p.AdminRoles)
	}
	fmt.Fprintf(w, "sign-in digest: %s\n", p.Digest)
	if !offer.TokenKept {
		fmt.Fprintln(w, "sign-in cannot be set up during install without a retained Authentik API token")
	}
}

func approveInstallSignIn(offer *apiclient.InstallSignIn, digest string, people []int64, setup map[string]any) error {
	if digest == "" {
		if len(people) != 0 {
			return fmt.Errorf("--person requires --approve-sign-in")
		}
		return nil
	}
	if offer == nil || !offer.Offered || offer.Plan == nil {
		return fmt.Errorf("sign-in is not offered for this install; review the install plan")
	}
	if !offer.TokenKept {
		return fmt.Errorf("sign-in during install requires a retained Authentik API token")
	}
	if digest != offer.Plan.Digest {
		return fmt.Errorf("the sign-in plan changed; review the install plan again")
	}
	setup["sign_in"] = map[string]any{"digest": digest}
	if len(people) != 0 {
		setup["people"] = people
	}
	return nil
}

// validateInstallPeople catches invalid selections before starting an install.
// The server must still recheck them when the later grant step runs.
func validateInstallPeople(ctx context.Context, client *apiclient.Client, ids []int64, w io.Writer) error {
	if len(ids) == 0 {
		return nil
	}
	view, err := client.People(ctx)
	if err != nil {
		return fmt.Errorf("review selected people: %w", err)
	}
	if !view.TokenKept || view.ReadError != "" {
		return fmt.Errorf("cannot review selected people; check homesteadctl people list")
	}
	active := map[int64]apiclient.Person{}
	for _, p := range view.People {
		if p.State == "active" {
			active[p.ID] = p
		}
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		p, ok := active[id]
		if !ok {
			return fmt.Errorf("person %d is missing or disabled; review homesteadctl people list", id)
		}
		if seen[id] {
			return fmt.Errorf("person %d was selected more than once", id)
		}
		seen[id] = true
		fmt.Fprintf(w, "grant user access: %s (%s, %d)\n", p.DisplayName, p.Username, id)
	}
	return nil
}

// printFilesLink lists the craft apps the install's setup links to
// Nextcloud for files (S326). It is part of the install's review: the
// install approves it with the rest.
func printFilesLink(w io.Writer, link *apiclient.InstallFilesLink) {
	if link == nil {
		return
	}
	for _, line := range link.Lines {
		fmt.Fprintf(w, "files: %s\n", line)
	}
	how := "with its existing link"
	if link.NewLink {
		how = "as account " + link.Account + ", with an app password Homestead creates"
	}
	fmt.Fprintf(w, "files: into %s/%s %s, after the install\n", link.BaseURL, link.Root, how)
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
                           the install and the setup)
  --approve-sign-in DIGEST approve the Authentik plan shown by apps plan
  --approve-access DIGEST  approve the access choices shown by apps plan
  --reauth-file FILE       local password for an app-administrator grant
  --person ID              grant a person access after sign-in (repeatable)`

func runAppsInstall(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	var file, publicName, approvePublic, approveSignIn, approveAccess, reauthFile string
	var people []int64
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
		case args[i] == "--approve-sign-in" && i+1 < len(args):
			approveSignIn = args[i+1]
			i++
		case args[i] == "--approve-access" && i+1 < len(args):
			approveAccess = args[i+1]
			i++
		case args[i] == "--reauth-file" && i+1 < len(args):
			reauthFile = args[i+1]
			i++
		case args[i] == "--person" && i+1 < len(args):
			id, err := strconv.ParseInt(args[i+1], 10, 64)
			if err != nil || id <= 0 {
				fmt.Fprintln(stderr, "--person requires a positive person ID")
				return exitUsage
			}
			people = append(people, id)
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
		return installWithPublic(ctx, client, pkg, file, body, publicName, approvePublic, approveSignIn, approveAccess, reauthFile, people, stdout, stderr)
	}
	// A craft app, or Nextcloud, brings a Nextcloud link for files; the
	// plan lists it (apps plan) and the install approves it, so the
	// appliance adds the address and links once the install succeeds
	// (S326, ADR-048 §4).
	install, err := client.PlanInstall(ctx, pkg, body)
	if err != nil {
		return fail(stderr, err)
	}
	link := install.FilesLink
	setup := firstLocalRoute(install.Routes) != nil || link != nil || approveSignIn != "" || len(people) != 0 || approveAccess != "" || (install.Access != nil && install.Access.Mode == "managed")
	admin := false
	if setup {
		body["digest"] = install.Digest
		setupBody := map[string]any{}
		if existing, ok := body["setup"]; ok && existing != nil {
			var valid bool
			setupBody, valid = existing.(map[string]any)
			if !valid {
				return fail(stderr, fmt.Errorf("setup must be an object"))
			}
		}
		if link != nil {
			printFilesLink(stdout, link)
			setupBody["files_link"] = map[string]any{"digest": link.Digest}
		}
		if err := approveInstallSignIn(install.SignIn, approveSignIn, people, setupBody); err != nil {
			return fail(stderr, err)
		}
		if err := validateInstallPeople(ctx, client, people, stdout); err != nil {
			return fail(stderr, err)
		}
		admin, err = setInstallAccess(install.Access, body, approveAccess, setupBody)
		if err != nil {
			return fail(stderr, err)
		}
		body["setup"] = setupBody
	}
	if !setup && (install.Access == nil || install.Access.Mode == "unavailable") {
		return fail(stderr, fmt.Errorf("app access is unavailable"))
	}
	password := ""
	if reauthFile != "" {
		password, err = readSecretFile(reauthFile)
		if err != nil {
			return fail(stderr, err)
		}
	}
	var id int64
	if admin {
		id, err = client.InstallPackageWithAdmins(ctx, pkg, body, password)
	} else {
		id, err = client.InstallPackage(ctx, pkg, body)
	}
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		if setup {
			fmt.Fprintf(stdout, "setup: the appliance completes the approved app setup after the install; homesteadctl apps setup status %s\n", pkg)
		}
		return exitOK
	}
	if code := followOperation(ctx, client, id, stdout, stderr); code != exitOK || !setup {
		return code
	}
	return followSetup(ctx, client, pkg, "setup:", stdout, stderr)
}

// followSetup follows the setup the appliance runs after an install and
// fails when it stopped.
func followSetup(ctx context.Context, client *apiclient.Client, pkg, title string, stdout, stderr io.Writer) int {
	setup, err := client.AppSetupStatus(ctx, pkg)
	if err != nil {
		return fail(stderr, fmt.Errorf("the app is installed; read its setup with homesteadctl apps setup status %s: %w", pkg, err))
	}
	if setup.Operation != 0 && (setup.Status == "running" || setup.Status == "waiting") {
		fmt.Fprintln(stdout, title)
		_ = followOperation(ctx, client, setup.Operation, stdout, stderr)
		if setup, err = client.AppSetupStatus(ctx, pkg); err != nil {
			return fail(stderr, err)
		}
	}
	if setup.Status == "stopped" {
		printAppSetup(stdout, setup)
		return fail(stderr, fmt.Errorf("the app is installed; its setup stopped: review it with homesteadctl apps setup status %s", pkg))
	}
	return exitOK
}

// installWithPublic is `apps install --public` (S313). Without approval
// it plans the install and the public address and prints both digests;
// nothing changes. With --approve-public it re-plans, refuses a changed
// public plan, installs by the install digest, adds the app's address to
// the edge, publishes by the approved digest, and checks each address.
func installWithPublic(ctx context.Context, client *apiclient.Client, pkg, file string, body map[string]any,
	name, approve, approveSignIn, approveAccess, reauthFile string, people []int64, stdout, stderr io.Writer) int {
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
	req := apiclient.AppPublicRequest{Enabled: &enabled, Hostname: name, Route: string(route.Host), BeforeInstall: true,
		AnticipateSignIn: install.Access != nil && install.Access.Mode == "managed"}
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
		printFilesLink(stdout, install.FilesLink)
		printInstallSignIn(stdout, install.SignIn)
		accessDigest := ""
		if err := printInstallAccess(ctx, client, pkg, body, install.Access, stdout, &accessDigest); err != nil {
			return fail(stderr, err)
		}
		named := " " + plan.Hostname
		if plan.Path != "" {
			// A path app has no name of its own to pass (ADR-044).
			named = ""
		}
		fmt.Fprintf(stdout, "approve: homesteadctl apps install %s -f %s --public%s --approve-public %s", pkg, file, named, plan.Digest)
		if si := install.SignIn; si != nil && si.Offered && si.TokenKept && si.Plan != nil {
			fmt.Fprintf(stdout, " --approve-sign-in %s", si.Plan.Digest)
		}
		if accessDigest != "" {
			fmt.Fprintf(stdout, " --approve-access %s", accessDigest)
		}
		fmt.Fprintln(stdout)
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
	setupBody := map[string]any{"public": map[string]any{"hostname": plan.Hostname, "route": string(route.Host), "digest": approve}}
	if existing, ok := body["setup"]; ok && existing != nil {
		prior, valid := existing.(map[string]any)
		if !valid {
			return fail(stderr, fmt.Errorf("setup must be an object"))
		}
		for k, v := range prior {
			if k != "public" {
				setupBody[k] = v
			}
		}
	}
	if err := approveInstallSignIn(install.SignIn, approveSignIn, people, setupBody); err != nil {
		return fail(stderr, err)
	}
	if err := validateInstallPeople(ctx, client, people, stdout); err != nil {
		return fail(stderr, err)
	}
	admin, err := setInstallAccess(install.Access, body, approveAccess, setupBody)
	if err != nil {
		return fail(stderr, err)
	}
	if install.FilesLink != nil {
		// The Nextcloud link the install review listed (S326).
		setupBody["files_link"] = map[string]any{"digest": install.FilesLink.Digest}
	}
	body["setup"] = setupBody
	password := ""
	if reauthFile != "" {
		password, err = readSecretFile(reauthFile)
		if err != nil {
			return fail(stderr, err)
		}
	}
	var id int64
	if admin {
		id, err = client.InstallPackageWithAdmins(ctx, pkg, body, password)
	} else {
		id, err = client.InstallPackage(ctx, pkg, body)
	}
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

// runAppsShow prints one package's summary and its declared integrations
// (S341). The lines are advisory: they say what the app can work with,
// never that Homestead has configured it.
func runAppsShow(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl apps show <package>")
		return exitUsage
	}
	manifest, err := client.CatalogPackage(ctx, args[0])
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s %s — %s\n", manifest.Name, manifest.Version, manifest.Blurb)
	for _, service := range manifest.Services {
		if service.Exposes != "" && service.ContainerPort != 0 {
			fmt.Fprintf(stdout, "  container port: %s %d\n", service.Name, service.ContainerPort)
		}
	}
	for _, integration := range manifest.Works {
		fmt.Fprintf(stdout, "  integrates: %s %s %s (%s) — %s\n",
			integration.Level, integration.Kind, integration.Target, integration.Title, integration.Note)
	}
	return exitOK
}

// runAppsBundles prints the curated profiles with their combined memory
// and storage verdicts (S344). Members the catalog does not carry yet are
// named, so an incomplete profile never reads as installable today.
func runAppsBundles(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	bundles, err := client.CatalogBundles(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	for _, bundle := range bundles {
		fmt.Fprintf(stdout, "%s: %s\n", bundle.Title, bundle.Blurb)
		fmt.Fprintf(stdout, "  %s\n", bundle.Detail)
		for _, storageRow := range bundle.Storage {
			state := fmt.Sprintf("%s of %s free", humanBytes(storageRow.Needed), humanBytes(storageRow.Free))
			if !storageRow.Known {
				state = fmt.Sprintf("%s needed; no assignment for this class", humanBytes(storageRow.Needed))
			}
			fmt.Fprintf(stdout, "  storage %s: %s\n", storageRow.Class, state)
		}
		if len(bundle.Missing) > 0 {
			fmt.Fprintf(stdout, "  not in the catalog yet: %s\n", strings.Join(bundle.Missing, ", "))
		}
	}
	return exitOK
}

// humanBytes renders a byte count in decimal units: owners read
// capacities, not integers.
func humanBytes(bytes uint64) string {
	const unit = 1000
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "kMGTPE"[exp])
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
