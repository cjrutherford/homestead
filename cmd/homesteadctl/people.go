package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"homestead/internal/apiclient"
)

const peopleUsage = `usage: homesteadctl people <command> ...
  list
  apps PERSON [--json]
  add --name NAME --username USER [--email EMAIL] [--app APP[:admin]]...
      [--link | --generate | --password-file FILE | --password-stdin]
  edit PERSON [--name NAME] [--username USER] [--email EMAIL]
  reset-password PERSON [--link | --generate | --password-file FILE | --password-stdin]
  reset-mfa PERSON
  disable PERSON
  enable PERSON
  remove PERSON
  adopt USERNAME
  grant PERSON APP [--admin]
  revoke PERSON APP
  resolve PERSON put-back|keep|recreate|forget [--link | --generate]

PERSON is a username or the number from "people list". People are never
Homestead administrators; this only decides which apps they can open.
Passwords are read from a 0600 file or from standard input, never from
the command line. A generated password or one-time link is printed once
and is not stored.`

// runPeople drives the People API (S317, ADR-043): the same changes as
// the dashboard's People page, each one confirmed on the way in.
func runPeople(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, peopleUsage)
		return exitUsage
	}
	rest := args[1:]
	var code int
	switch args[0] {
	case "list":
		code = runPeopleList(ctx, client, stdout, stderr)
	case "apps":
		code = runPeopleApps(ctx, client, rest, stdout, stderr)
	case "add":
		code = runPeopleAdd(ctx, client, rest, stdout, stderr)
	case "edit":
		code = runPeopleEdit(ctx, client, rest, stdout, stderr)
	case "reset-password":
		code = runPeopleReset(ctx, client, rest, stdout, stderr)
	case "reset-mfa":
		code = runPeopleSimple(ctx, client, rest, stdout, stderr, "reset-mfa")
	case "disable", "enable", "remove":
		code = runPeopleSimple(ctx, client, rest, stdout, stderr, args[0])
	case "adopt":
		code = runPeopleAdopt(ctx, client, rest, stdout, stderr)
	case "grant":
		code = runPeopleGrant(ctx, client, rest, stdout, stderr, false)
	case "revoke":
		code = runPeopleGrant(ctx, client, rest, stdout, stderr, true)
	case "resolve":
		code = runPeopleResolve(ctx, client, rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown people subcommand %q\n%s\n", args[0], peopleUsage)
		return exitUsage
	}
	return code
}

// peopleFail explains the no-token state in full and falls back to fail.
func peopleFail(stderr io.Writer, err error) int {
	var se *apiclient.StatusError
	if errors.As(err, &se) && se.Code == apiclient.PeopleNoToken {
		fmt.Fprintln(stderr, "Error: Homestead cannot manage people without a kept Authentik API token.")
		fmt.Fprintln(stderr, "A kept token is sealed on this appliance. It lets Homestead create and change people and the homestead- groups; it does not let anyone sign in to Homestead.")
		fmt.Fprintln(stderr, "To allow it: homesteadctl sso plan --token-file FILE, then homesteadctl sso approve --digest D --token-file FILE --keep-token")
		return exitError
	}
	return fail(stderr, err)
}

// findPerson resolves a username or id from the list.
func findPerson(ctx context.Context, client *apiclient.Client, who string) (apiclient.Person, error) {
	view, err := client.People(ctx)
	if err != nil {
		return apiclient.Person{}, err
	}
	if !view.TokenKept {
		return apiclient.Person{}, &apiclient.StatusError{Method: "GET", Path: "/api/v1/people", Status: 422, Code: apiclient.PeopleNoToken}
	}
	id, idErr := strconv.ParseInt(who, 10, 64)
	for _, p := range view.People {
		if p.Username == who || (idErr == nil && p.ID == id) {
			return p, nil
		}
	}
	return apiclient.Person{}, fmt.Errorf("no person %q; see `homesteadctl people list`", who)
}

