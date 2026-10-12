package apiclient

import (
	"context"
	"time"
)

// DriftScanFile is one drifted file in a scheduled scan result.
type DriftScanFile struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// DriftScanStack is one package with drifted files.
type DriftScanStack struct {
	Package string          `json:"package"`
	Files   []DriftScanFile `json:"files"`
}

// DriftScanResult is the latest scheduled scan (S261).
type DriftScanResult struct {
	ScannedAt time.Time        `json:"scanned_at"`
	Scanned   int              `json:"scanned"`
	Drifted   []DriftScanStack `json:"drifted"`
	Errors    []string         `json:"errors"`
}

// DriftSchedule is the scan cadence plus the latest scan; LastScan is
// nil before the first scan.
type DriftSchedule struct {
	IntervalSeconds int64            `json:"interval_seconds"`
	Enabled         bool             `json:"enabled"`
	UpdatedAt       time.Time        `json:"updated_at"`
	LastScan        *DriftScanResult `json:"last_scan"`
}

// DriftSchedule reads the periodic scan schedule and latest result.
func (c *Client) DriftSchedule(ctx context.Context) (DriftSchedule, error) {
	var out DriftSchedule
	if err := c.get(ctx, "/api/v1/drift/schedule", &out); err != nil {
		return DriftSchedule{}, err
	}
	return out, nil
}

// DriftScheduleSave sets the scan interval and enablement.
func (c *Client) DriftScheduleSave(ctx context.Context, intervalSeconds int64, enabled bool) (DriftSchedule, error) {
	var out DriftSchedule
	body := map[string]any{"interval_seconds": intervalSeconds, "enabled": enabled}
	if err := c.postJSON(ctx, "/api/v1/drift/schedule", body, &out); err != nil {
		return DriftSchedule{}, err
	}
	return out, nil
}
