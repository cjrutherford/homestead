package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/store"
)

const driftDemoManifest = `name: demo
version: 1.0.0
title: Demo
blurb: Demo package.
description: Demonstrates drift review.
category: testing
icon: mdi:test
services:
- name: web
  image: example/web:1.0.0
  exposes: web
requirements:
  memorymb: 64
  storage:
  - class: application
    minbytes: 1048576
  ports: [18080]
exposure:
  package: demo
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
backuppaths: [data]
`

// driftTestServer mounts drift routes over a scratch store and
// catalog, stubbing stack directories into a temp dir. It returns the
// server, store, and stack dir; files start recorded-then-edited so
// review always has drift to show.
func driftTestServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	catalogDir := filepath.Join(dir, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(catalogDir, "demo.yaml"), []byte(driftDemoManifest), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stackDir := filepath.Join(dir, "stacks", "demo")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services: {managed: true}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := catalog.RecordFingerprints(s, "demo", stackDir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services: {expert: true}\n"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}
	old := driftDirFor
	driftDirFor = func(name string) string {
		if name == "demo" {
			return stackDir
		}
		return old(name)
	}
	t.Cleanup(func() { driftDirFor = old })
	srv := New("127.0.0.1:0")
	srv.MountDrift(s, catalogDir)
	return srv, s, stackDir
}

func TestDriftReview(t *testing.T) {
	srv, _, _ := driftTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/demo/drift", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var review []struct {
		Path     string `json:"path"`
		Kind     string `json:"kind"`
		Recorded string `json:"recorded"`
		Current  string `json:"current"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &review); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(review) != 1 || review[0].Path != "compose.yaml" || review[0].Kind != "modified" {
		t.Fatalf("review = %+v", review)
	}
	if review[0].Recorded != "services: {managed: true}\n" || review[0].Current != "services: {expert: true}\n" {
		t.Fatalf("review = %+v", review)
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/ghost/drift", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestDriftAccept(t *testing.T) {
	srv, s, stackDir := driftTestServer(t)
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/drift", map[string]any{"action": "accept"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "compose.yaml") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	// Adoption ends the drift.
	review, err := catalog.Review(s, "demo", stackDir)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(review) != 0 {
		t.Fatalf("review = %+v", review)
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/drift", map[string]any{"action": "merge"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestDriftRestore(t *testing.T) {
	srv, s, stackDir := driftTestServer(t)
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/drift", map[string]any{"action": "restore"})
	// No daemon here: the operation starts, then fails at up — but
	// the route accepted reviewed drift and returned an operation,
	// and restore-files already rewrote the managed bytes.
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var started struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if started.ID == 0 {
		t.Fatal("no operation id")
	}
	// Execution runs async: poll until restore-files rewrote the
	// managed bytes (up fails without a daemon, afterwards).
	deadline := time.Now().Add(10 * time.Second)
	for {
		review, err := catalog.Review(s, "demo", stackDir)
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if len(review) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("review = %+v", review)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Clean stacks report no-op instead of empty operations.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/drift", map[string]any{"action": "restore"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "no drift") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}