func runPeopleList(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	view, err := client.People(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if !view.TokenKept {
		switch view.Reason {
		case "no-sign-in":
			fmt.Fprintln(stdout, "People: Sign-in with Authentik is not set up, so Homestead cannot manage people.")
		default:
			fmt.Fprintln(stdout, "People: Homestead has no kept Authentik API token, so it cannot manage people.")
		}
		fmt.Fprintln(stdout, view.TokenWords)
		fmt.Fprintln(stdout, "To allow it: homesteadctl sso approve --digest D --token-file FILE --keep-token")
		return exitOK
	}
	fmt.Fprintf(stdout, "%s (owner): every app\n", view.Owner)
	for _, p := range view.People {
		fmt.Fprintf(stdout, "%d  %s (%s)  %s  %s\n", p.ID, p.DisplayName, p.Username, p.State, describeGrants(p.Grants))
		if p.Status != "" && p.Status != "ok" {
			fmt.Fprintf(stdout, "    status: %s\n", p.Status)
		}
		for _, d := range p.Drift {
			fmt.Fprintf(stdout, "    drift: %s: Homestead has %s, Authentik has %s\n", driftLabel(d), d.Homestead, d.Authentik)
		}
	}
	if len(view.People) == 0 {
		fmt.Fprintln(stdout, "no other people yet")
	}
	if view.ReadError != "" {
		fmt.Fprintf(stdout, "could not read Authentik: %s\n", view.ReadError)
	}
	for _, u := range view.Unmanaged {
		fmt.Fprintf(stdout, "not managed: %s (%s); `homesteadctl people adopt %s` brings them under Homestead\n", u.Username, u.Name, u.Username)
	}
	return exitOK
}

func driftLabel(d apiclient.PersonDrift) string {
	if d.Kind == "grant" {
		return "app " + d.App
	}
	return d.Field
}

func describeGrants(gs []apiclient.PersonGrant) string {
	if len(gs) == 0 {
		return "no apps"
	}
	parts := make([]string, 0, len(gs))
	for _, g := range gs {
		if g.Role == "admin" {
			parts = append(parts, g.App+" (admin)")
		} else {
			parts = append(parts, g.App)
		}
	}
	return strings.Join(parts, ", ")
}

// readPeoplePassword takes a password from a 0600 file or standard input.
func readPeoplePassword(file string, stdin bool, in io.Reader) (string, error) {
	if file != "" {
		info, err := os.Stat(file)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", file, err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("%s is readable by others; chmod 600 it first", file)
		}
		return readSecretFile(file)
	}
	data, err := io.ReadAll(io.LimitReader(in, 4096))
	if err != nil {
		return "", err
	}
	pw := strings.TrimRight(string(data), "\r\n")
	if pw == "" {
		return "", errors.New("no password on standard input")
	}
	return pw, nil
}

type credentialFlagSet struct {
	link, generate, stdin *bool
	file                  *string
}

func addCredentialFlags(set *flag.FlagSet) credentialFlagSet {
	return credentialFlagSet{
		link:     set.Bool("link", false, "send a one-time recovery link"),
		generate: set.Bool("generate", false, "generate a password and show it once"),
		stdin:    set.Bool("password-stdin", false, "read the password from standard input"),
		file:     set.String("password-file", "", "file (mode 0600) holding the password"),
	}
}

// resolve returns the method and any given password.
func (c credentialFlagSet) resolve(stdin io.Reader) (method, password string, err error) {
	n := 0
	for _, b := range []bool{*c.link, *c.generate, *c.stdin, *c.file != ""} {
		if b {
			n++
		}
	}
	if n > 1 {
		return "", "", errors.New("choose one of --link, --generate, --password-file, --password-stdin")
	}
	switch {
	case *c.generate:
		return "generated", "", nil
	case *c.stdin || *c.file != "":
		pw, err := readPeoplePassword(*c.file, *c.stdin, stdin)
		return "", pw, err
	}
	return "link", "", nil
}

func printRevealed(stdout, stderr io.Writer, rev *apiclient.Revealed) {
	if rev == nil {
		return
	}
	switch rev.Kind {
	case "link":
		fmt.Fprintln(stderr, "One-time sign-in link, for the local network (shown once, not stored):")
	default:
		fmt.Fprintln(stderr, "Generated password (shown once, not stored):")
	}
	fmt.Fprintln(stdout, rev.Value)
	if rev.PublicValue != "" {
		fmt.Fprintln(stderr, "The same link over the internet (it works once, from either address):")
		fmt.Fprintln(stdout, rev.PublicValue)
	}
}

