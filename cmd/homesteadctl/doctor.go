package main

import (
	"context"
	"fmt"
	"io"

	"homestead/internal/apiclient"
	"homestead/internal/doctor"
)

// runDoctor prints the system diagnostic report (S044): one line per
// check plus the overall verdict. Any failing check exits 1; warnings
// still exit 0 — the details say what to do either way.
func runDoctor(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	report, err := client.Doctor(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	for _, check := range report.Checks {
		fmt.Fprintf(stdout, "%s: %s — %s\n", check.Name, check.Status, check.Detail)
	}
	fmt.Fprintf(stdout, "overall: %s\n", report.Overall)
	if report.Overall == doctor.StatusFail {
		return exitError
	}
	return exitOK
}
