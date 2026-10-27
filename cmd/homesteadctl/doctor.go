package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"homestead/internal/apiclient"
	"homestead/internal/doctor"
)

// runDoctor prints the system diagnostic report (S044, S316): the checks
// under their group headings, each problem with what it means and what to
// do, then the overall verdict. --live also runs the live probes (the
// tunnel, Authentik, the apps). Any failing check exits 1; warnings still
// exit 0. Doctor reads and explains; it never acts.
func runDoctor(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("doctor", flag.ContinueOnError)
	set.SetOutput(stderr)
	live := set.Bool("live", false, "also run the live probes: tunnel, Authentik, app addresses")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	var (
		report doctor.Report
		err    error
	)
	if *live {
		report, err = client.DoctorLive(ctx)
	} else {
		report, err = client.Doctor(ctx)
	}
	if err != nil {
		return fail(stderr, err)
	}
	for _, group := range doctor.ByGroup(report.Checks) {
		if group.Group == "" {
			// Reports from an older daemon carry no groups.
			group.Group = "Checks"
		}
		fmt.Fprintf(stdout, "== %s\n", group.Group)
		for _, check := range group.Checks {
			fmt.Fprintf(stdout, "%s: %s — %s\n", check.Name, check.Status, check.Detail)
			if check.Status == doctor.StatusOK {
				continue
			}
			if check.Why != "" {
				fmt.Fprintf(stdout, "    why: %s\n", check.Why)
			}
			if check.Fix != nil {
				fmt.Fprintf(stdout, "    fix: %s\n", check.Fix.Text)
				if check.Fix.Link != "" {
					fmt.Fprintf(stdout, "    page: %s\n", check.Fix.Link)
				}
				if check.Fix.Command != "" {
					fmt.Fprintf(stdout, "    run: %s\n", check.Fix.Command)
				}
			}
		}
	}
	if !report.Live {
		fmt.Fprintln(stdout, "live checks not run: add --live for the tunnel, Authentik and the apps")
	}
	fmt.Fprintf(stdout, "overall: %s\n", report.Overall)
	if report.Overall == doctor.StatusFail {
		return exitError
	}
	return exitOK
}
