package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"

	"homestead/internal/apiclient"
)

const appsMineUsage = "usage: homesteadctl apps mine [--json]"
const peopleAppsUsage = "usage: homesteadctl people apps PERSON [--json]   (PERSON is a username or the number from \"people list\")"

// runAppsMine is `apps mine` (S337, ADR-050 §5): the apps the owner sees on
// the dashboard's apps page. The CLI runs as the owner on the local address,
// so each address is the local one.
func runAppsMine(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("apps mine", flag.ContinueOnError)
	set.SetOutput(stderr)
	asJSON := set.Bool("json", false, "print JSON")
	if set.Parse(args) != nil || set.NArg() > 0 {
		fmt.Fprintln(stderr, appsMineUsage)
		return exitUsage
	}
	apps, err := client.MyApps(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	return printMyApps(stdout, apps, *asJSON, "no installed apps with a web address")
}

// runPeopleApps is `people apps`: the list a person would see, from
// Homestead's record of their grants.
func runPeopleApps(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("people apps", flag.ContinueOnError)
	set.SetOutput(stderr)
	asJSON := set.Bool("json", false, "print JSON")
	// Allow the flag after the person, as the other commands do.
	var who string
	rest := args
	if len(rest) > 0 && rest[0] != "" && rest[0][0] != '-' {
		who, rest = rest[0], rest[1:]
	}
	if set.Parse(rest) != nil || set.NArg() > 0 || who == "" {
		fmt.Fprintln(stderr, peopleAppsUsage)
		return exitUsage
	}
	id, err := strconv.ParseInt(who, 10, 64)
	if err != nil {
		p, err := findPerson(ctx, client, who)
		if err != nil {
			return peopleFail(stderr, err)
		}
		id = p.ID
	}
	apps, err := client.PersonApps(ctx, id)
	if err != nil {
		return fail(stderr, err)
	}
	return printMyApps(stdout, apps, *asJSON, "no apps: nothing granted, or the person is disabled")
}

func printMyApps(stdout io.Writer, apps []apiclient.MyApp, asJSON bool, empty string) int {
	if asJSON {
		if apps == nil {
			apps = []apiclient.MyApp{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(apps)
		return exitOK
	}
	if len(apps) == 0 {
		fmt.Fprintln(stdout, empty)
		return exitOK
	}
	for _, a := range apps {
		fmt.Fprintf(stdout, "%s  %s  %s  %s\n", a.Name, a.Title, a.Role, a.URL)
	}
	return exitOK
}
