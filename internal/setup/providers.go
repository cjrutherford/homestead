package setup

import (
	"homestead/internal/dns"
)

// Permission is one grant the owner gives a provider credential. Grant is
// our best description of the provider's console wording; nothing verifies
// the exact console text. What is pinned is the set of grants:
// permissions_test.go runs each adapter and the tunnel client against a
// recording server and fails when the calls they make and the grants listed
// here differ, in either direction.
type Permission struct {
	Grant  string `json:"grant"`
	Reason string `json:"reason"`
}

// CredentialField is one value the owner enters. The value goes to the
// secret store (ADR-021) and never into setup state.
type CredentialField struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

// Provider is one DNS provider the walkthrough offers (ADR-034).
type Provider struct {
	Name        string            `json:"name"`
	Label       string            `json:"label"`
	Summary     string            `json:"summary"`
	Credentials []CredentialField `json:"credentials"`
	Permissions []Permission      `json:"permissions"`
	// TunnelPermissions are added to the same credential when the owner
	// chooses a Cloudflare Tunnel; empty for every other provider.
	TunnelPermissions []Permission `json:"tunnel_permissions"`
	Notes             []string     `json:"notes"`
}

// providerCopy is the owner-facing text. The credential fields are not
// repeated here: they come from the adapters (RequiredCredentials), so the
// form can never ask for something an adapter does not read.
var providerCopy = []Provider{
	{
		Name:    "cloudflare",
		Label:   "Cloudflare",
		Summary: "Homestead reads your zone and edits its DNS records. Certificates use the same token.",
		Permissions: []Permission{
			{Grant: "Zone: Zone: Read", Reason: "find the zone you chose and confirm the token can see it"},
			{Grant: "Zone: DNS: Edit", Reason: "list, create and delete the records Homestead manages, the record that points a tunnel hostname at the tunnel, and the temporary records for certificates"},
		},
		TunnelPermissions: []Permission{
			{Grant: "Account: Cloudflare Tunnel: Edit", Reason: "create, check and remove the tunnel (your account is found from the zone)"},
		},
		Notes: []string{
			"Create an API token (My Profile, API Tokens, Create Token) and limit it to the one zone you chose.",
			"Add the tunnel permission only if you choose Cloudflare Tunnel. The tunnel uses this same token; it never asks for it again or for an account ID.",
		},
	},
	{
		Name:    "digitalocean",
		Label:   "DigitalOcean",
		Summary: "Homestead reads your domain and edits its DNS records. Certificates use the same token.",
		Permissions: []Permission{
			{Grant: "Read scope", Reason: "list your domains and their records through the domains API"},
			{Grant: "Write scope", Reason: "create and delete the records Homestead manages, and the temporary records for certificates"},
		},
		Notes: []string{
			"Create a personal access token (API, Tokens) with both read and write scope.",
			"DigitalOcean provides DNS and certificates only. Reaching the appliance from outside is a router port forward.",
		},
	},
	{
		Name:    "route53",
		Label:   "Amazon Route 53",
		Summary: "Homestead reads your hosted zone and edits its records. Certificates use the same key pair.",
		Permissions: []Permission{
			{Grant: "route53:ListHostedZones", Reason: "find the hosted zone you chose"},
			{Grant: "route53:ListResourceRecordSets", Reason: "read the records in that zone"},
			{Grant: "route53:ChangeResourceRecordSets", Reason: "create and delete the records Homestead manages, and the temporary records for certificates"},
		},
		Notes: []string{
			"Create an IAM user with an access key pair and allow those three actions on the hosted zone.",
			"Route 53 provides DNS and certificates only. Reaching the appliance from outside is a router port forward.",
		},
	},
	{
		Name:    "manual",
		Label:   "Manual",
		Summary: "No credential. Homestead shows the exact records and you create them where your domain is managed.",
		Notes: []string{
			"Nothing is stored. Certificates that need a DNS challenge ask you to add the record by hand.",
			"Reaching the appliance from outside is a router port forward.",
		},
	},
}

// Providers returns the four providers the walkthrough offers, with the
// credential fields read from the adapters themselves.
func Providers() []Provider {
	out := make([]Provider, 0, len(providerCopy))
	for _, p := range providerCopy {
		p.Credentials = credentialsOf(p.Name)
		if p.Permissions == nil {
			p.Permissions = []Permission{}
		}
		if p.TunnelPermissions == nil {
			p.TunnelPermissions = []Permission{}
		}
		out = append(out, p)
	}
	return out
}

// ProviderNamed finds a provider by name.
func ProviderNamed(name string) (Provider, bool) {
	for _, p := range Providers() {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// adapter builds the production adapter for a provider; it is used for its
// declared credential fields only, no request is made.
func adapter(name string) dns.Provider {
	switch name {
	case "cloudflare":
		if p, err := dns.NewCloudflare(dns.CloudflareAPI); err == nil {
			return p
		}
	case "digitalocean":
		if p, err := dns.NewDigitalOcean(dns.DigitalOceanAPI); err == nil {
			return p
		}
	case "route53":
		if p, err := dns.NewRoute53(dns.Route53Endpoint); err == nil {
			return p
		}
	case "manual":
		return dns.Manual{}
	}
	return nil
}

func credentialsOf(name string) []CredentialField {
	out := []CredentialField{}
	a := adapter(name)
	if a == nil {
		return out
	}
	for _, f := range a.RequiredCredentials() {
		out = append(out, CredentialField{Name: f.Name, Label: f.Label, Secret: f.Secret})
	}
	return out
}

// AccessOption is one way the appliance can be reached (ADR-034, step 3).
type AccessOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
	// Reason says why an option is unavailable; empty when it is available.
	Reason string `json:"reason,omitempty"`
}

// Access modes.
const (
	AccessHome   = "home"
	AccessTunnel = "tunnel"
	AccessRouter = "router"
)

// AccessOptions lists the ways to reach the appliance for a provider.
// Cloudflare Tunnel is offered only with Cloudflare (ADR-017); the others
// stay in the list, unavailable, with the reason, never silently hidden.
func AccessOptions(provider string) []AccessOption {
	tunnel := AccessOption{
		ID:          AccessTunnel,
		Label:       "Cloudflare Tunnel",
		Description: "An outbound-only connection from the appliance to Cloudflare. No router changes.",
		Available:   provider == "cloudflare",
	}
	if !tunnel.Available {
		if provider == "" {
			tunnel.Reason = "Choose Cloudflare as the DNS provider to use a tunnel."
		} else {
			tunnel.Reason = "A tunnel needs Cloudflare as the DNS provider. " + providerLabel(provider) +
				" provides DNS and certificates only; use a router port forward to reach the appliance from outside."
		}
	}
	return []AccessOption{
		{
			ID:          AccessHome,
			Label:       "Home network only",
			Description: "Reachable from devices on your network. Nothing is opened to the internet. This is the default.",
			Available:   true,
		},
		tunnel,
		{
			ID:          AccessRouter,
			Label:       "Router port forward",
			Description: "Open specific ports on your router to the appliance. You review and approve the plan first.",
			Available:   true,
		},
	}
}

func providerLabel(name string) string {
	if p, ok := ProviderNamed(name); ok {
		return p.Label
	}
	return name
}
