package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"homestead/internal/version"
)

func TestProbeURL(t *testing.T) {
	for listen, want := range map[string]string{
		"127.0.0.1:8080": "http://127.0.0.1:8080/healthz",
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9":      "http://127.0.0.1:9/healthz",
		"[::]:8080":      "http://[::1]:8080/healthz",
	} {
		if got, err := probeURL(listen); err != nil || got != want {
			t.Errorf("probeURL(%q) = %q, %v; want %q", listen, got, err, want)
		}
	}
	if _, err := probeURL("nonsense"); err == nil {
		t.Error("bad listen address must fail")
	}
}

func TestProbeOnce(t *testing.T) {
	var reported atomic.Value
	reported.Store(version.Version)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": reported.Load().(string)})
	}))
	defer ts.Close()
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	ctx := context.Background()
	if err := probeOnce(ctx, ts.URL, sock, version.Version); err != nil {
		t.Fatalf("healthy probe: %v", err)
	}
	reported.Store("9.9.9")
	if err := probeOnce(ctx, ts.URL, sock, version.Version); err == nil || !strings.Contains(err.Error(), "reports version") {
		t.Fatalf("version mismatch = %v", err)
	}
	reported.Store(version.Version)
	if err := probeOnce(ctx, ts.URL, filepath.Join(t.TempDir(), "none"), version.Version); err == nil {
		t.Fatal("missing socket must fail")
	}
}
