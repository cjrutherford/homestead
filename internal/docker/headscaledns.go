package docker

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"homestead/internal/dns"
	"homestead/internal/operation"
)

// HeadscaleExtraRecord is one custom DNS answer in the control-plane
// configuration: Headscale serves A/AAAA extras to overlay nodes, so
// CNAME and TXT intent stays out (the proxy and provider records serve
// those names). Names render fully qualified, matching the rewrite
// bridge (S098) spelling both sides already speak.
type HeadscaleExtraRecord struct {
	Name  string
	Type  string
	Value string
}

// HeadscaleDNS is the overlay DNS intent for a managed control plane:
// MagicDNS with a base domain, upstream nameservers, and custom
// answers. Tailscale-side DNS (MagicDNS names, search, nameservers)
// stays admin-console owned — no adapter is planned — so this spec
// targets Headscale configuration only.
type HeadscaleDNS struct {
	MagicDNS    bool
	BaseDomain  string
	Nameservers []string
	Extra       []HeadscaleExtraRecord
}

// Validate checks the spec shape: base domain spelling, usable
// nameserver IPs, and well-formed extras.
func (s HeadscaleDNS) Validate() error {
	if s.BaseDomain != "" {
		if err := validateHeadscaleDomain(s.BaseDomain); err != nil {
			return err
		}
	}
	for _, server := range s.Nameservers {
		addr, err := netip.ParseAddr(server)
		if err != nil || addr.IsUnspecified() {
			return fmt.Errorf("headscale: nameserver must be a usable IP address")
		}
	}
	seen := map[HeadscaleExtraRecord]bool{}
	for i := range s.Extra {
		if s.Extra[i].Type != "A" && s.Extra[i].Type != "AAAA" {
			return fmt.Errorf("headscale: extra record #%d must be A or AAAA", i)
		}
		if err := validateHeadscaleDomain(s.Extra[i].Name); err != nil {
			return fmt.Errorf("headscale: extra record #%d: %w", i, err)
		}
		addr, err := netip.ParseAddr(s.Extra[i].Value)
		if err != nil || addr.IsUnspecified() {
			return fmt.Errorf("headscale: extra record #%d value must be an IP address", i)
		}
		if (s.Extra[i].Type == "A") != addr.Is4() {
			return fmt.Errorf("headscale: extra record #%d type and value disagree", i)
		}
		if seen[s.Extra[i]] {
			return fmt.Errorf("headscale: duplicate extra record #%d", i)
		}
		seen[s.Extra[i]] = true
	}
	return nil
}

// validateHeadscaleDomain checks FQDN-ish spelling for config values:
// lowercase DNS, trailing dot tolerated and trimmed by callers' context
// (Headscale accepts both; rendering keeps the given form).
func validateHeadscaleDomain(name string) error {
	trimmed := strings.TrimSuffix(name, ".")
	if len(trimmed) == 0 || len(trimmed) > 253 {
		return fmt.Errorf("headscale: name must be 1-253 characters")
	}
	if strings.ToLower(trimmed) != trimmed {
		return fmt.Errorf("headscale: name must be lowercase")
	}
	if strings.HasPrefix(trimmed, ".") || strings.Contains(trimmed, "..") {
		return fmt.Errorf("headscale: name must not have empty labels")
	}
	for _, label := range strings.Split(trimmed, ".") {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("headscale: invalid label %q", label)
		}
		for _, r := range label {
			if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return fmt.Errorf("headscale: invalid label %q", label)
			}
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("headscale: invalid label %q", label)
		}
	}
	return nil
}

// ExtraRecordsFor bridges split-DNS intent to control-plane answers:
// address records become extras with fully-qualified names, everything
// else stays out with A/AAAA-only serving. Zone names the intent
// origin for validation context; record names arrive relative and
// leave absolute.
func ExtraRecordsFor(zoneName string, records []dns.Record) ([]HeadscaleExtraRecord, error) {
	rewrites, err := dns.EffectiveRewrites(zoneName, records)
	if err != nil {
		return nil, err
	}
	var out []HeadscaleExtraRecord
	for _, rewrite := range rewrites {
		typ := "A"
		if addr, err := netip.ParseAddr(rewrite.Answer); err == nil && addr.Is6() {
			typ = "AAAA"
		}
		out = append(out, HeadscaleExtraRecord{Name: rewrite.Domain, Type: typ, Value: rewrite.Answer})
	}
	return out, nil
}

// headscaleConfigPath locates the managed configuration file.
func headscaleConfigPath(dir string) string {
	return filepath.Join(dir, "config", "config.yaml")
}

// WriteHeadscaleDNS reconciles the dns section of a deployed managed
// configuration, preserving every other key: the file parses into a
// generic mapping, only dns is replaced, and the document writes back
// with the managed header. Missing configuration fails — deploy first
// (S114) — and invalid specs refuse before touching disk.
func WriteHeadscaleDNS(dir string, spec HeadscaleDNS) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	path := headscaleConfigPath(dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("headscale: read config: %w", err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("headscale: parse config: %w", err)
	}
	if config == nil {
		config = map[string]any{}
	}
	section := map[string]any{"magic_dns": spec.MagicDNS}
	if spec.BaseDomain != "" {
		section["base_domain"] = spec.BaseDomain
	}
	if len(spec.Nameservers) > 0 {
		servers := make([]any, 0, len(spec.Nameservers))
		for _, server := range spec.Nameservers {
			servers = append(servers, server)
		}
		section["nameservers"] = servers
	}
	if len(spec.Extra) > 0 {
		extras := make([]any, 0, len(spec.Extra))
		for _, record := range spec.Extra {
			extras = append(extras, map[string]any{
				"name": record.Name, "type": record.Type, "value": record.Value,
			})
		}
		section["extra_records"] = extras
	}
	config["dns"] = section
	var body strings.Builder
	body.WriteString("# Generated by Homestead (S114/S115). The dns section reconciles;\n# every other key is deployment input, preserved across writes.\n")
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(config); err != nil {
		return fmt.Errorf("headscale: render config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("headscale: render config: %w", err)
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		return fmt.Errorf("headscale: write config: %w", err)
	}
	return nil
}

// HeadscaleDNSDefinition plans overlay DNS adoption: reconcile the dns
// section, restart the control plane to load it, and verify the server
// runs. Idempotent throughout: identical specs rewrite identically and
// restart converges.
func HeadscaleDNSDefinition(stack Stack, spec HeadscaleDNS) (operation.Definition, error) {
	if err := checkHeadscaleStack(stack); err != nil {
		return operation.Definition{}, err
	}
	if err := spec.Validate(); err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "headscale.dns"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "write-dns",
		Run: func(ctx context.Context) error {
			return WriteHeadscaleDNS(stack.Dir, spec)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "restart",
		Run: func(ctx context.Context) error {
			return runCompose(ctx, stack, []string{"restart"}, nil)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "verify",
		Run: func(ctx context.Context) error {
			return verifyService(ctx, stack, HeadscaleService)
		},
	})
	return def, nil
}
