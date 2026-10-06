package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Admin is a created administrator record.
type Admin struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// LoginSession is a minted session: single-use token plus expiry.
type LoginSession struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SetupAdmin creates the first administrator (S020). The appliance must be
// CLAIMED with no administrator yet, else a *StatusError (403/409/422).
func (c *Client) SetupAdmin(ctx context.Context, username, password string) (Admin, error) {
	data, err := json.Marshal(map[string]any{"username": username, "password": password})
	if err != nil {
		return Admin{}, fmt.Errorf("api: encode request: %w", err)
	}
	req, err := c.post(ctx, "/api/v1/admins/setup", data)
	if err != nil {
		return Admin{}, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return Admin{}, fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return Admin{}, c.statusError(req, res)
	}
	var admin Admin
	if err := json.NewDecoder(res.Body).Decode(&admin); err != nil {
		return Admin{}, fmt.Errorf("api: decode response: %w", err)
	}
	return admin, nil
}

// Login mints a session token (S020). Bad credentials answer 401.
func (c *Client) Login(ctx context.Context, username, password string) (LoginSession, error) {
	data, err := json.Marshal(map[string]any{"username": username, "password": password})
	if err != nil {
		return LoginSession{}, fmt.Errorf("api: encode request: %w", err)
	}
	req, err := c.post(ctx, "/api/v1/sessions", data)
	if err != nil {
		return LoginSession{}, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return LoginSession{}, fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return LoginSession{}, c.statusError(req, res)
	}
	var session LoginSession
	if err := json.NewDecoder(res.Body).Decode(&session); err != nil {
		return LoginSession{}, fmt.Errorf("api: decode response: %w", err)
	}
	return session, nil
}

// Logout destroys the session for token (S020). Unknown sessions answer 401.
func (c *Client) Logout(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/api/v1/sessions", nil)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return c.statusError(req, res)
	}
	return nil
}
