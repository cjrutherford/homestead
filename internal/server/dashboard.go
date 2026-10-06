package server

import "net/http"

// MountDashboard serves the production UI at / (S031). Longer API patterns
// keep precedence over the catch-all; the handler falls back to index.html
// for client-side routes. Deliberately method-less: explicit GET/HEAD
// patterns conflict with the API patterns in Go's mux; the handler itself
// serves GET/HEAD and rejects the rest. The parity matrix (S037) records
// this route as GET plus HEAD.
func (s *Server) MountDashboard(h http.Handler) {
	s.mux.Handle("/", h)
}
