package cert

import (
	"context"
	"fmt"
	"time"

	"homestead/internal/dns"
	"homestead/internal/domain"
	"homestead/internal/secret"
)

// DefaultProviders registers the supported DNS-01 providers against
// their production endpoints: Cloudflare, Route 53, and DigitalOcean.
// Manual stays out — it has no API to place through, so its DNS-01
// remains the owner pasting rendered values by hand.
func DefaultProviders() (*dns.Registry, error) {
	registry := dns.NewRegistry()
	cloudflare, err := dns.NewCloudflare(dns.CloudflareAPI)
	if err != nil {
		return nil, err
	}
	route53, err := dns.NewRoute53(dns.Route53Endpoint)
	if err != nil {
		return nil, err
	}
	digitalocean, err := dns.NewDigitalOcean(dns.DigitalOceanAPI)
	if err != nil {
		return nil, err
	}
	for _, provider := range []dns.Provider{cloudflare, route53, digitalocean} {
		if err := registry.Register(provider); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// DNS01 wires providers into issuance: it resolves the issuer's DNS
// provider, assembles its credentials from the secret store under the
// wizard's conventional names, and runs the S102 lifecycle around the
// ACME order. The ACME account key arrives per call — persistence
// belongs to the issuance layer above, which keeps it in secrets.
type DNS01 struct {
	Providers *dns.Registry
	Secrets   *secret.Store
	Client    *Client
	Probe     dns.ProbeFunc
	Interval  time.Duration
	// Issuances, when set, records the approved-issuance fingerprint and
	// chain of every certificate issued through IssueFor (S263).
	Issuances *Issuances
}

// placerFor resolves the issuer to a provider plus assembled credentials.
// Only ACME issuers with dns-01 challenges qualify; anything else is a
// caller error, never a fallback.
func (d *DNS01) placerFor(issuer Issuer, zone string) (Placer, error) {
	if err := issuer.Validate(); err != nil {
		return Placer{}, err
	}
	if issuer.Type != IssuerACME || issuer.Challenge != ChallengeDNS01 {
		return Placer{}, fmt.Errorf("cert: %s issuance is not dns-01", issuer.Name)
	}
	if d.Providers == nil {
		return Placer{}, fmt.Errorf("cert: provider registry must not be nil")
	}
	provider, err := d.Providers.Get(issuer.DNSProvider)
	if err != nil {
		return Placer{}, err
	}
	if d.Secrets == nil {
		return Placer{}, fmt.Errorf("cert: secret store must not be nil")
	}
	creds := dns.Credentials{}
	for _, field := range provider.RequiredCredentials() {
		value, err := d.Secrets.Get(domain.SecretName(provider.Name(), field.Name))
		if err != nil {
			return Placer{}, fmt.Errorf("cert: missing credential %q", field.Name)
		}
		creds[field.Name] = value
	}
	return Placer{Provider: provider, Zone: zone, Creds: creds}, nil
}

// Issued material: the PEM chain plus the fresh certificate key PEM.
type Issued struct {
	ChainPEM []byte
	KeyPEM   []byte
	// Fingerprint is the approved-issuance fingerprint of this issuance
	// (ADR-024): normalized names, DNS provider id, challenge method.
	Fingerprint Fingerprint
}

// Issue runs one dns-01 issuance: compute authorizations through the
// ACME order, place TXT values, await propagation, trigger validation,
// finalize, download the chain, and clean up. Cleanup runs even when
// issuance fails; a cleanup failure alongside success returns the
// material with the error, since dropping issued certificates would be
// the worse loss. agreeTerms records owner consent to the directory's
// terms (S106 obtains it); without agreement and published terms,
// issuance refuses.
func (d *DNS01) Issue(ctx context.Context, acct *Account, issuer Issuer, zone string, names []string, agreeTerms bool) (Issued, error) {
	return d.IssueFor(ctx, "", acct, issuer, zone, names, agreeTerms)
}

// IssueFor is Issue for a named certificate: on success the approved
// issuance fingerprint and public chain are stored under certName (when
// Issuances is set), so the renewal check (S263) can tell later whether
// the desired issuance is still the approved one. A recording failure
// returns the issued material with the error, like a cleanup failure.
func (d *DNS01) IssueFor(ctx context.Context, certName string, acct *Account, issuer Issuer, zone string, names []string, agreeTerms bool) (Issued, error) {
	issued, err := d.issue(ctx, acct, issuer, zone, names, agreeTerms)
	if len(issued.ChainPEM) == 0 {
		return issued, err
	}
	issued.Fingerprint = NewFingerprint(names, issuer.DNSProvider, string(issuer.Challenge))
	if certName != "" && d.Issuances != nil {
		if recErr := d.Issuances.Record(certName, issued.Fingerprint, issued.ChainPEM, time.Now()); recErr != nil && err == nil {
			err = fmt.Errorf("cert: issued but recording failed: %w", recErr)
		}
	}
	return issued, err
}

func (d *DNS01) issue(ctx context.Context, acct *Account, issuer Issuer, zone string, names []string, agreeTerms bool) (Issued, error) {
	if d.Client == nil {
		return Issued{}, fmt.Errorf("cert: ACME client must not be nil")
	}
	if len(names) == 0 {
		return Issued{}, fmt.Errorf("cert: names must not be empty")
	}
	placer, err := d.placerFor(issuer, zone)
	if err != nil {
		return Issued{}, err
	}
	if err := d.Client.ensureAccount(ctx, acct, agreeTerms); err != nil {
		return Issued{}, err
	}
	dir, err := d.Client.discover(ctx)
	if err != nil {
		return Issued{}, err
	}
	var order acmeOrder
	if err := d.Client.postJWS(ctx, acct, dir.NewOrder, map[string]any{
		"identifiers": orderIdentifiers(names),
	}, &order); err != nil {
		return Issued{}, err
	}
	challengeURLs, values, err := d.authorize(ctx, acct, order)
	if err != nil {
		return Issued{}, err
	}
	records, err := ChallengeRecords(zone, values)
	if err != nil {
		return Issued{}, err
	}
	if err := placer.Place(ctx, records); err != nil {
		return Issued{}, err
	}
	cleanupErr := func() error {
		if err := AwaitPropagation(ctx, d.Probe, zone, records, d.interval()); err != nil {
			return err
		}
		return nil
	}()
	issued, issueErr := d.finish(ctx, acct, order, names, challengeURLs)
	if cleanup := placer.Cleanup(ctx, records); cleanup != nil && issueErr == nil && cleanupErr == nil {
		return issued, fmt.Errorf("cert: issued but cleanup failed: %w", cleanup)
	} else if cleanup != nil && issueErr != nil {
		return Issued{}, fmt.Errorf("%v (cleanup also failed: %v)", issueErr, cleanup)
	}
	if cleanupErr != nil {
		_ = placer.Cleanup(ctx, records)
		return Issued{}, cleanupErr
	}
	return issued, issueErr
}

// orderIdentifiers renders the newOrder identifier set.
func orderIdentifiers(names []string) []map[string]string {
	out := make([]map[string]string, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]string{"type": "dns", "value": name})
	}
	return out
}

