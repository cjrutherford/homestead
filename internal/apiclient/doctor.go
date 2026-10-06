package apiclient

import (
	"context"

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
