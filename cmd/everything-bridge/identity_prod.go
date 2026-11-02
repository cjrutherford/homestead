//go:build !everythingdev

package main

import (
	"net/http"

	"homestead/internal/everything"
)

// TODO(S367): real Authentik verification. ADR-053 requires it but does not
// yet specify the mechanism (forward-auth header from the edge vs OIDC), so
// none is implemented and the production build refuses to start.
func newIdentity(_ bool, _, _ string) (everything.Identity, func(*http.ServeMux), error) {
	return nil, nil, everything.ErrNoVerifier
}
