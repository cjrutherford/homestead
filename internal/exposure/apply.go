package exposure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/firewall"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// Approval is the recorded ownership decision for one route: the
// reviewed plan, the acting owner, and the explicit CIDRs public and
// overlay sources may serve. The planner never invents wide sources,
// so owners name them here — least privilege by construction. The
// plan stores as JSON for the audit trail: what was reviewed is what
// applies. Applying reads approvals, so firewall composition always
// reflects every approved route — approving a second route never
// drops the first.
type Approval struct {
	Host     docker.Hostname
	Plan     Plan
	Actor    string
	Networks []string
}

// ErrNoApproval reports apply without approval: the ceremony orders
// review, then approval, then application — never application first.
var ErrNoApproval = errors.New("exposure: route not approved")

// Seams for side effects, stubbed in tests like the composeUp seam:
// live network discovery, firewall application, proxy reload.
var (
	discoverNetwork  = discover.DiscoverNetwork
	applyFirewallSet = firewall.ApplySet
	reloadProxy      = docker.ReloadProxy
)

// Approve reviews a plan into an approval: the actor is named, the
// edge re-validates, a fresh assessment must align, and every CIDR
// parses — approving stale reviews, unsafe targets, or malformed
// networks refuses. Already-placed routes refuse: approvals move
// exposure, never restate it.
func Approve(plan Plan, actor string, networks []string) (Approval, error) {
	if actor == "" {
		return Approval{}, fmt.Errorf("exposure: actor must not be empty")
	}
	if err := plan.Edge.Validate(); err != nil {
		return Approval{}, err
	}
	if plan.Edge.Exposure != plan.To {
		return Approval{}, fmt.Errorf("exposure: edge serves %s, plan targets %s", plan.Edge.Exposure, plan.To)
	}
	if plan.From == plan.To {
		return Approval{}, fmt.Errorf("exposure: %q already serves %s", plan.Host, plan.To)
	}
	assessment, err := docker.Assess(plan.Edge)
	if err != nil {
		return Approval{}, err
	}
	if !assessment.Aligned {
		return Approval{}, fmt.Errorf("exposure: %q assessment no longer aligned: %s", plan.Host, firstFinding(assessment))
	}
	for _, network := range networks {
		if _, err := netip.ParsePrefix(network); err != nil {
			return Approval{}, fmt.Errorf("exposure: invalid CIDR %q: %w", network, err)
		}
	}
	return Approval{Host: plan.Host, Plan: plan, Actor: actor, Networks: append([]string(nil), networks...)}, nil
}

// firstFinding names the first engine finding for refusal messages.
func firstFinding(assessment docker.Assessment) string {
	if len(assessment.Findings) == 0 {
		return "misaligned"
	}
	return assessment.Findings[0].Message
}

