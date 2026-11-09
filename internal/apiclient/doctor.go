package apiclient

import (
	"context"
	"time"

	"homestead/internal/doctor"
)

// Doctor returns the system diagnostic report (S044).
func (c *Client) Doctor(ctx context.Context) (doctor.Report, error) {
	var report doctor.Report
	if err := c.get(ctx, "/api/v1/doctor", &report); err != nil {
		return doctor.Report{}, err
	}
	return report, nil
}

// liveTimeout leaves room for the daemon's live probe budget.
const liveTimeout = doctor.DefaultBudget + 10*time.Second

// DoctorLive returns the report with the live probes run (S316):
// GET /api/v1/doctor?live=1.
func (c *Client) DoctorLive(ctx context.Context) (doctor.Report, error) {
	longer := *c
	h := *c.http
	h.Timeout = liveTimeout
	longer.http = &h
	var report doctor.Report
	if err := longer.get(ctx, "/api/v1/doctor?live=1", &report); err != nil {
		return doctor.Report{}, err
	}
	return report, nil
}
