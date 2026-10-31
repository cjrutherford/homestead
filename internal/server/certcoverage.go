package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

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
	return coverageFromCertificates(certs, zone, names)
}

func coverageFromCertificates(certs router.ZoneCertificates, zone router.Zone, names []string) ([]publicNameCoverage, []string, error) {
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

// publicationCertificateState binds only active packs covering this name: their
// IDs, normalized matching hosts, and certificate IDs and statuses, plus
// the zone's Total TLS setting. Host duplicates, order, unrelated hosts and
// timestamps do not affect it.
func publicationCertificateState(certs router.ZoneCertificates, name string) (string, error) {
	if certs.Unreadable || certs.TotalTLSUnreadable {
		return "", fmt.Errorf("cannot review publication: Cloudflare certificate state is unavailable")
	}
	type packState struct {
		ID           string                   `json:"id"`
		Hosts        []string                 `json:"hosts"`
		Certificates []router.PackCertificate `json:"certificates"`
	}
	packs := []packState{}
	for _, pack := range certs.Packs {
		hosts := map[string]bool{}
		for _, host := range pack.Hosts {
			if router.HostCovers(host, name) {
				hosts[router.NormalizeName(host)] = true
			}
		}
		if len(hosts) == 0 {
			continue
		}
		if pack.ID == "" {
			return "", fmt.Errorf("cannot review publication: a relevant certificate pack has no identity")
		}
		p := packState{ID: pack.ID, Hosts: []string{}, Certificates: append([]router.PackCertificate{}, pack.Certificates...)}
		sort.Slice(p.Certificates, func(i, j int) bool {
			if p.Certificates[i].ID != p.Certificates[j].ID {
				return p.Certificates[i].ID < p.Certificates[j].ID
			}
			return p.Certificates[i].Status < p.Certificates[j].Status
		})
		for host := range hosts {
			p.Hosts = append(p.Hosts, host)
		}
		sort.Strings(p.Hosts)
		packs = append(packs, p)
	}
	sort.Slice(packs, func(i, j int) bool {
		if packs[i].ID != packs[j].ID {
			return packs[i].ID < packs[j].ID
		}
		return fmt.Sprint(packs[i].Hosts) < fmt.Sprint(packs[j].Hosts)
	})
	data, err := json.Marshal(struct {
		Version  int         `json:"version"`
		TotalTLS bool        `json:"total_tls"`
		Packs    []packState `json:"packs"`
	}{1, certs.TotalTLS, packs})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func publicationCoverage(ctx context.Context, client *router.TunnelClient, token string, zone router.Zone, name string) ([]publicNameCoverage, []string, string, error) {
	certs, err := client.ZoneCertificates(ctx, token, zone)
	if err != nil {
		return nil, nil, "", err
	}
	fingerprint, err := publicationCertificateState(certs, name)
	if err != nil {
		return nil, nil, "", err
	}
	coverage, warnings, err := coverageFromCertificates(certs, zone, []string{name})
	return coverage, warnings, fingerprint, err
}
