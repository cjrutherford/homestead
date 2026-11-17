package catalog

import (
	"fmt"
	"regexp"
	"strings"

	"homestead/internal/identity"
	"homestead/internal/storage"
)

// Catalog integration metadata (S341). A declaration records how one
// package relates to another: which identity provider speaks for it, which
// package shares a library, which app calls which at runtime, or merely
// that two packages are useful together. It is advisory by construction
// (ADR-051): nothing in install, update or start reads it, and it never
// causes Homestead to configure another application's internals.

// IntegrationKind classifies one declared relationship. The kind is
// authored; the level is derived from it, so two packages cannot disagree
// about what the same relationship means.
type IntegrationKind string

const (
	// IntegrationIdentity names the identity provider for this app's SSO.
	IntegrationIdentity IntegrationKind = "identity"
	// IntegrationLibrary names a package that mounts the same bulk
	// directory: the owner supplies the shared path at install.
	IntegrationLibrary IntegrationKind = "library"
	// IntegrationHandoff names an app this one calls at runtime, with
	// configuration the owner writes inside both apps.
	IntegrationHandoff IntegrationKind = "handoff"
	// IntegrationCompanion names a package that is useful together with
	// this one and shares no identity or data.
	IntegrationCompanion IntegrationKind = "companion"
)

// IntegrationLevel is derived from IntegrationKind, never authored. The
// numbering follows ADR-051: more Homestead involvement, never more
// automation.
type IntegrationLevel string

const (
	LevelCoexist  IntegrationLevel = "L1"
	LevelIdentity IntegrationLevel = "L2"
	LevelLibrary  IntegrationLevel = "L3"
	LevelHandoff  IntegrationLevel = "L4"
)

// Integration is one declared relationship in a manifest.
type Integration struct {
	Kind   IntegrationKind `yaml:"kind"`
	Target string          `yaml:"target"`
	Note   string          `yaml:"note"`
}

// Level derives the integration level. Unknown kinds resolve to L1: they
// never validate, and a zero value must not read as a deeper promise.
func (i Integration) Level() IntegrationLevel {
	switch i.Kind {
	case IntegrationIdentity:
		return LevelIdentity
	case IntegrationLibrary:
		return LevelLibrary
	case IntegrationHandoff:
		return LevelHandoff
	default:
		return LevelCoexist
	}
}

// Valid reports a kind the catalog understands.
func (k IntegrationKind) Valid() bool {
	switch k {
	case IntegrationIdentity, IntegrationLibrary, IntegrationHandoff, IntegrationCompanion:
		return true
	}
	return false
}

// validIntegrationTarget bounds targets to package names: a declaration
// names a catalog package, never a path, URL or free-form label.
var validIntegrationTarget = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// integrationNoteMax bounds the owner-facing note.
const integrationNoteMax = 200

// Validate checks one declaration from the declaring package's side:
// a known kind, a package-shaped target that is not the package itself,
// and a note written for an owner. Cross-package gates — the target
// existing, identity providers and shared bulk paths — run at load.
func (i Integration) Validate(owner string) error {
	if !i.Kind.Valid() {
		return fmt.Errorf("integration kind %q must be identity, library, handoff, or companion", i.Kind)
	}
	if !validIntegrationTarget.MatchString(i.Target) {
		return fmt.Errorf("integration target %q must be a package name", i.Target)
	}
	if i.Target == owner {
		return fmt.Errorf("package %q cannot integrate with itself", owner)
	}
	if strings.TrimSpace(i.Note) == "" || len(i.Note) > integrationNoteMax {
		return fmt.Errorf("integration note must be 1-%d characters", integrationNoteMax)
	}
	return nil
}

