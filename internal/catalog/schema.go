package catalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"homestead/internal/craft"
	"homestead/internal/docker"
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
	// Entrypoint names the image's own entrypoint when Homestead has to
	// supervise startup (S342). The generated stack replaces ENTRYPOINT
	// with the secret-entrypoint supervisor and passes this list, plus
	// Command, as the command it execs. Required for any service that
	// exports a secret value into the environment.
	Entrypoint []string
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
	for _, arg := range s.Entrypoint {
		if arg == "" {
			return fmt.Errorf("catalog: service %q has an empty entrypoint argument", s.Name)
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
		for _, token := range []string{RouteHostToken, RouteNamesToken, RoutePathToken, RouteURLToken} {
			if strings.Contains(value, token) && s.Exposes == "" {
				return fmt.Errorf("catalog: service %q variable %q uses %s but the service exposes no endpoint", s.Name, key, token)
			}
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
	// OIDC names the first-party adapter Homestead configures the app's
	// OpenID Connect sign-in through (S298, ADR-039 §3); nil when the app
	// cannot be signed in to through Authentik.
	OIDC *OIDCAdapter `yaml:"oidc,omitempty" json:"oidc,omitempty"`
	// Client declares the client an OIDC provider must host for this
	// package (S342). Present only when the class admits SSO and the
	// protocols include oidc. It is separate from OIDC, which names the
	// adapter that configures an existing app's side of the sign-in.
	Client *OIDCClient `yaml:"client,omitempty" json:"client,omitempty"`
}

// OIDC adapters (S298): each is first-party Go code in internal/appsso.
const (
	AdapterImmich      = "immich"
	AdapterNextcloud   = "nextcloud-user-oidc"
	AdapterJellyfinSSO = "jellyfin-sso-plugin"
)

// OIDCAdapter declares how an app takes OpenID Connect sign-in: the
// adapter, the service that talks to Authentik, and for Jellyfin the
// third-party plugin pinned by release and checksum (ADR-039 §3).
type OIDCAdapter struct {
	Adapter string        `yaml:"adapter" json:"adapter"`
	Service string        `yaml:"service" json:"service"`
	Plugin  *PinnedPlugin `yaml:"plugin,omitempty" json:"plugin,omitempty"`
}

// PinnedPlugin is one third-party plugin release: Homestead downloads
// exactly URL and refuses it unless its SHA-256 matches.
type PinnedPlugin struct {
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
	URL     string `yaml:"url" json:"url"`
	SHA256  string `yaml:"sha256" json:"sha256"`
}

var (
	pluginVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}$`)
	pluginSHA256  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// validate checks the adapter against the manifest's services: a known
// adapter, an existing service that serves an endpoint, OIDC among the
// protocols, and a plugin pin exactly for the Jellyfin adapter.
func (a OIDCAdapter) validate(protocols []identity.Capability, services map[string]Service) error {
	switch a.Adapter {
	case AdapterImmich, AdapterNextcloud, AdapterJellyfinSSO:
	default:
		return fmt.Errorf("catalog: OIDC adapter %q unknown", a.Adapter)
	}
	oidc := false
	for _, p := range protocols {
		oidc = oidc || p == identity.CapabilityOIDC
	}
	if !oidc {
		return fmt.Errorf("catalog: an OIDC adapter needs the oidc protocol")
	}
	service, ok := services[a.Service]
	if !ok || service.Exposes == "" {
		return fmt.Errorf("catalog: OIDC adapter service %q must be a service that serves an endpoint", a.Service)
	}
	if a.Adapter != AdapterJellyfinSSO {
		if a.Plugin != nil {
			return fmt.Errorf("catalog: only the %s adapter pins a plugin", AdapterJellyfinSSO)
		}
		return nil
	}
	p := a.Plugin
	if p == nil {
		return fmt.Errorf("catalog: the %s adapter must pin the plugin release and its checksum", AdapterJellyfinSSO)
	}
	u, err := url.Parse(p.URL)
	switch {
	case strings.TrimSpace(p.Name) == "":
		return fmt.Errorf("catalog: the pinned plugin needs a name")
	case !pluginVersion.MatchString(p.Version):
		return fmt.Errorf("catalog: plugin version %q is not a release number", p.Version)
	case err != nil || u.Scheme != "https" || u.Host == "" || !strings.HasSuffix(u.Path, ".zip") || !strings.Contains(u.Path, p.Version):
		return fmt.Errorf("catalog: plugin url must be the https .zip of release %s", p.Version)
	case !pluginSHA256.MatchString(p.SHA256):
		return fmt.Errorf("catalog: plugin sha256 must be 64 lowercase hex digits")
	}
	return nil
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
	if d.Client != nil {
		if d.Class.Effective() == SSOUnsupported {
			return fmt.Errorf("catalog: unsupported SSO takes no OIDC client")
		}
		ok := false
		for _, protocol := range d.Protocols {
			if protocol == identity.CapabilityOIDC {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("catalog: an OIDC client needs the oidc protocol")
		}
	}
	return nil
}

// BrowserCapability names a client-side browser feature a package relies on.
type BrowserCapability string

// Known browser capabilities. The set is closed so every capability
// carries reviewed plain-language wording; extend it with the package
// that first needs a new one.
const (
	BrowserWebCodecs     BrowserCapability = "webcodecs"
	BrowserWebGPU        BrowserCapability = "webgpu"
	BrowserWebGL2        BrowserCapability = "webgl2"
	BrowserWebAudio      BrowserCapability = "webaudio"
	BrowserAudioOutput   BrowserCapability = "audio-output"
	BrowserSecureContext BrowserCapability = "secure-context"
	BrowserClipboard     BrowserCapability = "clipboard"
	BrowserStorage       BrowserCapability = "browser-storage"
)

// Valid reports a known capability.
func (c BrowserCapability) Valid() bool {
	switch c {
	case BrowserWebCodecs, BrowserWebGPU, BrowserWebGL2, BrowserWebAudio,
		BrowserAudioOutput, BrowserSecureContext, BrowserClipboard, BrowserStorage:
		return true
	}
	return false
}

// browserLabels is the single source of plain-language capability names;
// the API sends them so the CLI and dashboard never keep their own table.
var browserLabels = map[BrowserCapability]string{
	BrowserWebCodecs:     "Video codecs (WebCodecs)",
	BrowserWebGPU:        "GPU graphics (WebGPU)",
	BrowserWebGL2:        "Graphics (WebGL2)",
	BrowserWebAudio:      "Audio engine (Web Audio)",
	BrowserAudioOutput:   "Audio output device",
	BrowserSecureContext: "Secure connection (HTTPS)",
	BrowserClipboard:     "Clipboard access",
	BrowserStorage:       "Browser storage",
}

// Label is the plain-language name, or the slug for an unknown capability.
func (c BrowserCapability) Label() string {
	if label, ok := browserLabels[c]; ok {
		return label
	}
	return string(c)
}

// MarshalJSON adds the derived Label to the API shape (S252).
func (r BrowserRequirement) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Capability BrowserCapability
		Label      string
		Reason     string
	}{r.Capability, r.Capability.Label(), r.Reason})
}

// BrowserRequirement is one client-side capability a package needs and
// the plain-language reason, shown as a non-blocking warning on the
// install plan and package page (S252, ADR-004: one field, both surfaces).
type BrowserRequirement struct {
	Capability BrowserCapability
	Reason     string
}

func validateBrowser(reqs []BrowserRequirement) error {
	seen := map[BrowserCapability]bool{}
	for i, req := range reqs {
		if !req.Capability.Valid() {
			return fmt.Errorf("catalog: browser requirement #%d: unknown capability %q", i, req.Capability)
		}
		if seen[req.Capability] {
			return fmt.Errorf("catalog: duplicate browser capability %q", req.Capability)
		}
		seen[req.Capability] = true
		if strings.TrimSpace(req.Reason) == "" || utf8.RuneCountInString(req.Reason) > 300 {
			return fmt.Errorf("catalog: browser capability %q needs a reason of 1-300 characters", req.Capability)
		}
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
	// Browser lists the browser capabilities the app needs (S252). They
	// are advisories only: the appliance cannot see the owner's browser,
	// so nothing refuses install, update or start on these grounds.
	Browser []BrowserRequirement
	// Secrets declares secret-backed container variables (S266,
	// ADR-021): delivered as 0600 files via Compose `secrets:`.
	Secrets []SecretDecl
	// Serve says how the edge serves the app (ADR-044): ServePath under
	// the appliance's own names at Path, or ServeHost on a name of its
	// own. Empty is ServeHost, the serving every package had before.
	Serve string
	// Path is the app's path when served by path (default /apps/<name>/,
	// under the reserved /apps/ prefix). It is the same on every name,
	// because the app's base-path setting is global to the app.
	Path string
	// StripPrefix removes Path before the request reaches the app: for
	// apps whose base-path setting only rewrites the links they generate.
	StripPrefix bool `yaml:"strip_prefix"`
	// Redirects send fixed /.well-known/ paths on the appliance's names
	// into the app's path (Nextcloud's CalDAV and CardDAV discovery).
	Redirects []docker.PathRedirect
	// Seeds are first-start files a package writes into its own data
	// before the app first starts (Jellyfin's network.xml carrying its
	// Base URL). A file that exists is never overwritten.
	Seeds []Seed
	// FirstRun names the first-party routine that finishes the app's own
	// first-run setup once it answers (S318), for apps that cannot set
	// themselves up from their environment, as Nextcloud does.
	FirstRun *FirstRun `yaml:"firstrun,omitempty" json:"firstrun,omitempty"`
	// Integrations declares how this package relates to the rest of the
	// appliance (S341, ADR-051). Advisory metadata: nothing in the
	// install, update or start path reads it, and it never causes
	// Homestead to configure another app.
	Integrations []Integration `yaml:"integrations,omitempty"`
}

// First-run routines (S318): each is first-party Go code in
// internal/firstrun; the manifest only names one.
const (
	FirstRunImmich   = "immich"
	FirstRunJellyfin = "jellyfin"
)

// Library kinds a first run may create (Jellyfin's collection types).
var libraryKinds = map[string]bool{"movies": true, "tvshows": true, "music": true}

// FirstRun declares the app's first-run routine: the service it talks
// to, the administrator account it creates and the declared secret
// holding that account's password. The secret is held by Homestead
// alone (it needs no consumer) and is revealable like Nextcloud's.
type FirstRun struct {
	Routine string `yaml:"routine" json:"routine"`
	Service string `yaml:"service" json:"service"`
	Account string `yaml:"account" json:"account"`
	Secret  string `yaml:"secret" json:"secret"`
	// Libraries are the media libraries the first run creates (S319,
	// Jellyfin only), each on one of the service's declared data paths.
	Libraries []Library `yaml:"libraries,omitempty" json:"libraries,omitempty"`
}

// Library is one media library: its name in the app, its kind and the
// container path it reads.
type Library struct {
	Name string `yaml:"name" json:"name"`
	Kind string `yaml:"kind" json:"kind"`
	Path string `yaml:"path" json:"path"`
}

func (f FirstRun) validate(m Manifest, services map[string]bool) error {
	switch f.Routine {
	case FirstRunImmich, FirstRunJellyfin:
	default:
		return fmt.Errorf("catalog: unknown first-run routine %q", f.Routine)
	}
	if !services[f.Service] {
		return fmt.Errorf("catalog: first-run service %q unknown", f.Service)
	}
	if len(f.Libraries) > 0 && f.Routine != FirstRunJellyfin {
		return fmt.Errorf("catalog: first-run %q creates no libraries", f.Routine)
	}
	targets := map[string]bool{}
	for _, svc := range m.Services {
		if svc.Name == f.Service {
			for _, d := range svc.Data {
				if !d.Ephemeral {
					targets[d.Target] = true
				}
			}
		}
	}
	names := map[string]bool{}
	for _, lib := range f.Libraries {
		if strings.TrimSpace(lib.Name) == "" || names[lib.Name] {
			return fmt.Errorf("catalog: first-run library name %q missing or repeated", lib.Name)
		}
		names[lib.Name] = true
		if !libraryKinds[lib.Kind] {
			return fmt.Errorf("catalog: first-run library %q kind %q unknown", lib.Name, lib.Kind)
		}
		if !targets[lib.Path] {
			return fmt.Errorf("catalog: first-run library %q path %q is not a persistent data path of %s", lib.Name, lib.Path, f.Service)
		}
	}
	secrets := m.Secrets
	if strings.TrimSpace(f.Account) == "" {
		return fmt.Errorf("catalog: first-run %q needs an account", f.Routine)
	}
	for _, decl := range secrets {
		if decl.Name == f.Secret {
			if !decl.Reveal || !decl.Immutable {
				return fmt.Errorf("catalog: first-run secret %q must be immutable and revealable", f.Secret)
			}
			return nil
		}
	}
	return fmt.Errorf("catalog: first-run secret %q is not declared", f.Secret)
}

// HeldSecret reports a declared secret Homestead holds for itself (the
// first-run account's password): it has no consumer service.
func (m Manifest) HeldSecret(name string) bool {
	return m.FirstRun != nil && m.FirstRun.Secret == name
}

// Seed is one first-start file: Target is a container path inside one
// of Service's persistent data paths. Content may use RoutePathToken.
type Seed struct {
	Service string
	Target  string
	Content string
}

// validateSeeds: each seed lands inside a persistent data path of its
// service, at most once, with bounded content.
func (m Manifest) validateSeeds() error {
	seen := map[string]bool{}
	for _, seed := range m.Seeds {
		if len(seed.Content) == 0 || len(seed.Content) > 8192 {
			return fmt.Errorf("catalog: seed %q content must be 1-8192 bytes", seed.Target)
		}
		if seen[seed.Service+"\x00"+seed.Target] {
			return fmt.Errorf("catalog: duplicate seed %q", seed.Target)
		}
		seen[seed.Service+"\x00"+seed.Target] = true
		if _, ok := seedMount(m, seed); !ok {
			return fmt.Errorf("catalog: seed %q must sit inside a persistent data path of service %q", seed.Target, seed.Service)
		}
	}
	return nil
}

// seedMount finds the persistent data path holding a seed.
func seedMount(m Manifest, seed Seed) (DataPath, bool) {
	if !strings.HasPrefix(seed.Target, "/") || strings.Contains(seed.Target, "..") || strings.HasSuffix(seed.Target, "/") {
		return DataPath{}, false
	}
	for _, service := range m.Services {
		if service.Name != seed.Service {
			continue
		}
		for _, data := range service.Data {
			if !data.Ephemeral && strings.HasPrefix(seed.Target, strings.TrimSuffix(data.Target, "/")+"/") {
				return data, true
			}
		}
	}
	return DataPath{}, false
}

// Serving modes (ADR-044).
const (
	ServeHost = "host"
	ServePath = "path"
)

// ServedByPath reports a package served under the appliance's names.
func (m Manifest) ServedByPath() bool { return m.Serve == ServePath }

// AppPath is the package's path when served by path: Path, or
// /apps/<name>/ by default. Empty for host packages.
func (m Manifest) AppPath() string {
	if !m.ServedByPath() {
		return ""
	}
	if m.Path != "" {
		return m.Path
	}
	return docker.AppPathFor(m.Name)
}

// validateServe checks the serving declaration (ADR-044).
func (m Manifest) validateServe() error {
	switch m.Serve {
	case "", ServeHost:
		if m.Path != "" || m.StripPrefix || len(m.Redirects) > 0 {
			return fmt.Errorf("catalog: path, strip_prefix and redirects need serve: path")
		}
		return nil
	case ServePath:
	default:
		return fmt.Errorf("catalog: serve must be path or host")
	}
	if len(m.Exposure.Endpoints) != 1 {
		return fmt.Errorf("catalog: serve: path needs exactly one endpoint")
	}
	if len(m.CraftImages()) > 0 && !m.StripPrefix {
		return fmt.Errorf("catalog: a craft app served by path strips its path (strip_prefix: true, ADR-045)")
	}
	probe := docker.Route{Host: "probe.lan", Target: docker.Target{Host: "probe", Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, Path: m.AppPath(),
		StripPrefix: m.StripPrefix, Redirects: m.Redirects}
	if err := probe.Validate(); err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	return nil
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
	if err := m.validateServe(); err != nil {
		return err
	}
	if err := m.validateSeeds(); err != nil {
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
	if m.SSO.OIDC != nil {
		byName := map[string]Service{}
		for _, service := range m.Services {
			byName[service.Name] = service
		}
		if err := m.SSO.OIDC.validate(m.SSO.Protocols, byName); err != nil {
			return err
		}
	}
	if err := validateBrowser(m.Browser); err != nil {
		return err
	}
	if err := validateOIDC(m); err != nil {
		return err
	}
	if err := validateSecrets(m, services); err != nil {
		return err
	}
	if m.FirstRun != nil {
		if err := m.FirstRun.validate(m, services); err != nil {
			return err
		}
	}
	if err := m.validateIntegrations(); err != nil {
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
