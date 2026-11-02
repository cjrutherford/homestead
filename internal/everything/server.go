package everything

import (
	"errors"
	"net/http"
)

// DataPath is the same-origin route the browser shim posts to.
const DataPath = "/_homestead/data"

// NewServer builds the bridge's HTTP routes. extra may add routes (used only
// by the development build); it is nil in production.
func NewServer(store *Store, identity Identity, origins []string, extra func(*http.ServeMux)) (http.Handler, error) {
	h, err := NewHandler(store, identity, origins)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle(DataPath, h)
	if extra != nil {
		extra(mux)
	}
	return mux, nil
}

// ErrNoVerifier is returned when no identity verifier is compiled in.
var ErrNoVerifier = errors.New("everything: no identity verifier available; real Authentik verification is not implemented (S367)")
