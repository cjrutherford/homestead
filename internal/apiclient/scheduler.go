package apiclient

import (
	"context"

	"homestead/internal/scheduler"
)

// SchedulerJobs lists scheduled jobs with last-run and next-due state (S260).
func (c *Client) SchedulerJobs(ctx context.Context) ([]scheduler.Status, error) {
	var out []scheduler.Status
	if err := c.get(ctx, "/api/v1/scheduler/jobs", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []scheduler.Status{}
	}
	return out, nil
}
