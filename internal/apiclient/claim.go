package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Identity is the appliance identity record (S019): claim state plus
// daemon version. Always available, even UNCLAIMED.
type Identity struct {
	State   string `json:"state"`
	Version string `json:"version"`
}

// Identity returns the appliance identity.
func (c *Client) Identity(ctx context.Context) (Identity, error) {
	var id Identity
	if err := c.get(ctx, "/api/v1/identity", &id); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// Claim redeems the single-use console token (S019). Success returns the
// new state ("CLAIMED"); consumed/unknown/expired tokens and double claims
// return a *StatusError (401/409) carrying the server message.
func (c *Client) Claim(ctx context.Context, token string) (string, error) {
	data, err := json.Marshal(map[string]any{"token": token})
	if err != nil {
		return "", fmt.Errorf("api: encode request: %w", err)
	}
	req, err := c.post(ctx, "/api/v1/claim", data)
	if err != nil {
		return "", err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", c.statusError(req, res)
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("api: decode response: %w", err)
	}
	return body.State, nil
}

func (c *Client) post(ctx context.Context, path string, data []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) statusError(req *http.Request, res *http.Response) error {
	excerpt, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	return &StatusError{
		Method: req.Method,
		Path:   req.URL.Path,
		Status: res.StatusCode,
		Body:   strings.TrimSpace(string(excerpt)),
	}
}
