package docker

import (
	"net/netip"
	"strings"
)

// privateSuffixes never survive public ACME: LAN conventions, RFC 2606
// documentation names, and localhost, for which Caddy itself falls back
// to internal issuance (matching what RecommendedTLS assigns
// explicitly).
var privateSuffixes = []string{
	"lan", "local", "localhost", "internal", "home.arpa",
	"test", "example", "invalid",
}

// IsPrivateName reports names public ACME can never issue for:
// single-label LAN names, private/reserved suffixes, and IP literals.
// Caddy automation needs a public name; anything private must terminate
// through the internal CA (or stay plain HTTP by explicit choice).
func IsPrivateName(host Hostname) bool {
	name := string(host)
	if _, err := netip.ParseAddr(name); err == nil {
		return true
	}
	if !strings.Contains(name, ".") {
		return true
	}
	for _, suffix := range privateSuffixes {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			return true
		}
	}
	return false
}

// DirectChallengesValid reports whether standard Caddy automation
// (http-01/tls-alpn-01 against a public CA) can serve a route: the name
// must be publicly issuable and the exposure must actually face the
// internet, since direct challenges prove control to the public side.
// Anything else needs DNS-01 (S102) or the internal CA (S104) —
// reachability beyond that is Caddy's to discover, not the model's to
// promise.
func DirectChallengesValid(host Hostname, exposure Exposure) bool {
	return !IsPrivateName(host) && exposure == ExposurePublic
}

// RecommendedTLS selects the TLS mode for a route: standard automation
// where direct challenges are valid, the internal CA everywhere else.
// Plain HTTP stays an explicit owner choice and is never recommended.
func RecommendedTLS(host Hostname, exposure Exposure) TLSMode {
	if DirectChallengesValid(host, exposure) {
		return TLSAuto
	}
	return TLSInternal
}
