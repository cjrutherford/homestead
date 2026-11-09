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

// MountGuide serves the built owner guide at /guide/ (S494). It needs a
// signed-in session (owner or person, see routeClasses and personRoutes), so
// it is registered as a GET pattern the boundary classifies; the handler
// itself is read-only static files.
func (s *Server) MountGuide(h http.Handler) {
	s.mux.HandleFunc("GET /guide/", h.ServeHTTP)
}
