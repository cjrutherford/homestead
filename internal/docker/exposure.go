package docker

import (
	"fmt"
	"regexp"
	"strings"

	"homestead/internal/firewall"
)

var validPolicyName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Policy narrows a route's exposure beyond its origin zone: the named
// allowed origins for one route. Sources reuse firewall.Source, so
// proxy intent and filter intent stay literally one language (ADR-006).
// Zone-wide sources (empty CIDR) filter at the firewall — packets never
// reach the proxy — while listed networks enforce here at render.
// Empty sources deny all origins explicitly: a parked route serves
// aborts, never content.
type Policy struct {
	Name    string
	Sources []firewall.Source
}

// Validate checks the policy shape: name spelling and valid sources.
// Duplicate zones stay legal — the proxy unions their networks.
func (p Policy) Validate() error {
	if !validPolicyName.MatchString(p.Name) {
		return fmt.Errorf("route.policy.name: must match [a-z][a-z0-9-]*")
	}
	for i := range p.Sources {
		if err := p.Sources[i].Validate(); err != nil {
			return fmt.Errorf("route.policy source #%d: %w", i, err)
		}
	}
	return nil
}

// networks collects listed CIDRs for proxy enforcement; zone-wide
// sources contribute nothing here by design (firewall's job).
func (p Policy) networks() []string {
	var out []string
	for _, source := range p.Sources {
		if source.CIDR != "" {
			out = append(out, source.CIDR)
		}
	}
	return out
}

// enforcement selects the render shape: open (no policy or zone-wide
// only), allowlist (listed networks), or deny (empty sources).
func (p *Policy) enforcement() string {
	if p == nil {
		return "open"
	}
	if len(p.Sources) == 0 {
		return "deny"
	}
	if len(p.networks()) == 0 {
		return "open"
	}
	return "allow"
}

// renderUpstream wraps the proxy block per enforcement: plain serving,
// an allowlist with aborts elsewhere, or aborts everywhere.
func renderUpstream(policy *Policy, proxy string) string {
	switch policy.enforcement() {
	case "allow":
		var body strings.Builder
		fmt.Fprintf(&body, "\t# Exposure policy: %s (S116). Listed networks serve; all other origins abort.\n", policy.Name)
		fmt.Fprintf(&body, "\t@allowed remote_ip %s\n", strings.Join(policy.networks(), " "))
		body.WriteString("\thandle @allowed {\n")
		for _, line := range strings.Split(strings.TrimSuffix(proxy, "\n"), "\n") {
			body.WriteString("\t" + line + "\n")
		}
		body.WriteString("\t}\n\thandle {\n\t\tabort\n\t}\n")
		return body.String()
	case "deny":
		return fmt.Sprintf("\t# Exposure policy: %s denies all origins.\n\thandle {\n\t\tabort\n\t}\n", policy.Name)
	default:
		return proxy
	}
}
