package exposure

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/docker"
	"homestead/internal/store"
)

func openOverrides(t *testing.T) (*Overrides, *audit.Service) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	trail := audit.New(s)
	return NewOverrides(s, trail), trail
}

func TestGrantFlow(t *testing.T) {
	overrides, trail := openOverrides(t)
	got, err := overrides.Grant("app.example.com", docker.ExposurePublic,
		"Launch day", "owner", false, nil)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if got.Host != "app.example.com" || got.To != docker.ExposurePublic ||
		got.Reason != "Launch day" || got.Actor != "owner" || got.Management ||
		got.CreatedAt.IsZero() {
		t.Fatalf("override = %+v", got)
	}
	fetched, err := overrides.Get("app.example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched != got {
		t.Fatalf("fetched = %+v, want %+v", fetched, got)
	}
	listed, err := overrides.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed = %+v", listed)
	}
	entries, err := trail.List(10)
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != ActionGrant ||
		!entries[0].Override || entries[0].OverrideReason != "Launch day" {
		t.Fatalf("entries = %+v", entries)
	}
	// Re-grant replaces with fresh audit.
	management, err := overrides.Grant("app.example.com", docker.ExposureOverlay,
		"Stepping back", "owner", true, nil)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if management.To != docker.ExposureOverlay || !management.Management {
		t.Fatalf("override = %+v", management)
	}
	entries, err = trail.List(10)
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if _, err := overrides.Get("missing.example.com"); !errors.Is(err, ErrNoOverride) {
		t.Fatalf("err = %v", err)
	}
}

func TestGrantRefusals(t *testing.T) {
	overrides, _ := openOverrides(t)
	closed := &EndpointExposure{Endpoint: "admin", Recommended: docker.ExposureLocal}
	cases := []struct {
		name     string
		host     docker.Hostname
		to       docker.Exposure
		reason   string
		actor    string
		endpoint *EndpointExposure
		want     string
	}{
		{"empty host", "", docker.ExposurePublic, "r", "a", nil, "1-253"},
		{"bad target", "app.example.com", "dmz", "r", "a", nil, "local, overlay, or public"},
		{"no reason", "app.example.com", docker.ExposurePublic, "", "a", nil, "1-500"},
		{"no actor", "app.example.com", docker.ExposurePublic, "r", "", nil, "actor must not be empty"},
		{"private public", "app.lan", docker.ExposurePublic, "r", "a", nil, "cannot go public"},
		{"ceiling holds", "app.example.com", docker.ExposurePublic, "r", "a", closed, "holds against overrides"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := overrides.Grant(tc.host, tc.to, tc.reason, tc.actor, false, tc.endpoint)
			if err == nil {
				t.Fatal("granted invalid override")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	// Refusals audit nothing.
	entries, err := overrides.audit.List(10)
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestRevoke(t *testing.T) {
	overrides, trail := openOverrides(t)
	if err := overrides.Revoke("ghost.example.com", "owner"); err != nil {
		t.Fatalf("revoke absent: %v", err)
	}
	if _, err := overrides.Grant("app.example.com", docker.ExposurePublic, "r", "owner", false, nil); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if err := overrides.Revoke("app.example.com", "owner"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := overrides.Get("app.example.com"); !errors.Is(err, ErrNoOverride) {
		t.Fatalf("err = %v", err)
	}
	if err := overrides.Revoke("app.example.com", ""); err == nil {
		t.Fatal("revoked without actor")
	}
	entries, err := trail.List(10)
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	revoked := false
	for _, entry := range entries {
		if entry.Action == ActionRevoke && !entry.Override {
			revoked = true
		}
	}
	if !revoked {
		t.Fatalf("entries = %+v", entries)
	}
	listed, err := overrides.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("listed = %+v", listed)
	}
}
