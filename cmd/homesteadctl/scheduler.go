package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"homestead/internal/apiclient"
)

func runScheduler(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "jobs" {
		fmt.Fprintln(stderr, "usage: homesteadctl scheduler jobs")
		return exitUsage
	}
	jobs, err := client.SchedulerJobs(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	for _, job := range jobs {
		next, last := "-", "never"
		if job.Enabled && !job.NextDue.IsZero() {
			next = job.NextDue.UTC().Format(time.RFC3339)
		}
		if !job.LastRunAt.IsZero() {
			last = job.LastRunAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(stdout, "%s every %ds enabled=%v last=%s next=%s op=%d running=%v\n",
			job.ID, job.IntervalSeconds, job.Enabled, last, next, job.LastOperationID, job.Running)
		if job.LastError != "" {
			fmt.Fprintf(stdout, "  last_error=%s\n", job.LastError)
		}
		if !job.LastSkipAt.IsZero() {
			fmt.Fprintf(stdout, "  last_skip=%s blocked_by_operation=%d\n",
				job.LastSkipAt.UTC().Format(time.RFC3339), job.LastSkipBlocking)
		}
	}
	return exitOK
}
