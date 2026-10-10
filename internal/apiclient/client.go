// Package apiclient is Homestead's reusable Go API client (S017; ADR-004).
//
// It speaks the daemon's HTTP API for local consumers — homesteadctl (S018)
// first among them — so CLI and dashboard share one validated path to the
// same endpoints. Transport failures return errors; validation outcomes
// stay data (Plan.Valid / field errors), mirroring internal/config.
//
// The client targets a base URL (loopback TCP today). Alternative
// transports — notably the local Unix socket from the trust ladder — plug
// in later through WithHTTPClient without changing call sites.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"homestead/internal/config"
	"homestead/internal/desired"
	"homestead/internal/schema"
)

// DefaultTimeout bounds every API call.
const DefaultTimeout = 10 * time.Second

// Client speaks the homestead-server API rooted at baseURL.
type Client struct {
	base string
	http *http.Client
	// stream serves event streams without a deadline: the server
	// closes them at terminal states and the caller's context
	// bounds them — a call timeout would kill long operations
	// mid-follow instead.
	stream *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout overrides DefaultTimeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.http.Timeout = d
	}
}

// WithHTTPClient overrides the transport (tests, future Unix-socket dialer).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		c.http = h
	}
}

// New returns a Client for baseURL (e.g. http://127.0.0.1:8080).
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		base:   strings.TrimRight(baseURL, "/"),
		http:   &http.Client{Timeout: DefaultTimeout},
		stream: &http.Client{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Health reports daemon health (S010).
type Health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// StatusError reports an unexpected HTTP status with a body excerpt.
type StatusError struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("api: %s %s: status %d (%s)", e.Method, e.Path, e.Status, e.Body)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	return c.roundTrip(req, http.StatusOK, out)
}

func (c *Client) roundTrip(req *http.Request, okStatus int, out any) error {
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != okStatus {
		excerpt, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return &StatusError{
			Method: req.Method,
			Path:   req.URL.Path,
			Status: res.StatusCode,
			Body:   strings.TrimSpace(string(excerpt)),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("api: decode response: %w", err)
	}
	return nil
}

// Healthz returns the daemon health record.
func (c *Client) Healthz(ctx context.Context) (Health, error) {
	var h Health
	if err := c.get(ctx, "/healthz", &h); err != nil {
		return Health{}, err
	}
	return h, nil
}

// Version returns the daemon version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	var body struct {
		Version string `json:"version"`
	}
	if err := c.get(ctx, "/version", &body); err != nil {
		return "", err
	}
	return body.Version, nil
}

// GetConfig reads the current desired state (S016).
func (c *Client) GetConfig(ctx context.Context) (desired.State, error) {
	var state desired.State
	if err := c.get(ctx, "/api/v1/config", &state); err != nil {
		return desired.State{}, err
	}
	return state, nil
}

type planEnvelope struct {
	Valid     bool                `json:"valid"`
	Effective map[string]any      `json:"effective"`
	Errors    []schema.FieldError `json:"errors"`
}

// PlanConfig validates a candidate without persisting (S016 plan).
// A 422 answers as a Plan with Valid=false; transport failures error.
func (c *Client) PlanConfig(ctx context.Context, settings map[string]any) (config.Plan, error) {
	return c.submitPlan(ctx, "/api/v1/config/plan", settings)
}

// ApplyConfig validates and persists a candidate (S016 apply). Invalid
// candidates return field errors with no error and persist nothing.
func (c *Client) ApplyConfig(ctx context.Context, settings map[string]any) (desired.State, []schema.FieldError, error) {
	var envelope planEnvelope
	req, err := c.planRequest(ctx, "/api/v1/config", http.MethodPut, settings)
	if err != nil {
		return desired.State{}, nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return desired.State{}, nil, fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		var state desired.State
		if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
			return desired.State{}, nil, fmt.Errorf("api: decode response: %w", err)
		}
		return state, nil, nil
	case http.StatusUnprocessableEntity:
		if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
			return desired.State{}, nil, fmt.Errorf("api: decode response: %w", err)
		}
		return desired.State{}, envelope.Errors, nil
	default:
		excerpt, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return desired.State{}, nil, &StatusError{
			Method: http.MethodPut,
			Path:   "/api/v1/config",
			Status: res.StatusCode,
			Body:   strings.TrimSpace(string(excerpt)),
		}
	}
}

func (c *Client) submitPlan(ctx context.Context, path string, settings map[string]any) (config.Plan, error) {
	var envelope planEnvelope
	req, err := c.planRequest(ctx, path, http.MethodPost, settings)
	if err != nil {
		return config.Plan{}, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return config.Plan{}, fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusUnprocessableEntity {
		excerpt, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return config.Plan{}, &StatusError{
			Method: http.MethodPost,
			Path:   path,
			Status: res.StatusCode,
			Body:   strings.TrimSpace(string(excerpt)),
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
		return config.Plan{}, fmt.Errorf("api: decode response: %w", err)
	}
	return config.Plan{Valid: envelope.Valid, Effective: envelope.Effective, Errors: envelope.Errors}, nil
}

func (c *Client) planRequest(ctx context.Context, path, method string, settings map[string]any) (*http.Request, error) {
	if settings == nil {
		settings = map[string]any{}
	}
	data, err := json.Marshal(map[string]any{"settings": settings})
	if err != nil {
		return nil, fmt.Errorf("api: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
