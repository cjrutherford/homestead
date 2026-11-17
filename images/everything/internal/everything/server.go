package everything

import (
	"errors"
	"net/http"
)

// DataPath is the same-origin route the browser shim posts to.
const DataPath = "/_homestead/data"

// HealthPath is the unauthenticated liveness route: no data, never cached.
const HealthPath = "/_homestead/healthz"

func healthHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if store.Ping(r.Context()) != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	}
}

// NewServer builds the bridge's HTTP routes. extra may add routes (used only
// by the development build); it is nil in production.
func NewServer(store *Store, identity Identity, origins []string, extra func(*http.ServeMux)) (http.Handler, error) {
	return NewServerAt("", store, identity, origins, extra)
}

// NewServerAt is NewServer with the data route under base, an application
// path prefix such as "/apps/tasks" ("" for the root).
func NewServerAt(base string, store *Store, identity Identity, origins []string, extra func(*http.ServeMux)) (http.Handler, error) {
	h, err := NewHandler(store, identity, origins)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	h.(*handler).base = base
	mux.Handle(base+DataPath, h)
	mux.HandleFunc(base+ReadPrefix, h.(*handler).serveRead)
	mux.HandleFunc(base+ViewPrefix, h.(*handler).serveView)
	mux.HandleFunc(base+EventsPath, h.(*handler).serveEvents)
	mux.HandleFunc(base+HealthPath, healthHandler(store))
	if src, ok := identity.(SessionSource); ok {
		mux.HandleFunc(base+SessionPath, sessionHandler(src))
	}
	if extra != nil {
		extra(mux)
	}
	return mux, nil
}

// ErrNoVerifier is returned when no identity verifier is compiled in.
var ErrNoVerifier = errors.New("everything: no identity verifier available; real Authentik verification is not implemented (S367)")
