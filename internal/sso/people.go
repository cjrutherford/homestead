package sso

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Authentik user management for the People feature (S317, ADR-043 section
// 4). Every call here acts on a user pk the caller took from Homestead's own
// records, or lists users to show them; none of them touches a group
// without the homestead- prefix.

const (
	// ManagedAttribute tags a user Homestead created or adopted.
	ManagedAttribute = "homestead.managed"
	// ManagedPath is the Authentik user path of users Homestead creates.
	ManagedPath = "users/homestead"
)

// RemoteAccount is an Authentik user as read.
type RemoteAccount struct {
	PK         int            `json:"pk"`
	Username   string         `json:"username"`
	Name       string         `json:"name"`
	Email      string         `json:"email"`
	IsActive   bool           `json:"is_active"`
	Path       string         `json:"path"`
	Type       string         `json:"type"`
	UID        string         `json:"uid"`
	Attributes map[string]any `json:"attributes"`
	GroupsObj  []struct {
		Name string `json:"name"`
	} `json:"groups_obj"`
}

// Managed reports the Homestead tag.
func (u RemoteAccount) Managed() bool {
	v, _ := u.Attributes[ManagedAttribute].(bool)
	return v
}

// HomesteadGroups are the member's groups that carry the prefix.
func (u RemoteAccount) HomesteadGroups() []string {
	var out []string
	for _, g := range u.GroupsObj {
		if ownedGroup(g.Name) {
			out = append(out, g.Name)
		}
	}
	return out
}

// SystemAccount reports Authentik's own service and system accounts, which
// are never listed or touched.
func (u RemoteAccount) SystemAccount() bool {
	switch u.Type {
	case "service_account", "internal_service_account":
		return true
	}
	return strings.HasPrefix(u.Username, "ak-outpost-")
}

// ListAccounts reads every human (internal or external) user.
func (a *API) ListAccounts(ctx context.Context) ([]RemoteAccount, error) {
	var all []RemoteAccount
	for page := 1; page > 0 && page < 1000; {
		var out pagedList[RemoteAccount]
		path := fmt.Sprintf("/api/v3/core/users/?include_groups=true&type=internal&type=external&page_size=200&page=%d", page)
		if err := a.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		for _, u := range out.Results {
			if !u.SystemAccount() {
				all = append(all, u)
			}
		}
		page = out.Pagination.Next
	}
	return all, nil
}

func accountPath(pk int) string { return fmt.Sprintf("/api/v3/core/users/%d/", pk) }

// GetAccount returns the user, or nil when it does not exist.
func (a *API) GetAccount(ctx context.Context, pk int) (*RemoteAccount, error) {
	var u RemoteAccount
	err := a.do(ctx, http.MethodGet, accountPath(pk), nil, &u)
	var ae *APIError
	if asAPIError(err, &ae) && ae.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// AccountFields are the writable fields of a managed user.
type AccountFields struct {
	Username string
	Name     string
	Email    string
}

// CreateAccount makes a Homestead-tagged internal user.
func (a *API) CreateAccount(ctx context.Context, f AccountFields) (*RemoteAccount, error) {
	body := map[string]any{
		"username": f.Username, "name": f.Name, "email": f.Email, "is_active": true,
		"type": "internal", "path": ManagedPath,
		"attributes": map[string]any{ManagedAttribute: true},
	}
	var u RemoteAccount
	if err := a.do(ctx, http.MethodPost, "/api/v3/core/users/", body, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// PatchAccount changes the given fields of a user.
func (a *API) PatchAccount(ctx context.Context, pk int, fields map[string]any) error {
	return a.do(ctx, http.MethodPatch, accountPath(pk), fields, nil)
}

// TagAccount adds the managed tag, keeping the user's other attributes.
func (a *API) TagAccount(ctx context.Context, u RemoteAccount) error {
	attrs := map[string]any{}
	for k, v := range u.Attributes {
		attrs[k] = v
	}
	attrs[ManagedAttribute] = true
	return a.PatchAccount(ctx, u.PK, map[string]any{"attributes": attrs})
}

// UntagAccount removes the managed tag (releasing an adopted user).
func (a *API) UntagAccount(ctx context.Context, u RemoteAccount) error {
	attrs := map[string]any{}
	for k, v := range u.Attributes {
		if k != ManagedAttribute {
			attrs[k] = v
		}
	}
	return a.PatchAccount(ctx, u.PK, map[string]any{"attributes": attrs})
}

// DeleteAccount deletes the user.
func (a *API) DeleteAccount(ctx context.Context, pk int) error {
	return a.do(ctx, http.MethodDelete, accountPath(pk), nil, nil)
}

// SetPassword sets the user's password. The value is sent once and is not
// kept.
func (a *API) SetPassword(ctx context.Context, pk int, password string) error {
	return a.do(ctx, http.MethodPost, accountPath(pk)+"set_password/", map[string]any{"password": password}, nil)
}

// RecoveryLink makes a temporary link the user opens to set a password.
func (a *API) RecoveryLink(ctx context.Context, pk int) (string, error) {
	var out struct {
		Link string `json:"link"`
	}
	if err := a.do(ctx, http.MethodPost, accountPath(pk)+"recovery/", map[string]any{}, &out); err != nil {
		return "", err
	}
	return out.Link, nil
}

// AddToGroup and RemoveFromGroup change one membership. The group is found
// by name, and only a homestead- group is ever changed.
func (a *API) AddToGroup(ctx context.Context, group string, userPK int) error {
	return a.membership(ctx, group, userPK, "add_user")
}

func (a *API) RemoveFromGroup(ctx context.Context, group string, userPK int) error {
	return a.membership(ctx, group, userPK, "remove_user")
}

func (a *API) membership(ctx context.Context, group string, userPK int, verb string) error {
	g, err := a.FindGroup(ctx, group)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("sso: group %s does not exist", group)
	}
	return a.do(ctx, http.MethodPost, "/api/v3/core/groups/"+url.PathEscape(g.PK)+"/"+verb+"/", map[string]any{"pk": userPK}, nil)
}

type remoteDevice struct {
	PK            string `json:"pk"`
	Name          string `json:"name"`
	MetaModelName string `json:"meta_model_name"`
}

// deviceKind maps a device's model name to the admin endpoint segment.
func deviceKind(model string) string {
	m := strings.ToLower(model)
	for _, k := range []string{"totp", "static", "webauthn", "duo", "sms", "email"} {
		if strings.HasSuffix(m, k+"device") {
			return k
		}
	}
	return ""
}

// ResetSecondFactor deletes the user's registered authenticators so they
// enrol again, and returns how many it removed. A device of a kind this
// code does not know is left and counted in skipped.
func (a *API) ResetSecondFactor(ctx context.Context, pk int) (removed, skipped int, err error) {
	var devices []remoteDevice
	if err := a.do(ctx, http.MethodGet, fmt.Sprintf("/api/v3/authenticators/admin/all/?user=%d", pk), nil, &devices); err != nil {
		return 0, 0, err
	}
	for _, d := range devices {
		kind := deviceKind(d.MetaModelName)
		if kind == "" {
			skipped++
			continue
		}
		if err := a.do(ctx, http.MethodDelete, "/api/v3/authenticators/admin/"+kind+"/"+url.PathEscape(d.PK)+"/", nil, nil); err != nil {
			return removed, skipped, err
		}
		removed++
	}
	return removed, skipped, nil
}