// authorize fetches every authorization, selects dns-01 challenges,
// and derives TXT values, returning the challenge URLs to trigger plus
// the name-to-value map for placement.
func (d *DNS01) authorize(ctx context.Context, acct *Account, order acmeOrder) ([]string, map[string]string, error) {
	var urls []string
	values := map[string]string{}
	for _, authzURL := range order.Authorizations {
		var authz acmeAuthorization
		if err := d.Client.get(ctx, authzURL, &authz); err != nil {
			return nil, nil, err
		}
		if authz.Status == "valid" {
			continue
		}
		var selected *acmeChallenge
		for i := range authz.Challenges {
			if authz.Challenges[i].Type == "dns-01" {
				selected = &authz.Challenges[i]
				break
			}
		}
		if selected == nil {
			return nil, nil, fmt.Errorf("cert: no dns-01 challenge for %s", authz.Identifier.Value)
		}
		keyAuthz, err := keyAuthorization(acct.Key, selected.Token)
		if err != nil {
			return nil, nil, err
		}
		values[authz.Identifier.Value] = dns01Value(keyAuthz)
		urls = append(urls, selected.URL)
	}
	return urls, values, nil
}

// finish triggers validation, polls authorizations, finalizes with a
// fresh CSR for names, polls the order, and downloads the chain,
// returning the chain plus the fresh certificate key.
func (d *DNS01) finish(ctx context.Context, acct *Account, order acmeOrder, names, challengeURLs []string) (Issued, error) {
	for _, url := range challengeURLs {
		if err := d.Client.postJWS(ctx, acct, url, nil, nil); err != nil {
			return Issued{}, fmt.Errorf("cert: trigger challenge: %w", err)
		}
	}
	for _, authzURL := range order.Authorizations {
		if _, err := d.Client.pollAuthz(ctx, authzURL, d.interval()); err != nil {
			return Issued{}, err
		}
	}
	der, keyPEM, err := finalizeCSR(names)
	if err != nil {
		return Issued{}, err
	}
	var finalized acmeOrder
	if err := d.Client.postJWS(ctx, acct, order.Finalize, map[string]any{
		"csr": b64url(der),
	}, &finalized); err != nil {
		return Issued{}, err
	}
	certURL, err := d.Client.pollOrder(ctx, acct, order.Finalize, d.interval())
	if err != nil {
		return Issued{}, err
	}
	chain, err := d.Client.download(ctx, acct, certURL)
	if err != nil {
		return Issued{}, err
	}
	return Issued{ChainPEM: chain, KeyPEM: keyPEM}, nil
}

// interval defaults the poll cadence when unset.
func (d *DNS01) interval() time.Duration {
	if d.Interval <= 0 {
		return 10 * time.Second
	}
	return d.Interval
}
