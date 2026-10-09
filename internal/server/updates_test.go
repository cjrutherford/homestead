package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/docker"
	"homestead/internal/store"
)

// dockerFake serves Engine API subsets over a Unix socket: ping,
// version, info, containers, and images. Containers carry Compose
// labels so per-package scoping exercises for real; both fixture
// packages run at the given image so review and backup-gate paths
// see moves for each.
func dockerFake(t *testing.T, image string) *docker.Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Version":"29.1.2","ApiVersion":"1.52"}`))
	})
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Driver":"overlayfs"}`))
	})
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"abc","Names":["/demo-web-1"],"Image":"` + image + `","State":"running","Status":"Up","Labels":{"com.docker.compose.project":"demo","com.docker.compose.service":"web"}},{"Id":"def","Names":["/plain-web-1"],"Image":"` + image + `","State":"running","Status":"Up","Labels":{"com.docker.compose.project":"plain","com.docker.compose.service":"web"}}]`))
	})
	mux.HandleFunc("/images/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/networks", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	// Wait for accept: async startup otherwise races first contact.
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("docker fake never accepted: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return docker.NewClient(socket)
}

func updatesTestServer(t *testing.T, image string) *Server {
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
	entry := `name: demo
version: 1.0.0
title: Demo
blurb: Demo package.
description: Demonstrates updates.
category: testing
icon: mdi:test
services:
- name: web
  image: example/web:2.0.0
  exposes: web
requirements:
  memorymb: 64
  storage:
  - class: application
    minbytes: 1048576
  ports: [8080]
exposure:
  package: demo
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
backuppaths: [data]
`
	if err := os.WriteFile(filepath.Join(catalogDir, "demo.yaml"), []byte(entry), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	plain := `name: plain
version: 1.0.0
title: Plain
blurb: No backup needs.
description: Demonstrates backup-free updates.
category: testing
icon: mdi:test
services:
- name: web
  image: example/web:2.0.0
  exposes: web
requirements:
  memorymb: 64
  storage:
  - class: application
    minbytes: 1048576
  ports: [8080]
exposure:
  package: plain
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
`
	if err := os.WriteFile(filepath.Join(catalogDir, "plain.yaml"), []byte(plain), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	srv := New("127.0.0.1:0")
	srv.MountUpdates(s, catalogDir, dockerFake(t, image))
	return srv
}

func TestUpdatesList(t *testing.T) {
	srv := updatesTestServer(t, "example/web:1.0.0")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/updates", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var list []struct {
		Package   string `json:"package"`
		Installed bool   `json:"installed"`
		Plan      struct {
			Services []struct {
				Service string `json:"Service"`
				Current string `json:"Current"`
				Target  string `json:"Target"`
			} `json:"Services"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	var demo struct {
		Package   string `json:"package"`
		Installed bool   `json:"installed"`
		Plan      struct {
			Services []struct {
				Service string `json:"Service"`
				Current string `json:"Current"`
				Target  string `json:"Target"`
			} `json:"Services"`
		} `json:"plan"`
	}
	for _, entry := range list {
		if entry.Package == "demo" {
			demo = entry
		}
	}
	if !demo.Installed {
		t.Fatalf("list = %+v", list)
	}
	if len(demo.Plan.Services) != 1 || demo.Plan.Services[0].Target != "example/web:2.0.0" {
		t.Fatalf("plan = %+v", demo.Plan)
	}
	// Current images plan empty.
	srv = updatesTestServer(t, "example/web:2.0.0")
	recorder = httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/updates", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	var current []struct {
		Plan struct {
			Services []any `json:"Services"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(current[0].Plan.Services) != 0 {
		t.Fatalf("plan = %+v", current)
	}
}

func TestUpdatesApply(t *testing.T) {
	srv := updatesTestServer(t, "example/web:1.0.0")
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/updates/plain/apply", strings.NewReader(`{"backup":false}`))
	srv.Handler().ServeHTTP(recorder, req)
	// No docker daemon runs compose here: the route accepted a valid
	// reviewed plan and returned an operation; execution fails async.
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
	// Backup-required plans refuse without backup requested.
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/updates/demo/apply", strings.NewReader(`{}`))
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	// Current packages report no-op instead of empty operations.
	srv = updatesTestServer(t, "example/web:2.0.0")
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/updates/demo/apply", strings.NewReader(`{}`))
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "already current") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/updates/ghost/apply", strings.NewReader(`{}`))
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Platform components refuse on the application route: the
	// daemon cannot be compose-upped out from under itself.
	for _, name := range []string{"homestead-server", "homesteadctl"} {
		recorder = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/v1/updates/"+name+"/apply", strings.NewReader(`{}`))
		srv.Handler().ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s code = %d", name, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), "separately") {
			t.Fatalf("%s body = %s", name, recorder.Body.String())
		}
	}
}
