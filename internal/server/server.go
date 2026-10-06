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
	"log/slog"
	"net"
	"net/http"
	"time"

	"homestead/internal/version"
)

// DefaultAddr binds loopback only: local-first (ADR-006) until the local
// security baseline (S222) explicitly configures wider surfaces.
const DefaultAddr = "127.0.0.1:8080"

// ShutdownTimeout bounds graceful connection drain on SIGINT/SIGTERM.
const ShutdownTimeout = 5 * time.Second

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

type versionResponse struct {
	Version string `json:"version"`
}

// Server is the homestead-server HTTP control surface.
type Server struct {
	addr string
	http *http.Server
	mux  *http.ServeMux
	log  *slog.Logger
}

// New returns a Server serving addr. Pass DefaultAddr for local-only binding.
func New(addr string) *Server {
	s := &Server{addr: addr, log: slog.Default()}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /version", s.handleVersion)
	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Handler exposes the route mux for tests and future composition.
func (s *Server) Handler() http.Handler { return s.http.Handler }

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
	return s.serve(ctx, ln)
}

func (s *Server) serve(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("homestead-server listening", "addr", ln.Addr().String())
		if err := s.http.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		s.log.Info("homestead-server shutting down")
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}