func runPeopleAdd(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("people add", flag.ContinueOnError)
	set.SetOutput(stderr)
	name := set.String("name", "", "display name")
	username := set.String("username", "", "username")
	email := set.String("email", "", "email address")
	var apps listFlag
	set.Var(&apps, "app", "app they can open; APP:admin makes them an app admin; repeat for several")
	creds := addCredentialFlags(set)
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *name == "" || *username == "" || set.NArg() > 0 {
		fmt.Fprintln(stderr, "people add requires --name and --username")
		return exitUsage
	}
	method, password, err := creds.resolve(os.Stdin)
	if err != nil {
		return fail(stderr, err)
	}
	in := apiclient.PersonAdd{DisplayName: *name, Username: *username, Email: *email, Method: method, Password: password}
	for _, a := range apps {
		g := apiclient.PersonGrant{App: a, Role: "user"}
		if app, ok := strings.CutSuffix(a, ":admin"); ok {
			g = apiclient.PersonGrant{App: app, Role: "admin"}
		}
		in.Grants = append(in.Grants, g)
	}
	res, err := client.AddPerson(ctx, in)
	if err != nil {
		return peopleFail(stderr, err)
	}
	fmt.Fprintf(stderr, "Added %s (%s): %s\n", res.Person.DisplayName, res.Person.Username, describeGrants(res.Person.Grants))
	if res.Warning != "" {
		fmt.Fprintln(stderr, "warning:", res.Warning)
	}
	if res.SignInFailed {
		fmt.Fprintf(stderr, "To give them a generated password instead: homesteadctl people reset-password %s --generate\n", res.Person.Username)
	}
	printRevealed(stdout, stderr, res.Revealed)
	return exitOK
}

func runPeopleEdit(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("people edit", flag.ContinueOnError)
	set.SetOutput(stderr)
	name := set.String("name", "", "new display name")
	username := set.String("username", "", "new username")
	email := set.String("email", "", "new email address")
	who, flags := splitPerson(args)
	if err := set.Parse(flags); err != nil || who == "" || set.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl people edit PERSON [--name NAME] [--username USER] [--email EMAIL]")
		return exitUsage
	}
	var in apiclient.PersonEdit
	set.Visit(func(f *flag.Flag) {
		v := f.Value.String()
		switch f.Name {
		case "name":
			in.DisplayName = &v
		case "username":
			in.Username = &v
		case "email":
			in.Email = &v
		}
	})
	_, _, _ = name, username, email
	if in.DisplayName == nil && in.Username == nil && in.Email == nil {
		fmt.Fprintln(stderr, "people edit needs at least one of --name, --username, --email")
		return exitUsage
	}
	p, err := findPerson(ctx, client, who)
	if err != nil {
		return peopleFail(stderr, err)
	}
	res, err := client.EditPerson(ctx, p.ID, in)
	if err != nil {
		return peopleFail(stderr, err)
	}
	fmt.Fprintf(stdout, "Updated %s (%s)\n", res.Person.DisplayName, res.Person.Username)
	return exitOK
}

// splitPerson takes the first non-flag argument as the person, so flags
// may follow it.
func splitPerson(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func runPeopleReset(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("people reset-password", flag.ContinueOnError)
	set.SetOutput(stderr)
	creds := addCredentialFlags(set)
	who, flags := splitPerson(args)
	if err := set.Parse(flags); err != nil || who == "" || set.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl people reset-password PERSON [--link | --generate | --password-file FILE | --password-stdin]")
		return exitUsage
	}
	method, password, err := creds.resolve(os.Stdin)
	if err != nil {
		return fail(stderr, err)
	}
	p, err := findPerson(ctx, client, who)
	if err != nil {
		return peopleFail(stderr, err)
	}
	rev, err := client.ResetPersonPassword(ctx, p.ID, method, password)
	if err != nil {
		return peopleFail(stderr, err)
	}
	if rev == nil {
		fmt.Fprintf(stderr, "Password for %s set.\n", p.Username)
		return exitOK
	}
	fmt.Fprintf(stderr, "Password for %s reset.\n", p.Username)
	printRevealed(stdout, stderr, rev)
	return exitOK
}

