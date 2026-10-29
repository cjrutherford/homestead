package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// PeopleNoToken is the X-Homestead-Error code the daemon sends when it
// cannot manage people because no Authentik API token is kept.
const PeopleNoToken = "people-no-token"

// PersonGrant is one app a person can open, and whether as an app admin.
type PersonGrant struct {
	App  string `json:"app"`
	Role string `json:"role"`
}

// PersonDrift is one difference between Homestead's record and Authentik.
type PersonDrift struct {
	Kind      string `json:"kind"`
	Field     string `json:"field,omitempty"`
	App       string `json:"app,omitempty"`
	Homestead string `json:"homestead"`
	Authentik string `json:"authentik"`
}

// Person is one person Homestead manages.
type Person struct {
	ID          int64         `json:"id"`
	DisplayName string        `json:"display_name"`
	Username    string        `json:"username"`
	Email       string        `json:"email"`
	State       string        `json:"state"`
	Origin      string        `json:"origin"`
	CreatedBy   string        `json:"created_by"`
	CreatedAt   time.Time     `json:"created_at"`
	Grants      []PersonGrant `json:"grants"`
	Status      string        `json:"status"`
	Drift       []PersonDrift `json:"drift"`
}

// UnmanagedUser is an Authentik user Homestead did not create.
type UnmanagedUser struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Active   bool   `json:"active"`
}

// PeopleView is the People list.
type PeopleView struct {
	TokenKept  bool            `json:"token_kept"`
	People     []Person        `json:"people"`
	Unmanaged  []UnmanagedUser `json:"unmanaged"`
	Apps       []string        `json:"apps"`
	ReadError  string          `json:"read_error,omitempty"`
	Owner      string          `json:"owner"`
	Reason     string          `json:"reason,omitempty"`
	TokenWords string          `json:"token_words"`
}

// Revealed is a credential shown once: kind is password or link.
type Revealed struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	// PublicValue is a link's address outside the local network, when
	// Authentik has a public address.
	PublicValue string `json:"public_value,omitempty"`
}

// PersonAdd is the body for adding a person.
type PersonAdd struct {
	DisplayName string        `json:"display_name"`
	Username    string        `json:"username"`
	Email       string        `json:"email"`
	Method      string        `json:"method,omitempty"`
	Password    string        `json:"password,omitempty"`
	Grants      []PersonGrant `json:"grants,omitempty"`
}

// PersonEdit is the body for editing; nil fields stay as they are.
type PersonEdit struct {
	DisplayName *string `json:"display_name"`
	Username    *string `json:"username"`
	Email       *string `json:"email"`
}

// PersonResult is what add, edit, grant, revoke and adopt return.
type PersonResult struct {
	Person   Person    `json:"person"`
	Revealed *Revealed `json:"revealed"`
	Warning  string    `json:"warning"`
	// SignInFailed: the person was added but how they sign in was not
	// set up (add only, ADR-049 §4).
	SignInFailed bool `json:"sign_in_failed,omitempty"`
}

// SecondFactorReset says which devices were removed and which were left.
type SecondFactorReset struct {
	Removed []string `json:"removed"`
	Skipped []string `json:"skipped"`
}

// People reads the People page.
func (c *Client) People(ctx context.Context) (PeopleView, error) {
	var out PeopleView
	err := c.get(ctx, "/api/v1/people", &out)
	return out, err
}

func peoplePath(id int64, rest string) string {
	return fmt.Sprintf("/api/v1/people/%d%s", id, rest)
}

// AddPerson adds a person; every change carries confirm.
func (c *Client) AddPerson(ctx context.Context, in PersonAdd) (PersonResult, error) {
	var out PersonResult
	err := c.postJSON(ctx, "/api/v1/people", withConfirm(in), &out)
	return out, err
}

// AdoptPerson takes an existing Authentik user under Homestead.
func (c *Client) AdoptPerson(ctx context.Context, username string) (PersonResult, error) {
	var out PersonResult
	err := c.postJSON(ctx, "/api/v1/people/adopt", map[string]any{"username": username, "confirm": true}, &out)
	return out, err
}

// EditPerson changes a person's name, username or email.
func (c *Client) EditPerson(ctx context.Context, id int64, in PersonEdit) (PersonResult, error) {
	var out PersonResult
	raw, err := json.Marshal(withConfirm(in))
	if err != nil {
		return out, fmt.Errorf("api: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.base+peoplePath(id, ""), bytes.NewReader(raw))
	if err != nil {
		return out, fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	err = c.roundTrip(req, http.StatusOK, &out)
	return out, err
}

// ResetPersonPassword sets a new way to sign in: method link or generated,
// or a given password. The result carries the credential once.
func (c *Client) ResetPersonPassword(ctx context.Context, id int64, method, password string) (*Revealed, error) {
	var out PersonResult
	err := c.postJSON(ctx, peoplePath(id, "/password-reset"),
		map[string]any{"confirm": true, "method": method, "password": password}, &out)
	return out.Revealed, err
}

// ResetPersonSecondFactor removes a person's second-factor devices.
func (c *Client) ResetPersonSecondFactor(ctx context.Context, id int64) (SecondFactorReset, error) {
	var out SecondFactorReset
	err := c.postJSON(ctx, peoplePath(id, "/mfa-reset"), map[string]any{"confirm": true}, &out)
	return out, err
}

// SetPersonActive disables or enables a person.
func (c *Client) SetPersonActive(ctx context.Context, id int64, active bool) error {
	rest := "/disable"
	if active {
		rest = "/enable"
	}
	return c.postJSON(ctx, peoplePath(id, rest), map[string]any{"confirm": true}, nil)
}

// RemovePerson removes a person from Authentik and Homestead.
func (c *Client) RemovePerson(ctx context.Context, id int64) error {
	return c.postJSON(ctx, peoplePath(id, "/remove"), map[string]any{"confirm": true}, nil)
}

// GrantPersonApp lets a person open an app, as an app admin when admin.
func (c *Client) GrantPersonApp(ctx context.Context, id int64, app string, admin bool) (Person, error) {
	var out PersonResult
	path := peoplePath(id, "/apps/"+url.PathEscape(app))
	if admin {
		path += "/admin"
	}
	raw, err := json.Marshal(map[string]any{"confirm": true})
	if err != nil {
		return Person{}, fmt.Errorf("api: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return Person{}, fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	err = c.roundTrip(req, http.StatusOK, &out)
	return out.Person, err
}

// RevokePersonApp takes an app away from a person.
func (c *Client) RevokePersonApp(ctx context.Context, id int64, app string) (Person, error) {
	var out PersonResult
	err := c.postJSON(ctx, peoplePath(id, "/apps/"+url.PathEscape(app)+"/revoke"), map[string]any{"confirm": true}, &out)
	return out.Person, err
}

// ResolvePersonDrift settles drift: put-back, keep, recreate or forget.
func (c *Client) ResolvePersonDrift(ctx context.Context, id int64, action, method string) (*Revealed, error) {
	var out PersonResult
	err := c.postJSON(ctx, peoplePath(id, "/resolve"),
		map[string]any{"confirm": true, "action": action, "method": method}, &out)
	return out.Revealed, err
}

func withConfirm(v any) map[string]any {
	raw, _ := json.Marshal(v)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	m["confirm"] = true
	return m
}
