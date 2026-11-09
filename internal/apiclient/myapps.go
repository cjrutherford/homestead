package apiclient

import (
	"context"
	"fmt"
)

// MyApp is one app on the apps list (S337, ADR-050).
type MyApp struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Icon  string `json:"icon"`
	// Role is "owner", "user" or "admin".
	Role string `json:"role"`
	URL  string `json:"url"`
	// LocalURL is always set; PublicURL only once the app is published.
	LocalURL  string `json:"local_url"`
	PublicURL string `json:"public_url,omitempty"`
}

// MyApps reads the signed-in owner's apps.
func (c *Client) MyApps(ctx context.Context) ([]MyApp, error) {
	var out struct {
		Apps []MyApp `json:"apps"`
	}
	if err := c.get(ctx, "/api/v1/me/apps", &out); err != nil {
		return nil, err
	}
	return out.Apps, nil
}

// PersonApps reads the apps one person would see.
func (c *Client) PersonApps(ctx context.Context, id int64) ([]MyApp, error) {
	var out struct {
		Apps []MyApp `json:"apps"`
	}
	if err := c.get(ctx, fmt.Sprintf("/api/v1/people/%d/apps", id), &out); err != nil {
		return nil, err
	}
	return out.Apps, nil
}
