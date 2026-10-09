package overlay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"homestead/internal/dns"
	"homestead/internal/store"
)

// Readiness is the overlay stage verdict (S225): the provider reached
// with live node addresses, or a local-only skip. View carries the
// validated overlay-audience intent (FQDN to node addresses) for the
// zone — reviewed, not applied: no overlay DNS provider exists to
// receive it yet, so intent waits while readiness proceeds.
type Readiness struct {
	Provider  string
	Addresses []string
	Skipped   bool
	View      dns.View
}

// State is the recorded overlay stage: provider plus node addresses.
// Skipped stages record provider "none" with no addresses, so later
// slices distinguish considered from never visited.
type State struct {
	Provider  string
	Addresses []string
}

// ErrNoOverlay reports no recorded overlay stage yet.
var ErrNoOverlay = errors.New("overlay: stage not reached")

// EstablishOverlay reaches OVERLAY_READY: the wizard selection names
// a provider, connectivity validates live, node addresses resolve,
// and the overlay view intent validates against the zone. Local-only
// selections skip with a recorded "none" — optional means optional.
// Re-running converges on the latest live state.
func EstablishOverlay(ctx context.Context, st *store.Store, wiz *RemoteWizard, zone, base string) (Readiness, error) {
	if st == nil {
		return Readiness{}, fmt.Errorf("overlay: bootstrap needs a store")
	}
	if wiz == nil {
		return Readiness{}, fmt.Errorf("overlay: bootstrap needs the remote wizard")
	}
	plan, err := wiz.Plan()
	if err != nil {
		return Readiness{}, err
	}
	if plan.Config.Provider == ProviderNone {
		if err := recordOverlay(st, string(ProviderNone), nil); err != nil {
			return Readiness{}, err
		}
		return Readiness{Provider: string(ProviderNone), Skipped: true}, nil
	}
	if err := wiz.ValidateConnectivity(ctx); err != nil {
		return Readiness{}, err
	}
	network, err := Status(ctx)
	if err != nil {
		return Readiness{}, err
	}
	var addresses []string
	for _, addr := range network.Addresses {
		if addr.IsValid() && !addr.IsUnspecified() {
			addresses = append(addresses, addr.String())
		}
	}
	if len(addresses) == 0 {
		return Readiness{}, fmt.Errorf("overlay: node online without usable addresses")
	}
	view, err := overlayView(zone, base, network)
	if err != nil {
		return Readiness{}, err
	}
	if err := recordOverlay(st, string(plan.Config.Provider), addresses); err != nil {
		return Readiness{}, err
	}
	return Readiness{Provider: string(plan.Config.Provider), Addresses: addresses, View: view}, nil
}

// overlayView builds the validated overlay-audience intent for the
// zone: the FQDN answering node addresses (A for IPv4, AAAA for
// IPv6). Validation runs through the zone model so malformed intent
// fails here, not at some future application point.
func overlayView(zoneName, base string, network Network) (dns.View, error) {
	view := dns.View{Name: "bootstrap-overlay", Audience: dns.AudienceOverlay}
	for _, addr := range network.Addresses {
		if !addr.IsValid() || addr.IsUnspecified() {
			continue
		}
		recordType := dns.RecordA
		if addr.Is6() {
			recordType = dns.RecordAAAA
		}
		view.Records = append(view.Records, dns.Record{
			Name: base, Type: recordType, Value: addr.String(), TTLSeconds: 300,
		})
	}
	if len(view.Records) == 0 {
		return dns.View{}, fmt.Errorf("overlay: no usable node addresses for view")
	}
	zone := dns.Zone{Name: zoneName, Views: []dns.View{view}}
	if err := zone.Validate(); err != nil {
		return dns.View{}, err
	}
	return view, nil
}

// recordOverlay stores the reached stage, replacing prior rows.
func recordOverlay(st *store.Store, provider string, addresses []string) error {
	if addresses == nil {
		addresses = []string{}
	}
	encoded, err := json.Marshal(addresses)
	if err != nil {
		return fmt.Errorf("overlay: encode addresses: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(
		`INSERT INTO overlay_state (id, provider, addresses, updated_at) VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET provider = excluded.provider, addresses = excluded.addresses, updated_at = excluded.updated_at`,
		provider, string(encoded), now,
	); err != nil {
		return fmt.Errorf("overlay: record stage: %w", err)
	}
	return nil
}

// CurrentOverlay returns the recorded overlay stage, or ErrNoOverlay
// when the stage was never reached.
func CurrentOverlay(st *store.Store) (State, error) {
	var state State
	var encoded string
	err := st.DB().QueryRow(`SELECT provider, addresses FROM overlay_state WHERE id = 1`).Scan(
		&state.Provider, &encoded,
	)
	if err == sql.ErrNoRows {
		return State{}, ErrNoOverlay
	}
	if err != nil {
		return State{}, fmt.Errorf("overlay: read stage: %w", err)
	}
	if err := json.Unmarshal([]byte(encoded), &state.Addresses); err != nil {
		return State{}, fmt.Errorf("overlay: decode addresses: %w", err)
	}
	if state.Addresses == nil {
		state.Addresses = []string{}
	}
	return state, nil
}
