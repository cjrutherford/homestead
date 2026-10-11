package server

import "net/http"

// recordingMux is the TCP mux: a ServeMux that remembers every pattern
// registered on it, so the route-classification test (ADR-020, S255)
// enumerates the real registrations instead of a hand-maintained list.
// The Unix-socket mux is a plain ServeMux (root-only by file mode).
type recordingMux struct {
	*http.ServeMux
	patterns []string
}

func newRecordingMux() *recordingMux { return &recordingMux{ServeMux: http.NewServeMux()} }

func (m *recordingMux) Handle(pattern string, h http.Handler) {
	m.ServeMux.Handle(pattern, h)
	m.patterns = append(m.patterns, pattern)
}

func (m *recordingMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.Handle(pattern, http.HandlerFunc(h))
}

// Routes lists every pattern registered on the TCP mux so far.
func (s *Server) Routes() []string {
	return append([]string(nil), s.mux.patterns...)
}
