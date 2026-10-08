package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/discover"
	"homestead/internal/store"
)

func assignMounts() discover.Storage {
	mount := func(point, fstype string, readOnly, accessible bool, free uint64) discover.Mount {
		return discover.Mount{Mountpoint: point, FSType: fstype, ReadOnly: readOnly, Accessible: accessible, FreeBytes: free}
	}
	return discover.Storage{Mounts: []discover.Mount{
		mount("/", "ext4", false, true, 10<<30),
		mount("/mnt/fast", "ext4", false, true, 100<<30),
		mount("/mnt/big", "ext4", false, true, 1000<<30),
		mount("/mnt/nas", "nfs", false, true, 5000<<30),
		mount("/mnt/ro", "ext4", true, true, 100<<30),
		mount("/mnt/stale", "ext4", false, false, 100<<30),
		mount("/proc", "proc", false, true, 0),
	}}
}

func TestSuitableMounts(t *testing.T) {
	discovered := assignMounts()
	// Databases skip network mounts but rank the rest by free space.
	db, err := SuitableMounts(ClassDatabase, discovered.Mounts, 0)
	if err != nil {
		t.Fatalf("SuitableMounts: %v", err)
	}
	var points []string
	for _, mount := range db {
		points = append(points, mount.Mountpoint)
	}
	want := []string{"/mnt/big", "/mnt/fast", "/"}
	if len(points) != len(want) {
		t.Fatalf("db = %v", points)
	}
	for i := range want {
		if points[i] != want[i] {
			t.Fatalf("db = %v, want %v", points, want)
		}
	}
	// Bulk welcomes the NAS first by size.
	bulk, err := SuitableMounts(ClassBulk, discovered.Mounts, 0)
	if err != nil {
		t.Fatalf("SuitableMounts: %v", err)
	}
	if len(bulk) == 0 || bulk[0].Mountpoint != "/mnt/nas" {
		t.Fatalf("bulk = %+v", bulk)
	}
	// Minimum free space filters small mounts.
	filtered, err := SuitableMounts(ClassBulk, discovered.Mounts, 2000<<30)
	if err != nil {
		t.Fatalf("SuitableMounts: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Mountpoint != "/mnt/nas" {
		t.Fatalf("filtered = %+v", filtered)
	}
	if _, err := SuitableMounts("archive", discovered.Mounts, 0); err == nil {
		t.Fatal("matched unknown class")
	}
}

func openAssignments(t *testing.T) (*Assignments, *Registry) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewAssignments(s), NewRegistry(s)
}

func registerForAssign(t *testing.T, registry *Registry, discovered discover.Storage, points ...string) {
	t.Helper()
	for _, point := range points {
		if _, err := registry.Register(point, strings.TrimPrefix(strings.ReplaceAll(point, "/", "-"), "-"), "owner", discovered); err != nil {
			t.Fatalf("Register %s: %v", point, err)
		}
	}
}

func TestAssignFlow(t *testing.T) {
	assignments, registry := openAssignments(t)
	discovered := assignMounts()
	registerForAssign(t, registry, discovered, "/mnt/big", "/mnt/nas")
	got, err := assignments.Assign(ClassDatabase, "/mnt/big", "owner", registry, discovered, 0)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if got.Class != ClassDatabase || got.Mountpoint != "/mnt/big" || got.Actor != "owner" || got.CreatedAt.IsZero() {
		t.Fatalf("assignment = %+v", got)
	}
	fetched, err := assignments.Get(ClassDatabase)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched != got {
		t.Fatalf("fetched = %+v, want %+v", fetched, got)
	}
	// Classes share mounts: bulk joins the same mount.
	if _, err := assignments.Assign(ClassBulk, "/mnt/big", "owner", registry, discovered, 0); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	listed, err := assignments.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 || listed[0].Class != ClassBulk || listed[1].Class != ClassDatabase {
		t.Fatalf("listed = %+v", listed)
	}
	// Migration to an unsuitable mount refuses; to a suitable one replaces.
	if _, err := assignments.Assign(ClassDatabase, "/mnt/nas", "owner", registry, discovered, 0); err == nil ||
		!strings.Contains(err.Error(), "not suitable") {
		t.Fatalf("err = %v", err)
	}
	moved, err := assignments.Assign(ClassBulk, "/mnt/nas", "owner", registry, discovered, 0)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if moved.Mountpoint != "/mnt/nas" {
		t.Fatalf("assignment = %+v", moved)
	}
	if _, err := assignments.Get(ClassCache); !errors.Is(err, ErrNoAssignment) {
		t.Fatalf("err = %v", err)
	}
}

func TestAssignRefusals(t *testing.T) {
	assignments, registry := openAssignments(t)
	discovered := assignMounts()
	registerForAssign(t, registry, discovered, "/mnt/big", "/mnt/nas")
	if _, err := assignments.Assign(ClassDatabase, "/mnt/fast", "owner", registry, discovered, 0); err == nil ||
		!strings.Contains(err.Error(), "unregistered") {
		t.Fatalf("err = %v", err)
	}
	if _, err := assignments.Assign(ClassDatabase, "/mnt/nas", "owner", registry, discovered, 0); err == nil ||
		!strings.Contains(err.Error(), "not suitable") {
		t.Fatalf("err = %v", err)
	}
	if _, err := assignments.Assign(ClassDatabase, "/mnt/big", "", registry, discovered, 0); err == nil {
		t.Fatal("assigned without actor")
	}
	if _, err := assignments.Assign("archive", "/mnt/big", "owner", registry, discovered, 0); err == nil {
		t.Fatal("assigned unknown class")
	}
	if _, err := assignments.Assign(ClassBulk, "/mnt/big", "owner", registry, discovered, 5000<<30); err == nil {
		t.Fatal("assigned beyond free space")
	}
}

func TestUnassign(t *testing.T) {
	assignments, registry := openAssignments(t)
	discovered := assignMounts()
	if err := assignments.Unassign(ClassDatabase); err != nil {
		t.Fatalf("unassign absent: %v", err)
	}
	registerForAssign(t, registry, discovered, "/mnt/big")
	if _, err := assignments.Assign(ClassDatabase, "/mnt/big", "owner", registry, discovered, 0); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if err := assignments.Unassign(ClassDatabase); err != nil {
		t.Fatalf("Unassign: %v", err)
	}
	if _, err := assignments.Get(ClassDatabase); !errors.Is(err, ErrNoAssignment) {
		t.Fatalf("err = %v", err)
	}
}