// RecordApproval stores the approval, replacing any earlier decision
// for the host: one route, one current approval.
func RecordApproval(st *store.Store, approval Approval) error {
	if approval.Host == "" || approval.Actor == "" {
		return fmt.Errorf("exposure: cannot record empty approval")
	}
	encoded, err := json.Marshal(approval.Plan)
	if err != nil {
		return fmt.Errorf("exposure: encode plan: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	networks, err := json.Marshal(approval.Networks)
	if err != nil {
		return fmt.Errorf("exposure: encode networks: %w", err)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO exposure_approvals (host, plan, actor, networks, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(host) DO UPDATE SET plan = excluded.plan, actor = excluded.actor, networks = excluded.networks, updated_at = excluded.updated_at`,
		string(approval.Host), string(encoded), approval.Actor, string(networks), now,
	); err != nil {
		return fmt.Errorf("exposure: record approval: %w", err)
	}
	return nil
}

// decodeApproval renders a stored row back into an approval, with the
// edge re-validated: stored plans never bypass shape checks.
func decodeApproval(host string, encoded string, actor string, networks string) (Approval, error) {
	var plan Plan
	if err := json.Unmarshal([]byte(encoded), &plan); err != nil {
		return Approval{}, fmt.Errorf("exposure: decode plan: %w", err)
	}
	var cidrs []string
	if err := json.Unmarshal([]byte(networks), &cidrs); err != nil {
		return Approval{}, fmt.Errorf("exposure: decode networks: %w", err)
	}
	approval := Approval{Host: docker.Hostname(host), Plan: plan, Actor: actor, Networks: cidrs}
	if err := approval.Plan.Edge.Validate(); err != nil {
		return Approval{}, fmt.Errorf("exposure: stored edge invalid: %w", err)
	}
	return approval, nil
}

// ApprovalFor returns the recorded approval for host.
func ApprovalFor(st *store.Store, host docker.Hostname) (Approval, error) {
	var encoded, actor, networks string
	err := st.DB().QueryRow(`SELECT plan, actor, networks FROM exposure_approvals WHERE host = ?`, string(host)).Scan(&encoded, &actor, &networks)
	if err == sql.ErrNoRows {
		return Approval{}, ErrNoApproval
	}
	if err != nil {
		return Approval{}, fmt.Errorf("exposure: read approval: %w", err)
	}
	return decodeApproval(string(host), encoded, actor, networks)
}

// Approvals lists every recorded approval in host order: the full
// set firewall composition expands.
func Approvals(st *store.Store) ([]Approval, error) {
	rows, err := st.DB().Query(`SELECT host, plan, actor, networks FROM exposure_approvals ORDER BY host`)
	if err != nil {
		return nil, fmt.Errorf("exposure: list approvals: %w", err)
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		var host, encoded, actor, networks string
		if err := rows.Scan(&host, &encoded, &actor, &networks); err != nil {
			return nil, fmt.Errorf("exposure: scan approval: %w", err)
		}
		approval, err := decodeApproval(host, encoded, actor, networks)
		if err != nil {
			return nil, err
		}
		out = append(out, approval)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("exposure: list approvals: %w", err)
	}
	return out, nil
}

// ApplyDefinition plans approval application for host: firewall
// composition (baseline plus every approved route's policies,
// atomically applied) then the edge route snippet with a proxy
// reload. The host must be approved first — application never
// precedes ownership. Live network facts resolve at run time, and
// the approval re-reads at run time, so long-queued operations
// apply current intent, not stale captures.
func ApplyDefinition(st *store.Store, host docker.Hostname, daemonPort int, caddyDir string) (operation.Definition, error) {
	if st == nil {
		return operation.Definition{}, fmt.Errorf("exposure: apply needs a store")
	}
	if host == "" {
		return operation.Definition{}, fmt.Errorf("exposure: apply needs a host")
	}
	if daemonPort < 1 || daemonPort > 65535 {
		return operation.Definition{}, fmt.Errorf("exposure: bad daemon port %d", daemonPort)
	}
	if caddyDir == "" {
		return operation.Definition{}, fmt.Errorf("exposure: apply needs a Caddy directory")
	}
	if _, err := ApprovalFor(st, host); err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "exposure.apply"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "firewall",
		Run: func(ctx context.Context) error {
			return applyFirewall(st, daemonPort)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "edge",
		Run: func(ctx context.Context) error {
			return applyEdge(st, host, caddyDir, ctx)
		},
	})
	return def, nil
}

// applyFirewall expands every approval against live network facts
// over the management baseline and atomically applies the composed
// table: one commit, verified after. Baseline rules come first,
// approvals follow in host order; exact duplicates collapse (the
// same effect from two routes is one rule), while same-named rules
// with different content refuse naming both owners — silently
// picking one would launder a conflict into filtering.
func applyFirewall(st *store.Store, daemonPort int) error {
	network, err := discoverNetwork()
	if err != nil {
		return fmt.Errorf("exposure: discover network: %w", err)
	}
	baseline, err := firewall.ManagementBaseline(daemonPort, network)
	if err != nil {
		return err
	}
	approvals, err := Approvals(st)
	if err != nil {
		return err
	}
	merged := firewall.RuleSet{Rules: append([]firewall.Rule(nil), baseline.Rules...)}
	byName := map[string]firewall.Rule{}
	owners := map[string]string{}
	for _, rule := range baseline.Rules {
		byName[rule.Name] = rule
		owners[rule.Name] = "management"
	}
	for _, approval := range approvals {
		refined, err := refinePolicies(approval.Plan.Firewall, approval.Networks)
		if err != nil {
			return fmt.Errorf("exposure: refine %q: %w", approval.Host, err)
		}
		expanded, err := firewall.Expand(refined, network)
		if err != nil {
			return fmt.Errorf("exposure: expand %q: %w", approval.Host, err)
		}
		for _, rule := range expanded.Rules {
			prior, seen := byName[rule.Name]
			if !seen {
				byName[rule.Name] = rule
				owners[rule.Name] = string(approval.Host)
				merged.Rules = append(merged.Rules, rule)
				continue
			}
			if !reflect.DeepEqual(prior, rule) {
				return fmt.Errorf("exposure: conflicting firewall rule %q approved by %s and %s", rule.Name, owners[rule.Name], approval.Host)
			}
		}
	}
	return applyFirewallSet(merged)
}

// refinePolicies substitutes approved CIDRs for zone-wide public
// and overlay sources: the planner never invents wide sources, so
// owners name them at approval. Local and already-narrowed sources
// pass through untouched. A policy left with no servable source
// refuses — widening without named networks is exactly what the
// ceremony blocks.
func refinePolicies(policies []firewall.ServicePolicy, networks []string) ([]firewall.ServicePolicy, error) {
	var out []firewall.ServicePolicy
	for _, policy := range policies {
		refined := firewall.ServicePolicy{Service: policy.Service}
		for _, source := range policy.Sources {
			if source.CIDR != "" || source.Zone == firewall.ZoneLocal {
				refined.Sources = append(refined.Sources, source)
				continue
			}
			for _, network := range networks {
				refined.Sources = append(refined.Sources, firewall.Source{Zone: source.Zone, CIDR: network})
			}
		}
		if len(refined.Sources) == 0 {
			return nil, fmt.Errorf("exposure: service %q needs explicit CIDRs for wide exposure", policy.Service.Name)
		}
		out = append(out, refined)
	}
	return out, nil
}

// applyEdge writes the approved route snippet and reloads the proxy:
// the route takes effect on reload, never on file write alone.
func applyEdge(st *store.Store, host docker.Hostname, caddyDir string, ctx context.Context) error {
	approval, err := ApprovalFor(st, host)
	if err != nil {
		return err
	}
	content, err := docker.RenderRoute(approval.Plan.Edge)
	if err != nil {
		return err
	}
	routesDir := filepath.Join(caddyDir, "routes")
	if err := os.MkdirAll(routesDir, 0o755); err != nil {
		return fmt.Errorf("exposure: routes dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(routesDir, docker.RouteFilename(approval.Plan.Edge.Host)), []byte(content), 0o644); err != nil {
		return fmt.Errorf("exposure: write route: %w", err)
	}
	return reloadProxy(ctx)
}
