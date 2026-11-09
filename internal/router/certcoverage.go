package router

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Edge certificate coverage (S312). Cloudflare serves a public name over
// TLS only when one of the zone's certificates covers it. The free
// Universal SSL certificate covers the zone apex and names one label
// below it (`*.example.com`); deeper names such as
// `auth.homestead.example.com` need Advanced Certificate Manager: a
// certificate pack that lists them, or Total TLS. Without one the edge
// refuses the TLS handshake. Every plan that publishes a name on the
// tunnel checks it here and warns; deeper names stay a supported choice.

// CertificatePermission is the token permission that reads coverage.
const CertificatePermission = "Zone, SSL and Certificates, Read"

// NormalizeName lowercases a DNS name and drops surrounding space and a
// trailing dot.
func NormalizeName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// UniversalCovers reports whether the free Universal SSL certificate
// covers name in zone: the apex or exactly one label below it. Case and
// a trailing dot are ignored.
func UniversalCovers(name, zone string) bool {
	name, zone = NormalizeName(name), NormalizeName(zone)
	if zone == "" {
		return false
	}
	if name == zone {
		return true
	}
	rest, ok := strings.CutSuffix(name, "."+zone)
	return ok && rest != "" && !strings.Contains(rest, ".")
}

// HostCovers reports whether a certificate host entry covers name:
// exactly, or as a wildcard for a single label (`*.homestead.example.com`
// covers `auth.homestead.example.com`, not `a.b.homestead.example.com`).
func HostCovers(host, name string) bool {
	host, name = NormalizeName(host), NormalizeName(name)
	if host == name {
		return true
	}
	parent, ok := strings.CutPrefix(host, "*.")
	if !ok {
		return false
	}
	rest, ok := strings.CutSuffix(name, "."+parent)
	return ok && rest != "" && !strings.Contains(rest, ".")
}

// CertificatePack contains non-secret identity and coverage data.
type CertificatePack struct {
	ID           string
	Hosts        []string
	Certificates []PackCertificate
}

type PackCertificate struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ZoneCertificates is what a zone's certificates cover, as far as the
// token could read it.
type ZoneCertificates struct {
	Zone string
	// Hosts are the host entries of the zone's active certificate packs.
	Hosts              []string
	Packs              []CertificatePack
	TotalTLSUnreadable bool
	// TotalTLS is on: Cloudflare issues a certificate for every proxied
	// name, however deep.
	TotalTLS bool
	// Unreadable is set when the token could not read the packs or the
	// Total TLS setting; only the Universal SSL rule is then applied.
	Unreadable bool
}

// Coverage is the answer for one name.
type Coverage struct {
	Covered bool
	// By says which certificate covers it: "universal", "certificate
	// pack" or "total tls"; empty when none does.
	By string
}

// Covers answers for one name.
func (z ZoneCertificates) Covers(name string) Coverage {
	if UniversalCovers(name, z.Zone) {
		return Coverage{Covered: true, By: "universal"}
	}
	if z.Unreadable {
		return Coverage{}
	}
	for _, host := range z.Hosts {
		if HostCovers(host, name) {
			return Coverage{Covered: true, By: "certificate pack"}
		}
	}
	if z.TotalTLS {
		return Coverage{Covered: true, By: "total tls"}
	}
	return Coverage{}
}

// UncoveredText is the plain warning for a name no certificate covers
// (S312), with the one-level alternative.
func UncoveredText(name, zone string) string {
	name, zone = NormalizeName(name), NormalizeName(zone)
	label := name
	if i := strings.IndexByte(name, '.'); i > 0 {
		label = name[:i]
	}
	parent := strings.TrimPrefix(name, label+".")
	return fmt.Sprintf("Cloudflare has no certificate for %s yet: browsers will fail to connect. "+
		"Names more than one level below %s need Advanced Certificate Manager on the zone (a certificate for *.%s, or Total TLS). "+
		"Names one level below, such as %s.%s, are covered for free.", name, zone, parent, label, zone)
}

// UnreadableText is the warning when the token cannot read coverage.
func UnreadableText(zone string) string {
	return fmt.Sprintf("Homestead could not read the certificates on %s: the Cloudflare token needs the %s permission. "+
		"It checked only the free certificate's rule (the zone and names one level below it).", NormalizeName(zone), CertificatePermission)
}

// ZoneCertificates reads the zone's active certificate packs and its
// Total TLS setting. It never fails: a pack read the token is not
// allowed (or any other error) marks the answer Unreadable, so a plan
// still goes through on the Universal SSL rule. A cancelled context is
// the one error returned.
func (c *TunnelClient) ZoneCertificates(ctx context.Context, token string, zone Zone) (ZoneCertificates, error) {
	out := ZoneCertificates{Zone: zone.Name, Hosts: []string{}}
	if err := validateZoneID(zone.ID); err != nil {
		out.Unreadable = true
		return out, nil
	}
	var packs []struct {
		ID           string            `json:"id"`
		Certificates []PackCertificate `json:"certificates"`
		Status       string            `json:"status"`
		Hosts        []string          `json:"hosts"`
	}
	if err := c.call(ctx, token, http.MethodGet, "/zones/"+zone.ID+"/ssl/certificate_packs?per_page=100", nil, &packs); err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Unreadable = true
		return out, nil
	}
	// A full page may have more results; do not treat a partial read as authoritative.
	if len(packs) >= 100 {
		out.Unreadable = true
		return out, nil
	}
	for _, pack := range packs {
		if pack.Status == "" || pack.Status == "active" {
			out.Hosts = append(out.Hosts, pack.Hosts...)
			out.Packs = append(out.Packs, CertificatePack{ID: pack.ID, Hosts: pack.Hosts, Certificates: pack.Certificates})
		}
	}
	var total struct {
		Enabled bool `json:"enabled"`
	}
	// Preserve the historical coverage fallback while recording that the
	// Total TLS setting could not be verified for publication review.
	if err := c.call(ctx, token, http.MethodGet, "/zones/"+zone.ID+"/acm/total_tls", nil, &total); err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.TotalTLSUnreadable = true
		return out, nil
	}
	out.TotalTLS = total.Enabled
	return out, nil
}
