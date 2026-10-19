package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"homestead/internal/discover"
	"homestead/internal/storage"
)

// StorageMounts lists discovered mounts (S235): candidates for
// registration with accessibility and free bytes.
func (c *Client) StorageMounts(ctx context.Context) ([]discover.Mount, error) {
	var out []discover.Mount
	if err := c.get(ctx, "/api/v1/storage/mounts", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []discover.Mount{}
	}
	return out, nil
}

// StorageRegistrations lists registered mounts.
func (c *Client) StorageRegistrations(ctx context.Context) ([]storage.Registration, error) {
	var out []storage.Registration
	if err := c.get(ctx, "/api/v1/storage/registrations", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []storage.Registration{}
	}
	return out, nil
}

// StorageRegister approves a discovered mount under a label.
func (c *Client) StorageRegister(ctx context.Context, mountpoint, label, actor string) (storage.Registration, error) {
	var out storage.Registration
	body := map[string]any{"mountpoint": mountpoint, "label": label, "actor": actor}
	if err := c.postJSON(ctx, "/api/v1/storage/registrations", body, &out); err != nil {
		return storage.Registration{}, err
	}
	return out, nil
}

// StorageAssignments lists class bindings.
func (c *Client) StorageAssignments(ctx context.Context) ([]storage.Assignment, error) {
	var out []storage.Assignment
	if err := c.get(ctx, "/api/v1/storage/assignments", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []storage.Assignment{}
	}
	return out, nil
}

// StorageImpactError is the daemon's 409 refusal: moving a class off a
// mount that installed packages still use. Re-send with the
// acknowledgement to proceed.
type StorageImpactError struct {
	Class    string
	From     string
	To       string
	Packages []string
}

func (e *StorageImpactError) Error() string {
	return fmt.Sprintf("moving %s from %s to %s affects installed packages (%s): their backups stop finding data on %s; re-run with --acknowledge-impact to proceed",
		e.Class, e.From, e.To, strings.Join(e.Packages, ", "), e.From)
}

// StorageAssign binds a class to a registered mount. acknowledgeImpact
// is the owner's explicit consent to move a class off a mount that
// installed packages use; without it the daemon answers 409 and this
// returns a *StorageImpactError.
func (c *Client) StorageAssign(ctx context.Context, class, mountpoint, actor string, acknowledgeImpact bool) (storage.Assignment, error) {
	var out storage.Assignment
	body := map[string]any{"class": class, "mountpoint": mountpoint, "actor": actor}
	if acknowledgeImpact {
		body["acknowledge_impact"] = true
	}
	if err := c.postJSON(ctx, "/api/v1/storage/assignments", body, &out); err != nil {
		var status *StatusError
		if errors.As(err, &status) && status.Status == http.StatusConflict {
			var impact struct {
				Class    string   `json:"class"`
				From     string   `json:"from"`
				To       string   `json:"to"`
				Packages []string `json:"affected_packages"`
			}
			if json.Unmarshal([]byte(status.Body), &impact) == nil && len(impact.Packages) > 0 {
				return storage.Assignment{}, &StorageImpactError{Class: impact.Class, From: impact.From, To: impact.To, Packages: impact.Packages}
			}
		}
		return storage.Assignment{}, err
	}
	return out, nil
}

// StorageInUseError is the daemon's 409 refusal to unassign a class or
// unregister a mount while installed apps (or classes) still use it.
type StorageInUseError struct {
	Message  string
	Packages []string
	Classes  []string
}

func (e *StorageInUseError) Error() string { return e.Message }

func storageInUse(err error) error {
	var status *StatusError
	if errors.As(err, &status) && status.Status == http.StatusConflict {
		var body struct {
			Error    string   `json:"error"`
			Packages []string `json:"affected_packages"`
			Classes  []string `json:"classes"`
		}
		if json.Unmarshal([]byte(status.Body), &body) == nil && body.Error != "" {
			return &StorageInUseError{Message: body.Error, Packages: body.Packages, Classes: body.Classes}
		}
	}
	return err
}

// StorageUnassign drops a class binding (S288). It never touches data on
// the disk; the daemon refuses with a *StorageInUseError while installed
// apps still use the mount.
func (c *Client) StorageUnassign(ctx context.Context, class string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/api/v1/storage/assignments/"+url.PathEscape(class), nil)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	return storageInUse(c.roundTrip(req, http.StatusNoContent, nil))
}

// StorageUnregister drops a mount's registration (S288); same refusal and
// the same promise not to touch the disk.
func (c *Client) StorageUnregister(ctx context.Context, mountpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/api/v1/storage/registrations?mountpoint="+url.QueryEscape(mountpoint), nil)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	return storageInUse(c.roundTrip(req, http.StatusNoContent, nil))
}
