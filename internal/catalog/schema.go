package catalog

import (
	"fmt"
	"regexp"
	"strings"

	"homestead/internal/craft"
	"homestead/internal/exposure"
	"homestead/internal/identity"
	"homestead/internal/storage"
)

var (
	validPackageName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	validVersion     = regexp.MustCompile(`^\d+\.\d+(\.\d+)?$`)
	validServiceName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// Service is one container an app runs: a compose-safe name, a pinned
// image (tags required, latest refused — reproducible installs only),
// and the exposure endpoint key it serves. Internal services (database,
// cache, workers) leave Exposes empty: no endpoint, no route, no
// posture to declare. Generation (S153) and recommendations (S154)
// skip them for serving while still rendering their containers.
type Service struct {
	Name    string
	Image   string
	Exposes string
	// Command overrides the image entrypoint command (official
	// images like Authentik require `server`/`worker` verbs):
	// empty inherits the image default.
	Command []string
	// Data declares every container path the image writes (S276):
	// persistent paths placed on mounted storage, ephemeral ones
	// explicitly marked.
	Data []DataPath
	// Environment is plain, non-secret container environment the
	// package needs to start (hostnames, usernames, database names).
	// Secret values never belong here (ADR-021): declare them in the
	// manifest's Secrets list instead.
	Environment map[string]string
}

// Validate checks service shape and endpoint linkage.
func (s Service) Validate(endpoints map[string]bool) error {
	if !validServiceName.MatchString(s.Name) {
		return fmt.Errorf("catalog: invalid service name %q", s.Name)
	}
	for _, arg := range s.Command {
		if arg == "" {
			return fmt.Errorf("catalog: service %q has an empty command argument", s.Name)
		}
	}
	image, tag, found := strings.Cut(s.Image, ":")
	if !found || image == "" || tag == "" || tag == "latest" {
		return fmt.Errorf("catalog: image %q must pin a non-latest tag", s.Image)
	}
	for key, value := range s.Environment {
		if !validEnvKey.MatchString(key) {
			return fmt.Errorf("catalog: service %q has invalid environment variable %q", s.Name, key)
		}
		if len(value) > 4096 {
			return fmt.Errorf("catalog: service %q variable %q exceeds 4096 characters", s.Name, key)
		}
	}
	if s.Exposes != "" && !endpoints[s.Exposes] {
		return fmt.Errorf("catalog: service %q exposes unknown endpoint %q", s.Name, s.Exposes)
	}
	return nil
}

// StorageNeed is one class floor: the minimum free bytes an install
// requires on the class mount. Zero means the class goes unused, and
// unused classes stay out rather than reserving nothing.
type StorageNeed struct {
	Class    storage.Class
	MinBytes uint64
}

// Validate checks the need shape.
func (n StorageNeed) Validate() error {
	if _, err := storage.ProfileFor(n.Class); err != nil {
		return err
	}
	if n.MinBytes == 0 {
		return fmt.Errorf("catalog: storage need for %s must exceed zero", n.Class)
	}
	return nil
}

// Requirements is what an install needs from its host: memory in
// megabytes, per-class storage floors, host ports the app binds (for
// conflict checks at plan time, never auto-opened), and the optional
// architecture, CPU, and GPU floors the requirements engine (S152)
// evaluates. Optional floors stay zero-valued when undeclared —
// existing manifests keep validating — with empty architectures
// meaning any supported platform.
type Requirements struct {
	MemoryMB      uint64
	Storage       []StorageNeed
	Ports         []int
	Architectures []string
	CPUCount      int
	NeedsGPU      bool
}

// Validate checks requirements shape: nonzero memory, valid unique
// storage needs, and valid unique ports.
func (r Requirements) Validate() error {
	if r.MemoryMB == 0 {
		return fmt.Errorf("catalog: memory must exceed zero megabytes")
	}
	seenClasses := map[storage.Class]bool{}
	for i := range r.Storage {
		if err := r.Storage[i].Validate(); err != nil {
			return fmt.Errorf("catalog: storage #%d: %w", i, err)
		}
		if seenClasses[r.Storage[i].Class] {
			return fmt.Errorf("catalog: duplicate storage class %q", r.Storage[i].Class)
		}
		seenClasses[r.Storage[i].Class] = true
	}
	seenPorts := map[int]bool{}
	for _, port := range r.Ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("catalog: port must be 1-65535")
		}
		if seenPorts[port] {
			return fmt.Errorf("catalog: duplicate port %d", port)
		}
		seenPorts[port] = true
	}
	seenArch := map[string]bool{}
	for _, arch := range r.Architectures {
		if arch != "amd64" && arch != "arm64" {
			return fmt.Errorf("catalog: architecture must be amd64 or arm64")
		}
		if seenArch[arch] {
			return fmt.Errorf("catalog: duplicate architecture %q", arch)
		}
		seenArch[arch] = true
	}
	if r.CPUCount < 0 {
		return fmt.Errorf("catalog: CPU count must not be negative")
	}
	return nil
}

