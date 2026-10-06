package server

import "net/http"

// MountDashboard serves the production UI at / (S031). Longer API patterns
// keep precedence over the catch-all; the handler falls back to index.html
// for client-side routes.
func (s *Server) MountDashboard(h http.Handler) {
	s.mux.Handle("/", h)
}
