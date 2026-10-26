package server

import (
	"context"
	"errors"

	"homestead/internal/router"
	"homestead/internal/secret"
)

// publicNameCoverage is whether Cloudflare's edge has a certificate for
// one public name (S312): the plan warns before approval when it does
// not, and the tunnel and app public-address status show it as a lamp.
type publicNameCoverage struct {
	Hostname string `json:"hostname"`
	Covered  bool   `json:"covered"`
	// By names the certificate: "universal", "certificate pack" or
	// "total tls".
	By string `json:"by,omitempty"`
	// Checked is false when the token could not read the zone's
	// certificates, so only the free certificate's rule was applied.
	Checked bool `json:"checked"`
}

// certificateCoverage is the shared edge certificate check every plan
// that publishes a name on the tunnel runs. It never refuses: an
// uncovered name, or a token that cannot read coverage, is a warning
// the owner may approve past. Names two levels deep are a supported
// choice once the zone has a certificate for them.
func certificateCoverage(ctx context.Context, client *router.TunnelClient, token string, zone router.Zone, names []string) ([]publicNameCoverage, []string, error) {
	certs, err := client.ZoneCertificates(ctx, token, zone)
	if err != nil {
		return nil, nil, err
	}
	coverage := []publicNameCoverage{}
	warnings := []string{}
	if certs.Unreadable {
		warnings = append(warnings, router.UnreadableText(zone.Name))
	}
	for _, name := range names {
		got := certs.Covers(name)
		coverage = append(coverage, publicNameCoverage{Hostname: name, Covered: got.Covered, By: got.By, Checked: !certs.Unreadable})
		if !got.Covered {
			warnings = append(warnings, router.UncoveredText(name, zone.Name))
		}
	}
	return coverage, warnings, nil
}

// statusCoverage answers coverage for names on a status page: one zone
// lookup and one certificate read per zone, and nothing at all (an
// empty list) when there is no token or Cloudflare does not answer.
// Status never fails for it.
func statusCoverage(ctx context.Context, secrets *secret.Store, names []string) []publicNameCoverage {
	out := []publicNameCoverage{}
	if len(names) == 0 || secrets == nil {
		return out
	}
	token, err := tunnelToken(secrets)
	if err != nil {
		return out
	}
	client, err := tunnelClient()
	if err != nil {
		return out
	}
	zones := map[string]router.ZoneCertificates{}
	for _, name := range names {
		zone, err := client.ZoneFor(ctx, token, name)
		if errors.Is(err, router.ErrNoZone) || err != nil {
			continue
		}
		certs, seen := zones[zone.ID]
		if !seen {
			if certs, err = client.ZoneCertificates(ctx, token, zone); err != nil {
				return out
			}
			zones[zone.ID] = certs
		}
		got := certs.Covers(name)
		out = append(out, publicNameCoverage{Hostname: name, Covered: got.Covered, By: got.By, Checked: !certs.Unreadable})
	}
	return out
}
