// Package server implements the homestead-server HTTP skeleton.
//
// S010 scope: health/version endpoints and graceful shutdown only. The daemon
// starts UNCLAIMED (see docs/architecture/BOOTSTRAP_TO_PUBLIC_TLS.md) and, per
// ADR-015, an unclaimed appliance exposes only health/identity/claim — the
// claim flow arrives in S019 and the administration API in later slices.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"homestead/internal/version"
)

// DefaultAddr binds loopback only: local-first (ADR-006) until the local
// security baseline (S222) explicitly configures wider surfaces.
const DefaultAddr = "127.0.0.1:8080"

// DefaultSocketPath is the root-only recovery socket (S021). The file
// mode (0600, set at listen time) is the access control: only root can
// connect, so endpoints mounted on the socket mux are root-only by
// OS enforcement rather than self-attestation.
const DefaultSocketPath = "/run/homestead/homestead.sock"

// ShutdownTimeout bounds graceful connection drain on SIGINT/SIGTERM.
const ShutdownTimeout = 5 * time.Second

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

type versionResponse struct {
	Version string `json:"version"`
}

// Server is the homestead-server HTTP control surface: a TCP mux for the
// API plus a Unix-socket mux for root-only endpoints (S021).
type Server struct {
	addr      string
	http      *http.Server
	mux       *http.ServeMux
	unix      *http.Server
	socketMux *http.ServeMux
	log       *slog.Logger
}

// New returns a Server serving addr. Pass DefaultAddr for local-only binding.
func New(addr string) *Server {
	s := &Server{addr: addr, log: slog.Default()}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /version", s.handleVersion)
	s.socketMux = http.NewServeMux()
	s.unix = &http.Server{
		Handler:           s.socketMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Handler exposes the TCP route mux for tests and future composition.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// SocketHandler exposes the Unix-socket mux for tests.
func (s *Server) SocketHandler() http.Handler { return s.unix.Handler }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, healthResponse{Status: "ok", Version: version.Version})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, versionResponse{Version: version.Version})
}

// Run serves addr until ctx is cancelled, then drains gracefully.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	return s.serve(ctx, ln, s.http, "tcp")
}

// RunUnix serves the socket mux on a root-only Unix socket (0600) until
// ctx is cancelled. A stale socket file is replaced; a missing parent
// directory fails loudly (S012 owns directories).
func (s *Server) RunUnix(ctx context.Context, socketPath string) error {
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("server: remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("server: listen unix %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("server: chmod socket: %w", err)
	}
	return s.serve(ctx, ln, s.unix, "unix")
}

func (s *Server) serve(ctx context.Context, ln net.Listener, hs *http.Server, name string) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("homestead-server listening", "listener", name, "addr", ln.Addr().String())
		if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		s.log.Info("homestead-server shutting down", "listener", name)
		if err := hs.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}
