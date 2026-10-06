package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"homestead/internal/version"
)

func TestHealthEndpoint(t *testing.T) {
	s := New(DefaultAddr)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /healthz Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("GET /healthz invalid JSON: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("GET /healthz status = %q, want ok", body.Status)
	}
	if body.Version != version.Version {
		t.Fatalf("GET /healthz version = %q, want %q", body.Version, version.Version)
	}
}

func TestVersionEndpoint(t *testing.T) {
	s := New(DefaultAddr)
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /version status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("GET /version invalid JSON: %v", err)
	}
	if body.Version != version.Version {
		t.Fatalf("GET /version version = %q, want %q", body.Version, version.Version)
	}
}

func TestUnknownRouteNotFound(t *testing.T) {
	s := New(DefaultAddr)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /admin status = %d, want 404", rec.Code)
	}
}

func TestHealthRejectsPost(t *testing.T) {
	s := New(DefaultAddr)
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /healthz status = %d, want 405", rec.Code)
	}
}

func TestGracefulShutdown(t *testing.T) {
	s := New("127.0.0.1:0")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	baseURL := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx, ln, s.http, "test") }()

	getHealth := func() error {
		res, err := http.Get(baseURL + "/healthz")
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET /healthz status = %d, want 200", res.StatusCode)
		}
		return nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := getHealth(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned error after cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down within 10s")
	}

	if err := getHealth(); err == nil {
		t.Fatal("server still accepts connections after shutdown")
	}
}
