package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Admin is a created administrator record.
type Admin struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// LoginSession is a minted session: a bearer token reusable until it
// expires (12h idle, 7d absolute) or is revoked by logout or password
// change. CSRFToken only matters to browsers; the CLI ignores it.
type LoginSession struct {
	Token     string    `json:"token"`
	CSRFToken string    `json:"csrf_token,omitempty"`
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

// ChangePassword rotates the session owner's password (S239):
// wrong current answers 401, weak replacements 422, throttled
// callers 429. Sessions issued before rotation die with it.
func (c *Client) ChangePassword(ctx context.Context, token, current, replacement string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/admins/password", nil)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(map[string]any{
		"current_password": current, "new_password": replacement,
	}); err != nil {
		return fmt.Errorf("api: encode: %w", err)
	}
	req.Body = io.NopCloser(&body)
	req.ContentLength = int64(body.Len())
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return c.statusError(req, res)
	}
	return nil
}

// LockoutStatus reports whether the session owner's account sits
// inside a login lockout (S239).
func (c *Client) LockoutStatus(ctx context.Context, token string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/admins/lockout", nil)
	if err != nil {
		return false, fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var out struct {
		Locked bool `json:"locked"`
	}
	if err := c.roundTrip(req, http.StatusOK, &out); err != nil {
		return false, err
	}
	return out.Locked, nil
}