// validateIntegrations checks the declarations owned by one manifest:
// every declaration valid, no duplicate (kind, target), identity only on
// an SSO-capable package, and a library only on a package that places
// something on bulk storage (S276).
func (m Manifest) validateIntegrations() error {
	seen := map[string]bool{}
	for _, decl := range m.Integrations {
		if err := decl.Validate(m.Name); err != nil {
			return fmt.Errorf("catalog: %s: %w", m.Name, err)
		}
		key := string(decl.Kind) + " " + decl.Target
		if seen[key] {
			return fmt.Errorf("catalog: duplicate %s integration with %q", decl.Kind, decl.Target)
		}
		seen[key] = true
		if decl.Kind == IntegrationIdentity && m.SSO.Class.Effective() == SSOUnsupported {
			return fmt.Errorf("catalog: %s declares identity %q but its SSO class is unsupported",
				m.Name, decl.Target)
		}
		if decl.Kind == IntegrationLibrary && !hasBulkPath(m) {
			return fmt.Errorf("catalog: %s declares a library but places nothing on bulk storage",
				m.Name)
		}
	}
	// An app that advertises SSO must say who provides it: shipping SSO
	// posture with no stated provider is an unactionable promise (S341).
	if m.SSO.Class.Effective() != SSOUnsupported {
		stated := false
		for _, decl := range m.Integrations {
			if decl.Kind == IntegrationIdentity {
				stated = true
				break
			}
		}
		if !stated {
			return fmt.Errorf("catalog: %s declares SSO support but no identity provider", m.Name)
		}
	}
	return nil
}

// hasBulkPath reports whether the package declares at least one bulk data
// path: two apps cannot share a library neither of them keeps on bulk
// storage.
func hasBulkPath(m Manifest) bool {
	for _, service := range m.Services {
		for _, data := range service.Data {
			if data.Class == storage.ClassBulk {
				return true
			}
		}
	}
	return false
}

// BuiltinIdentityProviders names the providers this build ships. Setup
// flows build a registry from the same list; loading the catalog reads it
// directly so a manifest can never name a provider the appliance cannot
// drive.
func BuiltinIdentityProviders() map[string]bool {
	providers := map[string]bool{}
	for _, provider := range identity.Builtins() {
		providers[provider.Name()] = true
	}
	return providers
}

// validateIntegrations runs the cross-package gates: every target is a
// shipped package, identity targets are providers this build drives, and
// library pairs both keep data on bulk storage. A catalog that would lie
// about a relationship fails to load, naming the package and target.
func (c Catalog) validateIntegrations() error {
	providers := BuiltinIdentityProviders()
	for _, name := range c.Names() {
		manifest, err := c.Get(name)
		if err != nil {
			return err
		}
		for _, decl := range manifest.Integrations {
			target, err := c.Get(decl.Target)
			if err != nil {
				return fmt.Errorf("catalog: %s declares %s %q, which the catalog does not ship",
					name, decl.Kind, decl.Target)
			}
			switch decl.Kind {
			case IntegrationIdentity:
				if !providers[decl.Target] {
					return fmt.Errorf("catalog: %s names %q as its identity provider, but it is not a registered provider",
						name, decl.Target)
				}
			case IntegrationLibrary:
				if !hasBulkPath(target) {
					return fmt.Errorf("catalog: %s shares a library with %q, but %q places nothing on bulk storage",
						name, decl.Target, decl.Target)
				}
			}
		}
	}
	return nil
}

// ResolvedIntegration is one declaration with its derived level and the
// target's title: surfaces never re-resolve names themselves.
type ResolvedIntegration struct {
	Kind   IntegrationKind
	Level  IntegrationLevel
	Target string
	Title  string
	Note   string
}

// IntegrationsFor resolves a manifest's declarations against the catalog.
// A package without declarations gets an empty list, never nil.
func (c Catalog) IntegrationsFor(manifest Manifest) []ResolvedIntegration {
	out := make([]ResolvedIntegration, 0, len(manifest.Integrations))
	for _, decl := range manifest.Integrations {
		resolved := ResolvedIntegration{
			Kind: decl.Kind, Level: decl.Level(), Target: decl.Target, Note: decl.Note,
		}
		if target, err := c.Get(decl.Target); err == nil {
			resolved.Title = target.Title
		}
		out = append(out, resolved)
	}
	return out
}

// HandoffNotes reports declared handoff targets the appliance has not
// installed, once each, in declaration order. Advisory: it changes no
// verdict — a missing companion is a degraded experience, not a broken
// install (L4 stays owner-configured).
func HandoffNotes(manifest Manifest, installed func(target string) bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, decl := range manifest.Integrations {
		if decl.Kind != IntegrationHandoff || seen[decl.Target] {
			continue
		}
		seen[decl.Target] = true
		if installed != nil && installed(decl.Target) {
			continue
		}
		out = append(out, fmt.Sprintf("%s works with %s, which is not installed yet — %s",
			manifest.Title, decl.Target, decl.Note))
	}
	return out
}
