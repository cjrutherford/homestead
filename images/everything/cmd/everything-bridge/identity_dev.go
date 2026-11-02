//go:build everythingdev

package main

import (
	"errors"
	"net"
	"net/http"
	"os"

	"homestead/internal/everything"
)

// INSECURE development identity. Requires the -dev-identity flag, the
// HOMESTEAD_EVERYTHING_INSECURE_DEV environment acknowledgement, and a
// loopback listen address. Never build with -tags everythingdev for release.
func newIdentity(c config, _ *everything.Store) (everything.Identity, func(*http.ServeMux), error) {
	if !c.dev || os.Getenv("HOMESTEAD_EVERYTHING_INSECURE_DEV") != "i-understand-this-is-not-authentication" {
		return nil, nil, errors.New("everything-bridge: dev build needs -dev-identity and HOMESTEAD_EVERYTHING_INSECURE_DEV=i-understand-this-is-not-authentication")
	}
	host, _, err := net.SplitHostPort(c.listen)
	if err != nil {
		return nil, nil, err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, nil, errors.New("everything-bridge: dev identity only listens on a loopback IP")
	}
	d := everything.NewDevIdentity(c.app)
	if c.static != "" {
		d.Static(c.base, c.static)
	}
	return d, d.Routes, nil
}
