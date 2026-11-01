package catalog

import (
	"fmt"
	"regexp"
	"strings"

	"homestead/internal/docker"
)

// ForwardAuth declares that Homestead gates the app behind Authentik
// forward-auth at the edge (S418, ADR-055). It exists for the apps that
// have no OIDC; it is mutually exclusive with sso.oidc. The app's class
// stays unsupported: that class says the app speaks no sign-in protocol
// of its own, which is exactly why the edge signs people in.
type ForwardAuth struct {
	// Service is the service the gate protects (it serves an endpoint).
	Service string `yaml:"service" json:"service"`
	// UserHeader is the header the app reads the username from. It is one
	// of the headers copied from the outpost's answer; empty for an app
	// that trusts no header.
	UserHeader string `yaml:"userheader,omitempty" json:"userheader,omitempty"`
	// CopyHeaders are the only X-Authentik-* headers copied to the app.
	CopyHeaders []string `yaml:"copyheaders,omitempty" json:"copyheaders,omitempty"`
	// Bypass are the paths that skip the gate, each with the reason it is
	// safe. Anchored regexes over the request path; the outpost's own
	// paths are handled by the engine and never listed.
	Bypass []ForwardBypass `yaml:"bypass,omitempty" json:"bypass,omitempty"`
	// Env are the app's trust settings, written to its override file
	// while the gate is on and taken out when it is off. The only
	// placeholders are {{edge_source}} (the edge network's CIDR) and
	// {{user_header}}.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// Admin says how the app's admin role is set: local (chosen inside the
	// app, the only value this release supports; an admin cannot be
	// mapped from a header).
	Admin string `yaml:"admin,omitempty" json:"admin,omitempty"`
	// Warnings are fixed lines for the sign-in plan.
	Warnings []string `yaml:"warnings,omitempty" json:"warnings,omitempty"`
	// AccountsFrom names how Homestead reads the app's existing usernames
	// for the collision and no-administrator refusals: navidrome-sqlite
	// is the only reader.
	AccountsFrom string `yaml:"accountsfrom,omitempty" json:"accountsfrom,omitempty"`
}

// ForwardBypass is one open path and why it is safe.
type ForwardBypass struct {
	Path   string `yaml:"path" json:"path"`
	Reason string `yaml:"reason" json:"reason"`
}

// Placeholders of the forward-auth env templates.
var ForwardAuthPlaceholders = []string{"edge_source", "user_header"}

// AccountsNavidromeSQLite reads Navidrome's user table.
const AccountsNavidromeSQLite = "navidrome-sqlite"

var (
	forwardHeader = regexp.MustCompile(`^X-Authentik-[A-Za-z0-9-]+$`)
	secretLikeKey = regexp.MustCompile(`(?i)(SECRET|PASSWORD|PASSWD|TOKEN|PRIVATE|CREDENTIAL|APIKEY|API_KEY)`)
)

func (f ForwardAuth) validate(m Manifest, services map[string]Service) error {
	if m.SSO.OIDC != nil {
		return fmt.Errorf("catalog: sso.forwardauth and sso.oidc are mutually exclusive")
	}
	if m.SSO.Class.Effective() != SSOUnsupported {
		return fmt.Errorf("catalog: a forward-auth app declares class unsupported (it speaks no sign-in protocol of its own)")
	}
	svc, ok := services[f.Service]
	if !ok || svc.Exposes == "" {
		return fmt.Errorf("catalog: forward-auth service %q must be a service that serves an endpoint", f.Service)
	}
	copied := map[string]bool{}
	for _, h := range f.CopyHeaders {
		if !forwardHeader.MatchString(h) {
			return fmt.Errorf("catalog: forward-auth copyheaders %q must be an X-Authentik-* header", h)
		}
		copied[h] = true
	}
	if f.UserHeader != "" && !copied[f.UserHeader] {
		return fmt.Errorf("catalog: forward-auth userheader %q must be one of copyheaders", f.UserHeader)
	}
	seen := map[string]bool{}
	for _, b := range f.Bypass {
		if err := docker.ValidateBypass(b.Path); err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
		if strings.TrimSpace(b.Reason) == "" {
			return fmt.Errorf("catalog: forward-auth bypass %q needs a reason", b.Path)
		}
		if seen[b.Path] {
			return fmt.Errorf("catalog: duplicate forward-auth bypass %q", b.Path)
		}
		seen[b.Path] = true
	}
	for k, v := range f.Env {
		if !validEnvKey.MatchString(k) || secretLikeKey.MatchString(k) {
			return fmt.Errorf("catalog: forward-auth env %q must be a plain setting name, not a secret", k)
		}
		if _, dup := svc.Environment[k]; dup {
			return fmt.Errorf("catalog: forward-auth env %q is also in the service's environment", k)
		}
		for _, m := range envPlaceholder.FindAllStringSubmatch(v, -1) {
			known := false
			for _, p := range ForwardAuthPlaceholders {
				known = known || m[1] == p
			}
			if !known {
				return fmt.Errorf("catalog: forward-auth env %q uses unknown placeholder {{%s}}", k, m[1])
			}
		}
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("catalog: forward-auth env %q must be one line", k)
		}
	}
	switch f.Admin {
	case "", "local":
	default:
		return fmt.Errorf("catalog: forward-auth admin %q must be local: a header cannot set an app's admin role", f.Admin)
	}
	switch f.AccountsFrom {
	case "", AccountsNavidromeSQLite:
	default:
		return fmt.Errorf("catalog: forward-auth accountsfrom %q unknown", f.AccountsFrom)
	}
	return nil
}