func runPeopleSimple(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer, verb string) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, "usage: homesteadctl people %s PERSON\n", verb)
		return exitUsage
	}
	p, err := findPerson(ctx, client, args[0])
	if err != nil {
		return peopleFail(stderr, err)
	}
	switch verb {
	case "reset-mfa":
		res, err := client.ResetPersonSecondFactor(ctx, p.ID)
		if err != nil {
			return peopleFail(stderr, err)
		}
		fmt.Fprintf(stdout, "Second factor reset for %s: %d removed", p.Username, len(res.Removed))
		if len(res.Skipped) > 0 {
			fmt.Fprintf(stdout, ", %d left (%s)", len(res.Skipped), strings.Join(res.Skipped, ", "))
		}
		fmt.Fprintln(stdout)
	case "disable", "enable":
		if err := client.SetPersonActive(ctx, p.ID, verb == "enable"); err != nil {
			return peopleFail(stderr, err)
		}
		fmt.Fprintf(stdout, "%s: %sd\n", p.Username, verb)
	case "remove":
		if err := client.RemovePerson(ctx, p.ID); err != nil {
			return peopleFail(stderr, err)
		}
		fmt.Fprintf(stdout, "Removed %s. App data stays in the apps.\n", p.Username)
	}
	return exitOK
}

func runPeopleAdopt(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: homesteadctl people adopt USERNAME")
		return exitUsage
	}
	res, err := client.AdoptPerson(ctx, args[0])
	if err != nil {
		return peopleFail(stderr, err)
	}
	fmt.Fprintf(stdout, "Adopted %s: %s\n", res.Person.Username, describeGrants(res.Person.Grants))
	return exitOK
}

func runPeopleGrant(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer, revoke bool) int {
	verb := "grant"
	if revoke {
		verb = "revoke"
	}
	set := flag.NewFlagSet("people "+verb, flag.ContinueOnError)
	set.SetOutput(stderr)
	admin := set.Bool("admin", false, "make them an app admin")
	var pos []string
	rest := args
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if err := set.Parse(rest); err != nil {
		return exitUsage
	}
	pos = append(pos, set.Args()...)
	if len(pos) != 2 || (revoke && *admin) {
		fmt.Fprintf(stderr, "usage: homesteadctl people %s PERSON APP%s\n", verb, map[bool]string{false: " [--admin]", true: ""}[revoke])
		return exitUsage
	}
	p, err := findPerson(ctx, client, pos[0])
	if err != nil {
		return peopleFail(stderr, err)
	}
	if revoke {
		_, err = client.RevokePersonApp(ctx, p.ID, pos[1])
	} else {
		_, err = client.GrantPersonApp(ctx, p.ID, pos[1], *admin)
	}
	if err != nil {
		return peopleFail(stderr, err)
	}
	switch {
	case revoke:
		fmt.Fprintf(stdout, "%s can no longer open %s\n", p.Username, pos[1])
	case *admin:
		fmt.Fprintf(stdout, "%s can open %s as an app admin\n", p.Username, pos[1])
	default:
		fmt.Fprintf(stdout, "%s can open %s\n", p.Username, pos[1])
	}
	return exitOK
}

func runPeopleResolve(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("people resolve", flag.ContinueOnError)
	set.SetOutput(stderr)
	link := set.Bool("link", false, "recreate with a one-time link (default)")
	generate := set.Bool("generate", false, "recreate with a generated password")
	var pos []string
	rest := args
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if err := set.Parse(rest); err != nil {
		return exitUsage
	}
	pos = append(pos, set.Args()...)
	valid := map[string]bool{"put-back": true, "keep": true, "recreate": true, "forget": true}
	if len(pos) != 2 || !valid[pos[1]] || (*link && *generate) {
		fmt.Fprintln(stderr, "usage: homesteadctl people resolve PERSON put-back|keep|recreate|forget [--link | --generate]")
		return exitUsage
	}
	method := "link"
	if *generate {
		method = "generated"
	}
	p, err := findPerson(ctx, client, pos[0])
	if err != nil {
		return peopleFail(stderr, err)
	}
	rev, err := client.ResolvePersonDrift(ctx, p.ID, pos[1], method)
	if err != nil {
		return peopleFail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s: %s done\n", p.Username, pos[1])
	printRevealed(stdout, stderr, rev)
	return exitOK
}
