package apiclient

import (
	"context"
)

// FileDrift is one drifted file with both sides attached for display.
type FileDrift struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Recorded string `json:"recorded"`
	Current  string `json:"current"`
}

// DriftResolution is an accept or restore outcome: adopted paths,
// a started operation id, or a no-op detail.
type DriftResolution struct {
	ID       int64    `json:"id"`
	Accepted []string `json:"accepted"`
	Detail   string   `json:"detail"`
}

// DriftReview returns the managed-state review for a package:
// every drifted file with recorded and current bytes.
func (c *Client) DriftReview(ctx context.Context, name string) ([]FileDrift, error) {
	var out []FileDrift
	if err := c.get(ctx, "/api/v1/catalog/"+name+"/drift", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []FileDrift{}
	}
	return out, nil
}

// DriftResolve applies one choice — accept adopts current files,
// restore rewrites recorded files through the operation engine.
func (c *Client) DriftResolve(ctx context.Context, name, action string) (DriftResolution, error) {
	var out DriftResolution
	body := map[string]any{"action": action}
	if err := c.postJSON(ctx, "/api/v1/catalog/"+name+"/drift", body, &out); err != nil {
		return DriftResolution{}, err
	}
	return out, nil
}
