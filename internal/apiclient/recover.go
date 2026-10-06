package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

// UnixClient returns an HTTP client dialing the root-only daemon socket
// (S021). Use it with a placeholder base URL — the dialer ignores the
// address: New("http://localhost", WithHTTPClient(UnixClient(path))).
func UnixClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: DefaultTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// Recover resets an administrator password over the local socket (S021).
// Unknown administrators answer 404.
func (c *Client) Recover(ctx context.Context, username, password string) (Admin, error) {
	data, err := json.Marshal(map[string]any{"username": username, "password": password})
	if err != nil {
		return Admin{}, fmt.Errorf("api: encode request: %w", err)
	}
	req, err := c.post(ctx, "/api/v1/admins/recover", data)
	if err != nil {
		return Admin{}, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return Admin{}, fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Admin{}, c.statusError(req, res)
	}
	var admin Admin
	if err := json.NewDecoder(res.Body).Decode(&admin); err != nil {
		return Admin{}, fmt.Errorf("api: decode response: %w", err)
	}
	return admin, nil
}
