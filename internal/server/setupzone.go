package server

import (
	"context"
	"sync"
	"time"

	"homestead/internal/secret"
	"homestead/internal/setup"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// setupZoneResolver turns the setup wizard's provider and domain into the
// DNS provider and the real DNS zone (S328). The domain the owner chose
// names the dashboard, not the zone: for Cloudflare the zone is the apex
// Cloudflare holds, taken from the tunnel's stored zone once a plan was
// applied, and otherwise asked of Cloudflare with the stored token. A zone
// found is remembered for the domain, so the plan and the apply agree.
type setupZoneResolver struct {
	st      *store.Store
	wizards *wizard.Service
	secrets *secret.Store

	mu    sync.Mutex
	known map[string]string // domain -> zone
}

func newSetupZoneResolver(st *store.Store, secrets *secret.Store) *setupZoneResolver {
	return &setupZoneResolver{st: st, wizards: wizard.New(st), secrets: secrets, known: map[string]string{}}
}

func (r *setupZoneResolver) resolve() (provider, zone string) {
	state, err := r.wizards.Get(setup.WizardID)
	if err != nil {
		return "", ""
	}
	provider, _ = state.Payload["provider"].(string)
	name, _ := state.Payload["domain"].(string)
	if provider != "cloudflare" || name == "" || r.secrets == nil {
		return "", ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if z := r.known[name]; z != "" {
		return provider, z
	}
	var stored string
	if err := r.st.DB().QueryRow(`SELECT zone_name FROM tunnel_routes WHERE zone_name != '' LIMIT 1`).Scan(&stored); err == nil && stored != "" {
		r.known[name] = stored
		return provider, stored
	}
	token, err := tunnelToken(r.secrets)
	if err != nil {
		return "", ""
	}
	client, err := tunnelClient()
	if err != nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	found, err := client.ZoneFor(ctx, token, name)
	if err != nil || found.Name == "" {
		return "", ""
	}
	r.known[name] = found.Name
	return provider, found.Name
}
