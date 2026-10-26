package appsso

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"homestead/internal/catalog"
)

// Apps with their own allow-list of host names (S313). Nextcloud refuses
// any Host not in its trusted_domains, so a public name added later must
// be added there too, and a removed one taken out. The other catalog
// apps that serve on the edge (Immich, Jellyfin, Authentik) answer on any
// Host; docs/DEFERRED.md records what they leave out.

// HostSettings says, in the owner's words, what TrustHosts changes for
// the app when names are added or removed; nil when the app keeps no
// allow-list. A plan shows it before approval.
func HostSettings(m catalog.Manifest, add, remove []string) []string {
	if m.Name != "nextcloud" {
		return nil
	}
	var out []string
	if len(add) > 0 {
		out = append(out, fmt.Sprintf("%s: trusted_domains gets %s (occ), after its own install finishes.", m.Title, strings.Join(add, ", ")))
	}
	if len(remove) > 0 {
		out = append(out, fmt.Sprintf("%s: %s is taken out of trusted_domains (occ).", m.Title, strings.Join(remove, ", ")))
	}
	return out
}

// TrustHosts adds names to, and removes names from, the app's own
// host allow-list. Apps without one are left alone. For Nextcloud it
// waits, bounded, for a Nextcloud that is installing itself, then uses
// occ; a name already listed is not added twice.
func TrustHosts(ctx context.Context, m catalog.Manifest, address string, add, remove []string) error {
	if m.Name != "nextcloud" {
		return nil
	}
	nc := nextcloud{}
	t := Target{Manifest: m, Container: container(m, "nextcloud"), Address: address}
	if err := nc.WaitSetUp(ctx, t); err != nil {
		return err
	}
	current, err := nc.trustedDomains(ctx, t)
	if err != nil {
		return err
	}
	for _, name := range remove {
		for index, value := range current {
			if strings.EqualFold(value, name) {
				if _, err := nc.occ(ctx, t, "", "config:system:delete", "trusted_domains", strconv.Itoa(index)); err != nil {
					return fmt.Errorf("appsso: take %s out of Nextcloud's trusted domains: %w", name, err)
				}
				delete(current, index)
			}
		}
	}
	next := 0
	for index := range current {
		if index >= next {
			next = index + 1
		}
	}
	for _, name := range add {
		listed := false
		for _, value := range current {
			listed = listed || strings.EqualFold(value, name)
		}
		if listed {
			continue
		}
		if _, err := nc.occ(ctx, t, "", "config:system:set", "trusted_domains", strconv.Itoa(next), "--value="+name); err != nil {
			return fmt.Errorf("appsso: add %s to Nextcloud's trusted domains: %w", name, err)
		}
		current[next] = name
		next++
	}
	return nil
}

// trustedDomains reads trusted_domains by index. occ prints a list as a
// JSON array, or as an object when its indexes have gaps; an unset key
// reads as empty.
func (a nextcloud) trustedDomains(ctx context.Context, t Target) (map[int]string, error) {
	out, err := a.occ(ctx, t, "", "config:system:get", "trusted_domains", "--output=json")
	current := map[int]string{}
	text := strings.TrimSpace(out)
	if err != nil {
		if text == "" || strings.Contains(strings.ToLower(text), "not set") {
			return current, nil
		}
		return nil, fmt.Errorf("appsso: read Nextcloud's trusted domains: %w", err)
	}
	if text == "" || text == "null" {
		return current, nil
	}
	var list []string
	if json.Unmarshal([]byte(text), &list) == nil {
		for i, v := range list {
			current[i] = v
		}
		return current, nil
	}
	var byIndex map[string]string
	if err := json.Unmarshal([]byte(text), &byIndex); err != nil {
		return nil, fmt.Errorf("appsso: Nextcloud's trusted domains are not a list: %s", text)
	}
	keys := make([]string, 0, len(byIndex))
	for k := range byIndex {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		i, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("appsso: Nextcloud's trusted domains have a key %q", k)
		}
		current[i] = byIndex[k]
	}
	return current, nil
}