// SSOClass declares how a package relates to single sign-on (S203):
// whether the app can delegate login to an external identity
// provider, and how strongly Homestead should nudge toward it.
type SSOClass string

const (
	// SSOUnsupported marks apps with no SSO path (including the
	// identity provider itself, which is the login — not a login
	// consumer).
	SSOUnsupported SSOClass = "unsupported"
	// SSOOptional marks apps that accept SSO alongside local logins.
	SSOOptional SSOClass = "optional"
	// SSORecommended marks apps Homestead nudges toward SSO.
	SSORecommended SSOClass = "recommended"
	// SSORequired marks apps that only serve SSO logins.
	SSORequired SSOClass = "required"
)

// Valid reports a known class. Empty means unset, which reads as
// unsupported so manifests without SSO posture stay valid.
func (c SSOClass) Valid() bool {
	return c == "" || c == SSOUnsupported || c == SSOOptional || c == SSORecommended || c == SSORequired
}

// Effective resolves the working class: unset reads as unsupported.
func (c SSOClass) Effective() SSOClass {
	if c == "" {
		return SSOUnsupported
	}
	return c
}

// SSODeclaration is one package's SSO posture: the class plus the
// protocols it speaks, drawn from the identity provider capabilities
// (S202) so declarations only name protocols providers can serve.
type SSODeclaration struct {
	Class     SSOClass
	Protocols []identity.Capability
}

// Validate checks the declaration: a known class, known unique
// protocols, and protocols present exactly when the class exceeds
// unsupported — support without a protocol is unactionable, and
// protocols on an unsupported app contradict the class.
func (d SSODeclaration) Validate() error {
	if !d.Class.Valid() {
		return fmt.Errorf("catalog: SSO class %q must be unsupported, optional, recommended, or required", d.Class)
	}
	seen := map[identity.Capability]bool{}
	for _, protocol := range d.Protocols {
		if !protocol.Valid() {
			return fmt.Errorf("catalog: SSO protocol %q unknown", protocol)
		}
		if seen[protocol] {
			return fmt.Errorf("catalog: duplicate SSO protocol %q", protocol)
		}
		seen[protocol] = true
	}
	if d.Class.Effective() == SSOUnsupported && len(d.Protocols) > 0 {
		return fmt.Errorf("catalog: unsupported SSO takes no protocols")
	}
	if d.Class.Effective() != SSOUnsupported && len(d.Protocols) == 0 {
		return fmt.Errorf("catalog: %s SSO must name at least one protocol", d.Class)
	}
	return nil
}

// Manifest is one first-party application (ADR-014): identity with a
// pinned semver, UX copy, services, requirements, exposure
// declaration, backup paths, SSO posture, and optional database export plus
// consistency hooks (S180). The exposure package name must equal the
// manifest name — one package, one declaration, no drift — and
// backup paths stay relative so they resolve under assigned mounts.
type Manifest struct {
	Name         string
	Version      string
	Title        string
	Blurb        string
	Description  string
	Category     string
	Icon         string
	Services     []Service
	Requirements Requirements
	Exposure     exposure.PackageExposure
	BackupPaths  []string
	Database     *DatabaseBackup
	Hooks        []BackupHook
	SSO          SSODeclaration
	// Secrets declares secret-backed container variables (S266,
	// ADR-021): delivered as 0600 files via Compose `secrets:`.
	Secrets []SecretDecl
}

