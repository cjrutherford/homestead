package apiclient

import "context"

// SecretKeyStatus is the secret-store key custody status (S258).
type SecretKeyStatus struct {
	Version    int  `json:"version"`
	InProgress bool `json:"in_progress"`
}

// SecretKeyStatus reports the current secret key version.
func (c *Client) SecretKeyStatus(ctx context.Context) (SecretKeyStatus, error) {
	var out SecretKeyStatus
	if err := c.get(ctx, "/api/v1/secrets/key", &out); err != nil {
		return SecretKeyStatus{}, err
	}
	return out, nil
}

// SecretRekey starts the confirmed re-key operation and returns its id.
func (c *Client) SecretRekey(ctx context.Context) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.postJSON(ctx, "/api/v1/secrets/rekey", map[string]any{"confirm": true}, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
