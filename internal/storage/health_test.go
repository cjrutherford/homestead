package storage

import (
	"strings"
	"testing"

	"homestead/internal/discover"
)

func healthDiscovery() discover.Storage {
	mount := func(point string, readOnly, accessible bool, total, free uint64) discover.Mount {
		return discover.Mount{Mountpoint: point, FSType: "ext4", ReadOnly: readOnly, Accessible: accessible, TotalBytes: total, FreeBytes: free}
	}
	return discover.Storage{Mounts: []discover.Mount{
		mount("/mnt/ok", false, true, 100, 60),
		mount("/mnt/ro", true, true, 100, 60),
		mount("/mnt/stale", false, false, 0, 0),
		mount("/mnt/full", false, true, 100, 5),
	}}
}

func TestCheckMount(t *testing.T) {
	discovered := healthDiscovery()
	healthy := CheckMount("/mnt/ok", discovered)
	if healthy.Status != HealthHealthy || healthy.FreeBytes != 60 || healthy.TotalBytes != 100 || healthy.UsePercent != 40 {
		t.Fatalf("healthy = %+v", healthy)
	}
	readonly := CheckMount("/mnt/ro", discovered)
	if readonly.Status != HealthDegraded || !strings.Contains(readonly.Detail, "read-only") {
		t.Fatalf("readonly = %+v", readonly)
	}
	stale := CheckMount("/mnt/stale", discovered)
	if stale.Status != HealthDegraded || !strings.Contains(stale.Detail, "stale") {
		t.Fatalf("stale = %+v", stale)
	}
	full := CheckMount("/mnt/full", discovered)
	if full.Status != HealthDegraded || full.UsePercent != 95 || !strings.Contains(full.Detail, "5% free") {
		t.Fatalf("full = %+v", full)
	}
	missing := CheckMount("/mnt/gone", discovered)
	if missing.Status != HealthMissing {
		t.Fatalf("missing = %+v", missing)
	}
}

func TestCheckAll(t *testing.T) {
	registrations := []Registration{
		{Mountpoint: "/mnt/ok", Label: "ok"},
		{Mountpoint: "/mnt/ro", Label: "ro"},
		{Mountpoint: "/mnt/gone", Label: "gone"},
	}
	health, summary := CheckAll(registrations, healthDiscovery())
	if len(health) != 3 {
		t.Fatalf("health = %+v", health)
	}
	if summary.Healthy != 1 || summary.Degraded != 1 || summary.Missing != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Overall != HealthMissing {
		t.Fatalf("overall = %q", summary.Overall)
	}
	degradedOnly, summary := CheckAll(registrations[:2], healthDiscovery())
	_ = degradedOnly
	if summary.Overall != HealthDegraded {
		t.Fatalf("overall = %q", summary.Overall)
	}
	healthyOnly, summary := CheckAll(registrations[:1], healthDiscovery())
	_ = healthyOnly
	if summary.Overall != HealthHealthy {
		t.Fatalf("overall = %q", summary.Overall)
	}
	empty, summary := CheckAll(nil, healthDiscovery())
	if len(empty) != 0 || summary.Overall != HealthHealthy {
		t.Fatalf("empty = %+v %+v", empty, summary)
	}
}