// Validate checks the manifest end to end.
func (m Manifest) Validate() error {
	if !validPackageName.MatchString(m.Name) {
		return fmt.Errorf("catalog: package must match [a-z][a-z0-9-]*")
	}
	if !validVersion.MatchString(m.Version) {
		return fmt.Errorf("catalog: version must be numeric dotted (x.y or x.y.z)")
	}
	if len(m.Title) == 0 || len(m.Title) > 64 {
		return fmt.Errorf("catalog: title must be 1-64 characters")
	}
	if len(m.Blurb) == 0 || len(m.Blurb) > 200 {
		return fmt.Errorf("catalog: blurb must be 1-200 characters")
	}
	if len(m.Description) == 0 || len(m.Description) > 2000 {
		return fmt.Errorf("catalog: description must be 1-2000 characters")
	}
	if len(m.Category) == 0 || len(m.Category) > 32 {
		return fmt.Errorf("catalog: category must be 1-32 characters")
	}
	if len(m.Icon) == 0 || len(m.Icon) > 64 {
		return fmt.Errorf("catalog: icon must be 1-64 characters")
	}
	for _, r := range m.Icon {
		if r != ':' && r != '/' && r != '-' && r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("catalog: invalid icon %q", m.Icon)
		}
	}
	if len(m.Services) == 0 {
		return fmt.Errorf("catalog: at least one service is required")
	}
	if err := m.Exposure.Validate(); err != nil {
		return err
	}
	if m.Exposure.Package != m.Name {
		return fmt.Errorf("catalog: exposure package %q must equal manifest %q", m.Exposure.Package, m.Name)
	}
	endpoints := map[string]bool{}
	for _, endpoint := range m.Exposure.Endpoints {
		endpoints[endpoint.Endpoint] = true
	}
	services := map[string]bool{}
	for _, service := range m.Services {
		services[service.Name] = true
	}
	seenServices := map[string]bool{}
	for i := range m.Services {
		if err := m.Services[i].Validate(endpoints); err != nil {
			return fmt.Errorf("catalog: service #%d: %w", i, err)
		}
		if seenServices[m.Services[i].Name] {
			return fmt.Errorf("catalog: duplicate service %q", m.Services[i].Name)
		}
		seenServices[m.Services[i].Name] = true
	}
	if err := validateCraftServices(m); err != nil {
		return err
	}
	if err := m.Requirements.Validate(); err != nil {
		return err
	}
	if err := validateData(m.Services, m.Requirements); err != nil {
		return err
	}
	for i, path := range m.BackupPaths {
		if path == "" || path[0] == '/' || path == "." || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") {
			return fmt.Errorf("catalog: backup path #%d must be relative and contained", i)
		}
	}
	if m.Database != nil {
		if err := m.Database.Validate(services); err != nil {
			return err
		}
	}
	if err := m.SSO.Validate(); err != nil {
		return err
	}
	if err := validateSecrets(m, services); err != nil {
		return err
	}
	for i := range m.Hooks {
		if err := m.Hooks[i].Validate(services); err != nil {
			return fmt.Errorf("catalog: hook #%d: %w", i, err)
		}
	}
	return nil
}

// Stateless reports a package that keeps nothing on the appliance
// (ADR-028): every data declaration is ephemeral (or there are none), and
// the manifest names no backup paths and no database export. Stateless
// packages are not backup targets; the rule derives from the manifest, so a
// package that later declares persistent data stops being stateless.
func (m Manifest) Stateless() bool {
	if m.Database != nil || len(m.BackupPaths) > 0 {
		return false
	}
	for _, service := range m.Services {
		for _, data := range service.Data {
			if !data.Ephemeral {
				return false
			}
		}
	}
	return true
}

// CraftImages returns the locally built craft images (S249) the manifest's
// services run, in service order.
func (m Manifest) CraftImages() []string {
	var out []string
	for _, service := range m.Services {
		if _, _, ok := craft.ParseRef(service.Image); ok {
			out = append(out, service.Image)
		}
	}
	return out
}

// validateCraftServices checks the craft form (ADR-028): an image that
// names a craft build must be exactly the local tag
// `homestead/craft-<app>:<version>`, never registry-qualified (craft
// images are built on the appliance, not pulled), for the package's own
// app and at the package's own version, so a manifest version bump and the
// craft manifest move together.
func validateCraftServices(m Manifest) error {
	for _, service := range m.Services {
		if !strings.Contains(service.Image, craftMarker) {
			continue
		}
		app, version, ok := craft.ParseRef(service.Image)
		if !ok {
			return fmt.Errorf("catalog: service %q image %q: craft images use the local tag homestead/craft-<app>:<version> and are built on the appliance, never pulled from a registry", service.Name, service.Image)
		}
		if app != m.Name {
			return fmt.Errorf("catalog: service %q image %q must be the package's own craft app %q", service.Name, service.Image, m.Name)
		}
		if version != m.Version {
			return fmt.Errorf("catalog: service %q image %q must carry the manifest version %q", service.Name, service.Image, m.Version)
		}
	}
	return nil
}

// ValidateCraftListing requires every craft image of the manifest to be
// listed (app and version) in the craft release manifest the caller
// supplies. Shape validation stays pure; listing needs the signed
// manifest, so callers with one (tests over the shipped manifest, tooling)
// run this beside Validate.
func ValidateCraftListing(m Manifest, listed func(app, version string) bool) error {
	for _, image := range m.CraftImages() {
		app, version, _ := craft.ParseRef(image)
		if !listed(app, version) {
			return fmt.Errorf("catalog: craft image %s is not listed in the craft manifest", image)
		}
	}
	return nil
}
