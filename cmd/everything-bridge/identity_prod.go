//go:build !everythingdev

package main

import (
	"errors"
	"net/http"

	"homestead/internal/everything"
)

// newIdentity builds the production OIDC identity. Any missing setting is an
// error, so an incompletely configured bridge refuses to start.
func newIdentity(c config, store *everything.Store) (everything.Identity, func(*http.ServeMux), error) {
	if c.await && c.issuer == "" && c.clientID == "" {
		return awaitingSignIn{}, func(mux *http.ServeMux) {
			mux.HandleFunc(c.base+"/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("Sign-in is not set up for this app yet. Ask the owner to set up sign-in with Authentik on the app's page in Homestead.\n"))
			})
		}, nil
	}
	if len(c.origins) != 1 {
		return nil, nil, errors.New("everything-bridge: exactly one -origin is required")
	}
	if c.secretFile == "" {
		return nil, nil, errors.New("everything-bridge: -oidc-client-secret-file is required")
	}
	secret, err := everything.ReadSecretFile(c.secretFile)
	if err != nil {
		return nil, nil, err
	}
	hc, err := everything.ClientWithCA(c.caFile)
	if err != nil {
		return nil, nil, err
	}
	o, err := everything.NewOIDC(everything.OIDCConfig{App: c.app, Issuer: c.issuer, ClientID: c.clientID, ClientSecret: secret,
		RedirectURL: c.redirectURL, Origin: c.origins[0], BasePath: c.base, StaticDir: c.static, HTTPClient: hc, Groups: c.groups}, store)
	if err != nil {
		return nil, nil, err
	}
	return o, o.Routes, nil
}

// awaitingSignIn authenticates nobody: until sign-in is applied the bridge
// answers 503 and the data route refuses every request.
type awaitingSignIn struct{}

func (awaitingSignIn) Authenticate(*http.Request) (everything.Scope, error) {
	return everything.Scope{}, errors.New("sign-in is not set up")
}
func (awaitingSignIn) CheckCSRF(*http.Request) bool { return false }
