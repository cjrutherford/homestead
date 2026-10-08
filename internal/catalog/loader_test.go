package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const loaderFixture = `name: jellyfin
version: 10.10.3
title: Jellyfin
blurb: Media server you control.
description: Streams your media library.
category: media
icon: mdi:plex
services:
- name: jellyfin
  image: jellyfin/jellyfin:10.10.3
  exposes: web
requirements:
  memorymb: 1024
  storage:
  - class: bulk
    minbytes: 107374182400
  ports: [8096]
exposure:
  package: jellyfin
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
backuppaths:
- config
`

func writeEntry(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	writeEntry(t, dir, "jellyfin.yaml", loaderFixture)
	writeEntry(t, dir, "README.md", "# catalog")
	writeEntry(t, dir, ".hidden.yaml", "junk")
	writeEntry(t, dir, "notes.txt", "junk")
	catalog, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if catalog.Empty() {
		t.Fatal("empty catalog")
	}
	if names := catalog.Names(); len(names) != 1 || names[0] != "jellyfin" {
		t.Fatalf("names = %v", names)
	}
	manifest, err := catalog.Get("jellyfin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if manifest.Version != "10.10.3" || len(manifest.Services) != 1 ||
		manifest.Requirements.MemoryMB != 1024 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if _, err := catalog.Get("ghost"); !errors.As(err, &ErrUnknownPackage{}) {
		t.Fatalf("err = %v (%T)", err, err)
	}
	empty, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !empty.Empty() || len(empty.Names()) != 0 {
		t.Fatal("empty dir not empty catalog")
	}
	if _, err := Load(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("loaded missing dir")
	}
}

func TestLoadRejects(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		content  string
		want     string
	}{
		{"bad yaml", "bad.yaml", ":\tbad", "parse"},
		{"unknown field", "jellyfin.yaml", loaderFixture + "typo: 1\n", "field typo"},
		{"name drift", "other.yaml", loaderFixture, "names"},
		{"invalid manifest", "jellyfin.yaml", "name: jellyfin\n", "semantic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeEntry(t, dir, tc.filename, tc.content)
			_, err := Load(dir)
			if err == nil {
				t.Fatal("loaded bad entry")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}
