package docker

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"homestead/internal/dns"
	"homestead/internal/operation"
)

// DefaultDNSResolver is the local DNS origin VerifyName dials when the
// caller passes no lookup: the AdGuard DNS port on loopback (S084).
const DefaultDNSResolver = "127.0.0.1:53"

// LookupFunc resolves a hostname to addresses. Production uses
// LookupVia; tests inject fakes.
type LookupFunc func(ctx context.Context, host string) ([]netip.Addr, error)

// FriendlyName binds a local route to its LAN answer: the proxy serves
// the hostname (S083) and local DNS answers it (S085). Friendly names
// require local exposure — names for wider origins arrive with the
// public exposure ceremony, never as an accident here.
type FriendlyName struct {
	Route  Route
	Answer netip.Addr
}

// Validate checks the binding: a valid local route and a usable answer.
func (n FriendlyName) Validate() error {
	if err := n.Route.Validate(); err != nil {
		return err
	}
	if n.Route.Exposure != ExposureLocal {
		return fmt.Errorf("names: exposure must be local for friendly names")
	}
	if !n.Answer.IsValid() || n.Answer.IsUnspecified() {
		return fmt.Errorf("names: answer must be a usable IP address")
	}
	return nil
}

// Rewrite maps the binding to the DNS adapter shape.
func (n FriendlyName) Rewrite() dns.Rewrite {
	return dns.Rewrite{Domain: string(n.Route.Host), Answer: n.Answer.String()}
}

// WriteNameSnippet renders one binding into the stack routes directory.
// The write is additive for a single name; table convergence (stale
// cleanup) stays with ReconcileRoutes, which callers run for the full
// desired table.
func WriteNameSnippet(dir string, name FriendlyName) error {
	if err := name.Validate(); err != nil {
		return err
	}
	content, err := RenderRoute(name.Route)
	if err != nil {
		return err
	}
	routesDir := filepath.Join(dir, "routes")
	if err := os.MkdirAll(routesDir, 0o755); err != nil {
		return fmt.Errorf("names: dir: %w", err)
	}
	file := RouteFilename(name.Route.Host)
	if err := os.WriteFile(filepath.Join(routesDir, file), []byte(content), 0o644); err != nil {
		return fmt.Errorf("names: write %s: %w", file, err)
	}
	return nil
}

// LookupVia returns a LookupFunc dialing addr for every query, so names
// verify against local DNS rather than the host resolver configuration.
func LookupVia(addr string) LookupFunc {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return func(ctx context.Context, host string) ([]netip.Addr, error) {
		ips, err := resolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("names: resolve %s: %w", host, err)
		}
		out := make([]netip.Addr, 0, len(ips))
		for _, ip := range ips {
			addr, ok := netip.AddrFromSlice(ip.To16())
			if !ok {
				continue
			}
			out = append(out, addr.Unmap())
		}
		return out, nil
	}
}

// VerifyName proves a binding serves: the route renders (the proxy can
// serve it) and the name resolves to the bound answer through lookup.
func VerifyName(ctx context.Context, lookup LookupFunc, name FriendlyName) error {
	if err := name.Validate(); err != nil {
		return err
	}
	if _, err := RenderRoute(name.Route); err != nil {
		return err
	}
	if lookup == nil {
		lookup = LookupVia(DefaultDNSResolver)
	}
	got, err := lookup(ctx, string(name.Route.Host))
	if err != nil {
		return err
	}
	for _, addr := range got {
		if addr == name.Answer {
			return nil
		}
	}
	return fmt.Errorf("names: %s resolves to %v, want %s", name.Route.Host, got, name.Answer)
}

// EnsureNameDefinition plans friendly-name adoption: write the proxy
// snippet, add the DNS answer, reload the proxy, and verify the name
// resolves to the answer. Steps run in that order so each layer exists
// before the next leans on it; rerunning converges (identical snippet,
// idempotent rewrite, converging reload).
func EnsureNameDefinition(stack Stack, dnsClient *dns.Client, lookup LookupFunc, name FriendlyName) (operation.Definition, error) {
	if err := checkCaddyStack(stack); err != nil {
		return operation.Definition{}, err
	}
	if err := name.Validate(); err != nil {
		return operation.Definition{}, err
	}
	if dnsClient == nil {
		return operation.Definition{}, fmt.Errorf("names: DNS client must not be nil")
	}
	def := operation.Definition{Type: "names.ensure"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "write-snippet",
		Run: func(ctx context.Context) error {
			return WriteNameSnippet(stack.Dir, name)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "add-rewrite",
		Run: func(ctx context.Context) error {
			return dnsClient.AddRewrite(ctx, name.Rewrite())
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "reload",
		Run: func(ctx context.Context) error {
			return ReloadProxy(ctx)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "verify-name",
		Run: func(ctx context.Context) error {
			return VerifyName(ctx, lookup, name)
		},
	})
	return def, nil
}
