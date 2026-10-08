package catalog

import (
	"fmt"
	"regexp"
	"strings"

	"homestead/internal/exposure"
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
}

// Validate checks service shape and endpoint linkage.
func (s Service) Validate(endpoints map[string]bool) error {
	if !validServiceName.MatchString(s.Name) {
		return fmt.Errorf("catalog: invalid service name %q", s.Name)
	}
	image, tag, found := strings.Cut(s.Image, ":")
	if !found || image == "" || tag == "" || tag == "latest" {
		return fmt.Errorf("catalog: image %q must pin a non-latest tag", s.Image)
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

// Manifest is one first-party application (ADR-014): identity with a
// pinned semver, UX copy, services, requirements, exposure
// declaration, and backup paths. The exposure package name must equal
// the manifest name — one package, one declaration, no drift — and
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
	if err := m.Requirements.Validate(); err != nil {
		return err
	}
	for i, path := range m.BackupPaths {
		if path == "" || path[0] == '/' || path == "." || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") {
			return fmt.Errorf("catalog: backup path #%d must be relative and contained", i)
		}
	}
	return nil
}
