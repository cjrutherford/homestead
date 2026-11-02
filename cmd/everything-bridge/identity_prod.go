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
		RedirectURL: c.redirectURL, Origin: c.origins[0], BasePath: c.base, StaticDir: c.static, HTTPClient: hc}, store)
	if err != nil {
		return nil, nil, err
	}
	return o, o.Routes, nil
}
