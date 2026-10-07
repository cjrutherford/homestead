package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"homestead/internal/audit"
)

// AuditList returns the newest audit entries first (S063). Non-positive
// limits default server-side; over-large ones clamp.
func (c *Client) AuditList(ctx context.Context, limit int) ([]audit.Entry, error) {
	path := "/api/v1/audit"
	if limit > 0 {
		path += "?limit=" + url.QueryEscape(strconv.Itoa(limit))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, c.statusError(req, res)
	}
	var entries []audit.Entry
	if err := json.NewDecoder(res.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("api: decode response: %w", err)
	}
	if entries == nil {
		entries = []audit.Entry{}
	}
	return entries, nil
}
