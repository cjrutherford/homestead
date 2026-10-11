package apiclient

import (
	"context"

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

// StorageAssign binds a class to a registered mount.
func (c *Client) StorageAssign(ctx context.Context, class, mountpoint, actor string) (storage.Assignment, error) {
	var out storage.Assignment
	body := map[string]any{"class": class, "mountpoint": mountpoint, "actor": actor}
	if err := c.postJSON(ctx, "/api/v1/storage/assignments", body, &out); err != nil {
		return storage.Assignment{}, err
	}
	return out, nil
}
