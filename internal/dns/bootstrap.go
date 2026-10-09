package dns

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"homestead/internal/discover"
	"homestead/internal/store"
)

// SplitDNS is the established split-horizon state (S224): the zone
// and FQDN served locally at the appliance address, the rewrites
// ensured, and the public-audience changes held back as proof the
// appliance stayed non-public. PublicVerified reports whether the
// public side was actually checked — providers without an API skip
// remote proof, since nothing can be written through them and nothing
// could have leaked either. The overlay view belongs to S225: no
// overlay DNS provider exists to receive one yet.
type SplitDNS struct {
	Zone           string
	FQDN           string
	LAN            string
	LocalApplied   []Rewrite
	PublicHeld     []Change
	PublicVerified bool
}

// ErrNoSplitDNS reports no recorded split-DNS state yet.
var ErrNoSplitDNS = errors.New("dns: split DNS not established")

// validBase checks the Homestead subdomain base: one lowercase label,
// the namespace platform services hang under.
func validBase(base string) error {
	if len(base) == 0 || len(base) > 63 {
		return fmt.Errorf("dns: base must be 1-63 characters")
	}
	if strings.ToLower(base) != base {
		return fmt.Errorf("dns: base must be lowercase")
	}
	for _, r := range base {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("dns: invalid base %q", base)
		}
	}
	if strings.HasPrefix(base, "-") || strings.HasSuffix(base, "-") {
		return fmt.Errorf("dns: invalid base %q", base)
	}
	return nil
}

// servableLAN reports addresses fit to answer locally: IPv4 unicast,
// neither loopback, link-local, unspecified, nor multicast.
func servableLAN(ip netip.Addr) bool {
	return ip.Is4() && !ip.IsUnspecified() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsMulticast()
}

// applianceLAN selects the appliance address: the first servable
// IPv4 on the default interface, else the first servable anywhere in
// index order. No usable address fails — answering with a wrong one
// would point the LAN at a stranger.
func applianceLAN(network discover.Network) (string, error) {
	ordered := make([]discover.Interface, 0, len(network.Interfaces))
	for _, iface := range network.Interfaces {
		if iface.Name == network.DefaultInterface {
			ordered = append([]discover.Interface{iface}, ordered...)
			continue
		}
		ordered = append(ordered, iface)
	}
	for _, iface := range ordered {
		if !iface.Up || iface.Loopback {
			continue
		}
		for _, address := range iface.Addresses {
			ip, err := netip.ParseAddr(address.IP)
			if err != nil || !servableLAN(ip) {
				continue
			}
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("dns: no usable LAN address")
}

// EstablishSplitDNS reaches SPLIT_DNS_READY without going public:
// the zone intent carries a local view (FQDN to the appliance LAN
// address) and no public view, local answers are ensured in local
// DNS, and the public audience is previewed — never applied — with
// its changes held as proof nothing went public. Re-running
// converges: present answers stay, the stage row replaces.
func EstablishSplitDNS(ctx context.Context, st *store.Store, local *Client, provider Provider, creds Credentials, zoneName, base string, network discover.Network) (SplitDNS, error) {
	if st == nil {
		return SplitDNS{}, fmt.Errorf("dns: split DNS needs a store")
	}
	if local == nil {
		return SplitDNS{}, fmt.Errorf("dns: split DNS needs local DNS")
	}
	if provider == nil {
		return SplitDNS{}, fmt.Errorf("dns: split DNS needs a provider")
	}
	if err := validateDNSName("zone.name", zoneName, false); err != nil {
		return SplitDNS{}, err
	}
	if err := validBase(base); err != nil {
		return SplitDNS{}, err
	}
	lan, err := applianceLAN(network)
	if err != nil {
		return SplitDNS{}, err
	}
	zone := Zone{
		Name: zoneName,
		Views: []View{{Name: "bootstrap-local", Audience: AudienceLocal, Records: []Record{
			{Name: base, Type: RecordA, Value: lan, TTLSeconds: 300},
		}}},
	}
	if err := zone.Validate(); err != nil {
		return SplitDNS{}, err
	}
	effective, err := EffectiveRecords(zone, AudienceLocal)
	if err != nil {
		return SplitDNS{}, err
	}
	desired, err := EffectiveRewrites(zoneName, effective)
	if err != nil {
		return SplitDNS{}, err
	}
	observed, err := local.ListRewrites(ctx)
	if err != nil {
		return SplitDNS{}, fmt.Errorf("dns: split DNS list local: %w", err)
	}
	present := map[Rewrite]bool{}
	for _, rewrite := range observed {
		present[rewrite] = true
	}
	result := SplitDNS{Zone: zoneName, FQDN: fqdn(zoneName, base), LAN: lan}
	for _, rewrite := range desired {
		if present[rewrite] {
			continue
		}
		if err := local.AddRewrite(ctx, rewrite); err != nil {
			return SplitDNS{}, fmt.Errorf("dns: split DNS add local: %w", err)
		}
		result.LocalApplied = append(result.LocalApplied, rewrite)
	}
	// The public audience previews only: baseline carries nothing
	// public, so every observed public record diffs as a held delete
	// and the applicable set stays empty by construction. API-less
	// providers skip even the listing — there is nothing to prove
	// against and nothing could have been written.
	if len(provider.Capabilities()) != 0 {
		public, err := provider.ListRecords(ctx, zoneName, creds)
		if err != nil {
			return SplitDNS{}, fmt.Errorf("dns: split DNS list public: %w", err)
		}
		preview, err := PreviewZone(zone, AudiencePublic, public, false)
		if err != nil {
			return SplitDNS{}, err
		}
		if len(preview.Apply) != 0 {
			return SplitDNS{}, fmt.Errorf("dns: split DNS would go public: %v", preview.Apply)
		}
		result.PublicVerified = true
		result.PublicHeld = preview.Held
	}
	if result.PublicHeld == nil {
		result.PublicHeld = []Change{}
	}
	if result.LocalApplied == nil {
		result.LocalApplied = []Rewrite{}
	}
	if err := recordSplitDNS(st, result); err != nil {
		return SplitDNS{}, err
	}
	return result, nil
}

// recordSplitDNS stores the established state: one row per appliance,
// replaced on re-establishing.
func recordSplitDNS(st *store.Store, result SplitDNS) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(
		`INSERT INTO split_dns_state (id, zone, fqdn, lan, updated_at) VALUES (1, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET zone = excluded.zone, fqdn = excluded.fqdn, lan = excluded.lan, updated_at = excluded.updated_at`,
		result.Zone, result.FQDN, result.LAN, now,
	); err != nil {
		return fmt.Errorf("dns: record split DNS: %w", err)
	}
	return nil
}

// CurrentSplitDNS returns the recorded split-DNS state, or
// ErrNoSplitDNS when the appliance still serves local names only.
// Run-scoped fields (applied, held) read empty: the table records
// the decision, not the run.
func CurrentSplitDNS(st *store.Store) (SplitDNS, error) {
	var result SplitDNS
	err := st.DB().QueryRow(`SELECT zone, fqdn, lan FROM split_dns_state WHERE id = 1`).Scan(
		&result.Zone, &result.FQDN, &result.LAN,
	)
	if err == sql.ErrNoRows {
		return SplitDNS{}, ErrNoSplitDNS
	}
	if err != nil {
		return SplitDNS{}, fmt.Errorf("dns: read split DNS: %w", err)
	}
	return result, nil
}
