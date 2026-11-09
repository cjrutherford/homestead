package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"homestead/internal/setup"
)

// SetupStatus reads the post-claim walkthrough status (S287, ADR-034).
func (c *Client) SetupStatus(ctx context.Context) (setup.Status, error) {
	var out setup.Status
	err := c.get(ctx, "/api/v1/setup", &out)
	return out, err
}

// SetupUpdate records a skip, unskip, acknowledgement or choice for a step.
// It carries no credential: those go through the existing sealed routes.
func (c *Client) SetupUpdate(ctx context.Context, step string, state, provider, access, domain *string) (setup.Status, error) {
	raw, err := json.Marshal(map[string]*string{"state": state, "provider": provider, "access": access, "domain": domain})
	if err != nil {
		return setup.Status{}, fmt.Errorf("api: encode: %w", err)
	}
	// Omit unset fields: the daemon rejects an empty update and treats a
	// null as absent.
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	for k, v := range fields {
		if v == nil {
			delete(fields, k)
		}
	}
	raw, _ = json.Marshal(fields)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/api/v1/setup/steps/"+url.PathEscape(step), bytes.NewReader(raw))
	if err != nil {
		return setup.Status{}, fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	var out setup.Status
	err = c.roundTrip(req, http.StatusOK, &out)
	return out, err
}
