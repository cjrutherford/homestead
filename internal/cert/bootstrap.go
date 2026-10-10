package cert

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// Readiness is the decided certificate strategy for the appliance
// identity (S226): which issuer serves the FQDN, over which
// challenge, and whether issuance needs public ingress. Deciding is
// the stage — fetching certificates belongs to later slices, which
// read the record instead of re-deriving it.
type Readiness struct {
	FQDN         string
	Strategy     IssuerType
	Challenge    ChallengeType
	DNSProvider  string
	NeedsIngress bool
	Detail       string
}

// ErrNoTLSReadiness reports no recorded TLS strategy yet.
var ErrNoTLSReadiness = errors.New("cert: TLS readiness not reached")

// Ingress reports verified public reachability per port, supplied by
// the caller (port verification lives in router territory): direct
// challenges decide on evidence, never optimism.
type Ingress struct {
	HTTP  bool
	HTTPS bool
}

// DecideReadiness selects the strategy for fqdn without requiring
// public app ingress to decide: private names stay internal, public
// names prefer DNS-01 where the provider speaks it (issuance before
// exposure), then direct challenges where ingress is verified, and
// finally the manual owner path. Wildcards skip direct challenges —
// only DNS-01 issues them. Unknown providers and malformed names
// refuse instead of defaulting somewhere unsafe.
func DecideReadiness(fqdn string, providers *dns.Registry, providerName string, ingress Ingress) (Readiness, error) {
	if err := validateDNSName("cert.fqdn", fqdn); err != nil {
		return Readiness{}, err
	}
	if docker.IsPrivateName(docker.Hostname(fqdn)) {
		return Readiness{
			FQDN: fqdn, Strategy: IssuerInternal,
			Detail: "private name: internal CA serves without public trust",
		}, nil
	}
	if providers == nil {
		return Readiness{}, fmt.Errorf("cert: TLS readiness needs a provider registry")
	}
	provider, err := providers.Get(providerName)
	if err != nil {
		return Readiness{}, err
	}
	wildcard := strings.HasPrefix(fqdn, "*.")
	capable := false
	for _, capability := range provider.Capabilities() {
		if capability == dns.CapabilityDNS01 {
			capable = true
			break
		}
	}
	if capable {
		return Readiness{
			FQDN: fqdn, Strategy: IssuerACME, Challenge: ChallengeDNS01, DNSProvider: provider.Name(),
			Detail: "DNS-01 issues before exposure: no ingress required",
		}, nil
	}
	if !wildcard && ingress.HTTP {
		return Readiness{
			FQDN: fqdn, Strategy: IssuerACME, Challenge: ChallengeHTTP01, NeedsIngress: true,
			Detail: "port 80 verified: direct HTTP challenge",
		}, nil
	}
	if !wildcard && ingress.HTTPS {
		return Readiness{
			FQDN: fqdn, Strategy: IssuerACME, Challenge: ChallengeTLSALPN01, NeedsIngress: true,
			Detail: "port 443 verified: direct TLS challenge",
		}, nil
	}
	if wildcard {
		return Readiness{
			FQDN: fqdn, Strategy: IssuerManual,
			Detail: "wildcard without DNS-01: owner supplies the certificate",
		}, nil
	}
	return Readiness{
		FQDN: fqdn, Strategy: IssuerManual,
		Detail: "no DNS-01 and no verified ingress: owner supplies the certificate",
	}, nil
}

// RecordReadiness stores the decided strategy: one row per
// appliance, replaced on re-deciding.
func RecordReadiness(st *store.Store, readiness Readiness) error {
	if readiness.FQDN == "" || !readiness.Strategy.Valid() {
		return fmt.Errorf("cert: cannot record empty readiness")
	}
	needs := 0
	if readiness.NeedsIngress {
		needs = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(
		`INSERT INTO tls_readiness (id, fqdn, strategy, challenge, dns_provider, needs_ingress, updated_at) VALUES (1, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET fqdn = excluded.fqdn, strategy = excluded.strategy, challenge = excluded.challenge, dns_provider = excluded.dns_provider, needs_ingress = excluded.needs_ingress, updated_at = excluded.updated_at`,
		readiness.FQDN, string(readiness.Strategy), string(readiness.Challenge), readiness.DNSProvider, needs, now,
	); err != nil {
		return fmt.Errorf("cert: record readiness: %w", err)
	}
	return nil
}

// CurrentReadiness returns the recorded TLS strategy, or
// ErrNoTLSReadiness when the stage was never reached.
func CurrentReadiness(st *store.Store) (Readiness, error) {
	var readiness Readiness
	var strategy, challenge string
	var needs int
	err := st.DB().QueryRow(`SELECT fqdn, strategy, challenge, dns_provider, needs_ingress FROM tls_readiness WHERE id = 1`).Scan(
		&readiness.FQDN, &strategy, &challenge, &readiness.DNSProvider, &needs,
	)
	if err == sql.ErrNoRows {
		return Readiness{}, ErrNoTLSReadiness
	}
	if err != nil {
		return Readiness{}, fmt.Errorf("cert: read readiness: %w", err)
	}
	readiness.Strategy = IssuerType(strategy)
	readiness.Challenge = ChallengeType(challenge)
	readiness.NeedsIngress = needs != 0
	if !readiness.Strategy.Valid() {
		return Readiness{}, fmt.Errorf("cert: recorded strategy %q unknown", strategy)
	}
	return readiness, nil
}

// EstablishReadiness decides and records in one call: the
// bootstrap-chain entry point. Later slices read the record.
func EstablishReadiness(st *store.Store, fqdn string, providers *dns.Registry, providerName string, ingress Ingress) (Readiness, error) {
	if st == nil {
		return Readiness{}, fmt.Errorf("cert: TLS readiness needs a store")
	}
	readiness, err := DecideReadiness(fqdn, providers, providerName, ingress)
	if err != nil {
		return Readiness{}, err
	}
	if err := RecordReadiness(st, readiness); err != nil {
		return Readiness{}, err
	}
	return readiness, nil
}
