package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"homestead/internal/docker"
)

// Running container drift (S262). The comparison is read-only: it
// derives desired state from the stack's compose.yaml (plus override),
// compares it with the Engine inventory, and reports findings. Nothing
// is corrected; resolution stays the owner's choice on the S213 flow.
//
// Normalizations (each one avoids a documented false positive):
//   - Images: docker.io/ and library/ prefixes drop; a missing tag
//     reads as :latest; an image reference the Engine reports as an
//     image ID (its tag moved) resolves through the image list, and an
//     ID the list cannot resolve is not drift. Digest pins compare by
//     digest, via the image's RepoDigests when the container reports a
//     tag. Services built from source (no image:) skip the image check.
//   - Ports: protocol defaults to tcp; 0.0.0.0, :: and empty host IPs
//     are the same wildcard, and the Engine's separate IPv4/IPv6
//     entries collapse; the host IP is compared only when compose
//     names a specific one; container-only (ephemeral) mappings expect
//     only that the container port is published somewhere; exposed but
//     unpublished ports are ignored; stopped containers report no
//     ports, so ports are compared for running containers only.
//   - Networks: a service with no networks: attaches to the project
//     default network; names resolve to project_key unless the
//     top-level network is external or carries name:; services with
//     network_mode skip the check.
//   - Mounts: relative bind sources resolve against the stack
//     directory; paths are cleaned (symlinks resolved when both
//     exist); compose secrets are expected read-only file mounts at
//     /run/secrets and their host source is not compared; anonymous
//     volumes at destinations compose does not declare are the
//     image's own VOLUME declarations and are ignored; /etc/hosts,
//     /etc/hostname, and /etc/resolv.conf are Engine-managed; tmpfs
//     size is not compared (the Engine does not list it).
//   - Containers: services behind profiles are not expected; scale
//     and deploy.replicas set the expected count; a run-once service
//     (restart no) that exited 0 is finished, not stopped; one-off
//     `compose run` containers are not stack state.
//   - Override: image replaces, ports, volumes, and tmpfs append,
//     networks union, and secrets append; environment never matters
//     to live comparison.

// Live finding kinds.
const (
	LiveImage      = "image"
	LivePort       = "port"
	LiveNetwork    = "network"
	LiveMount      = "mount"
	LiveMissing    = "missing"
	LiveStopped    = "stopped"
	LiveRestarting = "restarting"
	LiveExtra      = "extra"
)

// Live finding resolutions, naming which path converges the finding.
const (
	// ResolveRestore: S213 restore (rewrite recorded files, compose up)
	// starts missing and stopped containers and recreates ones whose
	// compose configuration or image changed.
	ResolveRestore = "restore"
	// ResolveRecreate: the container diverged from a compose file that
	// may already be right, so compose up may leave it. Remove the
	// container (`docker compose rm -sf <service>`) and run restore, or
	// reconfigure the service.
	ResolveRecreate = "recreate"
	// ResolveManual: no Homestead flow converges it (crash loops, extra
	// containers); investigate or remove by hand.
	ResolveManual = "manual"
)

// LiveFinding is one divergence between a running container and the
// desired state in the generated compose files.
type LiveFinding struct {
	Service    string `json:"service"`
	Container  string `json:"container,omitempty"`
	Kind       string `json:"kind"`
	Detail     string `json:"detail"`
	Resolution string `json:"resolution"`
	// Hint is the exact next step for findings Homestead cannot fix
	// alone (recreate and manual).
	Hint string `json:"hint,omitempty"`
}

// LiveSource supplies the Engine inventory; the Docker client satisfies
// it and tests fake it.
type LiveSource interface {
	Inventory(ctx context.Context) (docker.Inventory, error)
}

// LiveReview is the live comparison for one package. Checked is false
// when the Engine was unreachable: nothing could be compared, which is
// not the same as clean.
type LiveReview struct {
	Checked  bool          `json:"checked"`
	Findings []LiveFinding `json:"findings"`
	// StackState is "stopped" when no expected service has a running
	// container: a run state the owner may have chosen (ADR-005), shown
	// as information and never as drift. Empty otherwise.
	StackState string `json:"stack_state,omitempty"`
	// Unsupported lists compose features the comparison cannot judge
	// (interpolation, extends, include, unparseable ports). Those fields
	// are skipped, never reported as drift.
	Unsupported []string `json:"unsupported,omitempty"`
}

// ErrLiveCompose marks compose files the live comparison cannot parse.
var ErrLiveCompose = errors.New("catalog: compose not readable for live comparison")

// LiveDrift reads the stack's compose files and compares them with the
// Engine inventory from src. Unreachable engines report Checked false.
// The compose read holds the per-project lock, so it never sees a file
// mid-restore.
func LiveDrift(ctx context.Context, src LiveSource, stack, dir string) (LiveReview, error) {
	unlock := lockProject(stack)
	desired, err := loadDesired(stack, dir)
	unlock()
	if err != nil {
		return LiveReview{}, err
	}
	inv, err := src.Inventory(ctx)
	if err != nil {
		return LiveReview{}, fmt.Errorf("catalog: read live state: %w", err)
	}
	return reviewLive(desired, inv), nil
}

// LiveDriftInventory compares one stack with an inventory already read,
// so scans query the Engine once for every package. Callers hold the
// project lock (scans do).
func LiveDriftInventory(stack, dir string, inv docker.Inventory) (LiveReview, error) {
	desired, err := loadDesired(stack, dir)
	if err != nil {
		return LiveReview{}, err
	}
	return reviewLive(desired, inv), nil
}

func reviewLive(desired desiredStack, inv docker.Inventory) LiveReview {
	if !inv.Reachable {
		return LiveReview{Findings: []LiveFinding{}}
	}
	review := LiveReview{Checked: true, Findings: []LiveFinding{}, Unsupported: desired.Notes}
	if desired.Unsupported {
		return review
	}
	review.Findings, review.StackState = compareLive(desired, inv)
	return review
}

// ---- desired state ----

type desiredPort struct {
	IP        string // "" is the wildcard
	Host      int    // 0: any host port
	Container int
	Proto     string
}

type desiredMount struct {
	Type     string // bind, volume, tmpfs
	Source   string // bind: absolute host path; volume: resolved name ("" anonymous)
	Target   string
	ReadOnly bool
	Secret   bool
}

type desiredService struct {
	Name        string
	Image       string
	Restart     string
	NetworkMode string
	Profiled    bool
	Expected    int
	Ports       []desiredPort
	Mounts      []desiredMount
	Networks    []string // resolved network names
	// Unsupported flags skip one comparison for this service because
	// compose uses interpolation or syntax the comparison cannot judge.
	PortsUnsupported, MountsUnsupported, NetworksUnsupported bool
}

type desiredStack struct {
	Project  string
	Services map[string]*desiredService
	// Unsupported marks the whole package (extends, include).
	Unsupported bool
	Notes       []string
}

type rawService struct {
	Image       string   `yaml:"image"`
	Restart     string   `yaml:"restart"`
	NetworkMode string   `yaml:"network_mode"`
	Profiles    []string `yaml:"profiles"`
	Scale       *int     `yaml:"scale"`
	Deploy      struct {
		Replicas *int `yaml:"replicas"`
	} `yaml:"deploy"`
	Ports    []any `yaml:"ports"`
	Volumes  []any `yaml:"volumes"`
	Tmpfs    any   `yaml:"tmpfs"`
	Secrets  []any `yaml:"secrets"`
	Networks any   `yaml:"networks"`
	Extends  any   `yaml:"extends"`
}

type rawResource struct {
	Name     string `yaml:"name"`
	External any    `yaml:"external"`
}

type rawCompose struct {
	Name     string                 `yaml:"name"`
	Services map[string]rawService  `yaml:"services"`
	Networks map[string]rawResource `yaml:"networks"`
	Volumes  map[string]rawResource `yaml:"volumes"`
	Include  any                    `yaml:"include"`
}

func readCompose(file string, required bool) (rawCompose, bool, error) {
	var doc rawCompose
	raw, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return doc, false, nil
		}
		return doc, false, fmt.Errorf("catalog: read %s: %w", filepath.Base(file), err)
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return doc, false, fmt.Errorf("%w: parse %s: %v", ErrLiveCompose, filepath.Base(file), err)
	}
	return doc, true, nil
}

func loadDesired(stack, dir string) (desiredStack, error) {
	base, _, err := readCompose(filepath.Join(dir, "compose.yaml"), true)
	if err != nil {
		return desiredStack{}, err
	}
	over, _, err := readCompose(filepath.Join(dir, OverrideFileName), false)
	if err != nil {
		return desiredStack{}, err
	}
	for name, o := range over.Services {
		b := base.Services[name]
		if o.Image != "" {
			b.Image = o.Image
		}
		b.Ports = append(b.Ports, o.Ports...)
		b.Volumes = append(b.Volumes, o.Volumes...)
		b.Secrets = append(b.Secrets, o.Secrets...)
		b.Tmpfs = mergeTmpfs(b.Tmpfs, o.Tmpfs)
		b.Networks = mergeNetworks(b.Networks, o.Networks)
		base.Services[name] = b
	}
	project := base.Name
	if project == "" {
		project = stack
	}
	out := desiredStack{Project: project, Services: map[string]*desiredService{}}
	note := func(format string, args ...any) { out.Notes = append(out.Notes, fmt.Sprintf(format, args...)) }
	if base.Include != nil || over.Include != nil {
		note("compose include: is not supported; live comparison skipped for this package")
		out.Unsupported = true
	}
	for name, svc := range base.Services {
		if svc.Extends != nil {
			note("service %s uses extends:; live comparison skipped for this package", name)
			out.Unsupported = true
		}
	}
	names := make([]string, 0, len(base.Services))
	for name := range base.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := base.Services[name]
		d := &desiredService{
			Name: name, Image: svc.Image, Restart: svc.Restart,
			NetworkMode: svc.NetworkMode, Profiled: len(svc.Profiles) > 0, Expected: 1,
		}
		switch {
		case svc.Scale != nil:
			d.Expected = *svc.Scale
		case svc.Deploy.Replicas != nil:
			d.Expected = *svc.Deploy.Replicas
		}
		if containsInterp(svc.Image) {
			d.Image = ""
			note("service %s image uses interpolation; image comparison skipped", name)
		}
		for _, p := range svc.Ports {
			parsed := parsePort(p)
			if containsInterp(p) || len(parsed) == 0 {
				d.PortsUnsupported = true
				continue
			}
			d.Ports = append(d.Ports, parsed...)
		}
		if d.PortsUnsupported {
			d.Ports = nil
			note("service %s has ports the comparison cannot evaluate (interpolation or unsupported syntax); port comparison skipped", name)
		}
		for _, v := range svc.Volumes {
			m, ok := parseMount(v, dir, project, base.Volumes)
			if containsInterp(v) || !ok {
				d.MountsUnsupported = true
				continue
			}
			d.Mounts = append(d.Mounts, m)
		}
		if d.MountsUnsupported {
			d.Mounts = nil
			note("service %s has volumes the comparison cannot evaluate (interpolation or unsupported syntax); mount comparison skipped", name)
		}
		if containsInterp(svc.Networks) {
			d.NetworksUnsupported = true
			note("service %s networks use interpolation; network comparison skipped", name)
		}
		for _, t := range tmpfsTargets(svc.Tmpfs) {
			d.Mounts = append(d.Mounts, desiredMount{Type: "tmpfs", Target: t})
		}
		for _, s := range svc.Secrets {
			if t := secretTarget(s); t != "" {
				d.Mounts = append(d.Mounts, desiredMount{Type: "bind", Target: t, ReadOnly: true, Secret: true})
			}
		}
		d.Networks = resolveNetworks(svc.Networks, project, base.Networks)
		out.Services[name] = d
	}
	return out, nil
}

var interpPattern = regexp.MustCompile(`\$(\{|[A-Za-z_])`)

// containsInterp reports compose variable interpolation anywhere in a
// decoded value: its resolved value depends on an environment the
// comparison does not reproduce. "$$" is a literal dollar.
func containsInterp(v any) bool {
	switch t := v.(type) {
	case string:
		return interpPattern.MatchString(strings.ReplaceAll(t, "$$", ""))
	case []any:
		for _, e := range t {
			if containsInterp(e) {
				return true
			}
		}
	case map[string]any:
		for k, e := range t {
			if containsInterp(k) || containsInterp(e) {
				return true
			}
		}
	}
	return false
}

func mergeTmpfs(a, b any) any {
	merged := append(tmpfsTargets(a), tmpfsTargets(b)...)
	if len(merged) == 0 {
		return nil
	}
	list := make([]any, 0, len(merged))
	for _, m := range merged {
		list = append(list, m)
	}
	return list
}

func mergeNetworks(a, b any) any {
	keys := append(networkKeys(a), networkKeys(b)...)
	if len(keys) == 0 {
		return nil
	}
	list := make([]any, 0, len(keys))
	for _, k := range keys {
		list = append(list, k)
	}
	return list
}

func tmpfsTargets(v any) []string {
	var out []string
	add := func(s string) {
		if s = strings.SplitN(s, ":", 2)[0]; s != "" {
			out = append(out, path.Clean(s))
		}
	}
	switch t := v.(type) {
	case string:
		add(t)
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				add(s)
			}
		}
	}
	return out
}

func networkKeys(v any) []string {
	var out []string
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	case map[string]any:
		for k := range t {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func resolveNetworks(v any, project string, top map[string]rawResource) []string {
	keys := networkKeys(v)
	if len(keys) == 0 {
		keys = []string{"default"}
	}
	var out []string
	for _, key := range keys {
		out = append(out, resourceName(key, project, top[key]))
	}
	sort.Strings(out)
	return out
}

// resourceName resolves a compose network or volume key to the name the
// Engine uses: explicit name:, the key itself when external, otherwise
// project-scoped.
func resourceName(key, project string, r rawResource) string {
	if r.Name != "" {
		return r.Name
	}
	switch e := r.External.(type) {
	case bool:
		if e {
			return key
		}
	case map[string]any:
		if n, ok := e["name"].(string); ok && n != "" {
			return n
		}
		return key
	}
	return project + "_" + key
}

func wildcardIP(ip string) string {
	ip = strings.Trim(ip, "[]")
	if ip == "0.0.0.0" || ip == "::" {
		return ""
	}
	return ip
}

func parsePort(v any) []desiredPort {
	switch t := v.(type) {
	case int:
		return []desiredPort{{Container: t, Proto: "tcp"}}
	case string:
		return parsePortString(t)
	case map[string]any:
		var p desiredPort
		p.Proto = "tcp"
		if s, ok := t["protocol"].(string); ok && s != "" {
			p.Proto = strings.ToLower(s)
		}
		p.Container = anyInt(t["target"])
		p.Host = anyInt(t["published"])
		if s, ok := t["host_ip"].(string); ok {
			p.IP = wildcardIP(s)
		}
		if p.Container > 0 {
			return []desiredPort{p}
		}
	}
	return nil
}

func anyInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

func portRange(s string) (int, int, bool) {
	lo, hi, found := strings.Cut(s, "-")
	a, err := strconv.Atoi(lo)
	if err != nil {
		return 0, 0, false
	}
	if !found {
		return a, a, true
	}
	b, err := strconv.Atoi(hi)
	if err != nil || b < a {
		return 0, 0, false
	}
	return a, b, true
}

func parsePortString(s string) []desiredPort {
	proto := "tcp"
	if i := strings.LastIndex(s, "/"); i >= 0 {
		proto = strings.ToLower(s[i+1:])
		s = s[:i]
	}
	ip := ""
	var parts []string
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return nil
		}
		ip = s[1:end]
		parts = append([]string{ip}, strings.Split(strings.TrimPrefix(s[end+1:], ":"), ":")...)
	} else {
		parts = strings.Split(s, ":")
	}
	var hostS, ctrS string
	switch len(parts) {
	case 1:
		ctrS = parts[0]
	case 2:
		hostS, ctrS = parts[0], parts[1]
	case 3:
		ip, hostS, ctrS = parts[0], parts[1], parts[2]
	default:
		// Unbracketed IPv6 is ambiguous: skip rather than guess.
		return nil
	}
	cLo, cHi, ok := portRange(ctrS)
	if !ok {
		return nil
	}
	var out []desiredPort
	if hostS == "" {
		for c := cLo; c <= cHi; c++ {
			out = append(out, desiredPort{IP: wildcardIP(ip), Container: c, Proto: proto})
		}
		return out
	}
	hLo, hHi, ok := portRange(hostS)
	if !ok || hHi-hLo != cHi-cLo {
		return nil
	}
	for i := 0; i <= cHi-cLo; i++ {
		out = append(out, desiredPort{IP: wildcardIP(ip), Host: hLo + i, Container: cLo + i, Proto: proto})
	}
	return out
}

func parseMount(v any, dir, project string, vols map[string]rawResource) (desiredMount, bool) {
	switch t := v.(type) {
	case string:
		parts := strings.Split(t, ":")
		if len(parts) == 1 {
			return desiredMount{Type: "volume", Target: path.Clean(parts[0])}, true
		}
		m := desiredMount{Target: path.Clean(parts[1])}
		if len(parts) > 2 {
			for _, opt := range strings.Split(parts[2], ",") {
				if opt == "ro" {
					m.ReadOnly = true
				}
			}
		}
		src := parts[0]
		if strings.HasPrefix(src, "/") || strings.HasPrefix(src, ".") || strings.HasPrefix(src, "~") {
			m.Type = "bind"
			m.Source = hostPath(src, dir)
		} else {
			m.Type = "volume"
			m.Source = resourceName(src, project, vols[src])
		}
		return m, true
	case map[string]any:
		typ, _ := t["type"].(string)
		src, _ := t["source"].(string)
		tgt, _ := t["target"].(string)
		if tgt == "" {
			return desiredMount{}, false
		}
		m := desiredMount{Type: typ, Target: path.Clean(tgt)}
		if ro, ok := t["read_only"].(bool); ok {
			m.ReadOnly = ro
		}
		switch typ {
		case "bind":
			m.Source = hostPath(src, dir)
		case "volume":
			if src != "" {
				m.Source = resourceName(src, project, vols[src])
			}
		case "tmpfs":
		default:
			return desiredMount{}, false
		}
		return m, true
	}
	return desiredMount{}, false
}

func hostPath(src, dir string) string {
	if !filepath.IsAbs(src) {
		src = filepath.Join(dir, src)
	}
	return filepath.Clean(src)
}

func secretTarget(v any) string {
	const base = "/run/secrets/"
	switch t := v.(type) {
	case string:
		return base + t
	case map[string]any:
		src, _ := t["source"].(string)
		tgt, _ := t["target"].(string)
		switch {
		case tgt == "":
			tgt = src
		}
		if tgt == "" {
			return ""
		}
		if strings.HasPrefix(tgt, "/") {
			return path.Clean(tgt)
		}
		return base + tgt
	}
	return ""
}

// ---- comparison ----

var imageIDPattern = regexp.MustCompile(`^(sha256:)?[0-9a-f]{12,64}$`)
var anonymousVolume = regexp.MustCompile(`^[0-9a-f]{64}$`)

type imageRef struct{ name, tag, digest string }

func normalizeImage(ref string) imageRef {
	var r imageRef
	if i := strings.Index(ref, "@"); i >= 0 {
		r.digest = ref[i+1:]
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i+1:], "/") {
		r.tag = ref[i+1:]
		ref = ref[:i]
	}
	for _, prefix := range []string{"docker.io/", "index.docker.io/"} {
		ref = strings.TrimPrefix(ref, prefix)
	}
	if strings.HasPrefix(ref, "library/") && strings.Count(ref, "/") == 1 {
		ref = strings.TrimPrefix(ref, "library/")
	}
	r.name = ref
	if r.tag == "" && r.digest == "" {
		r.tag = "latest"
	}
	return r
}

func findImage(images []docker.Image, id string) (docker.Image, bool) {
	id = strings.TrimPrefix(id, "sha256:")
	if id == "" {
		return docker.Image{}, false
	}
	for _, img := range images {
		have := strings.TrimPrefix(img.ID, "sha256:")
		if have == id || strings.HasPrefix(have, id) || (have != "" && strings.HasPrefix(id, have)) {
			return img, true
		}
	}
	return docker.Image{}, false
}

// imageDrift returns a detail when the running image differs from the
// desired reference, or "" when it matches or cannot be judged.
func imageDrift(desired string, c docker.Container, images func() []docker.Image) string {
	if desired == "" {
		return ""
	}
	want := normalizeImage(desired)
	byID := func(id string) (docker.Image, bool) { return findImage(images(), id) }
	matchesImage := func(img docker.Image) bool {
		if want.digest != "" {
			for _, d := range img.RepoDigests {
				if got := normalizeImage(d); got.digest == want.digest {
					return true
				}
			}
			return false
		}
		for _, t := range img.RepoTags {
			if got := normalizeImage(t); got.name == want.name && got.tag == want.tag {
				return true
			}
		}
		return false
	}
	if imageIDPattern.MatchString(c.Image) {
		img, ok := byID(c.Image)
		if !ok {
			return "" // unresolvable ID: not drift
		}
		if matchesImage(img) {
			return ""
		}
		return fmt.Sprintf("running image %s no longer carries %s (the tag moved or the image changed since the container was created)", shortID(c.Image), desired)
	}
	got := normalizeImage(c.Image)
	if want.digest != "" {
		if got.digest != "" {
			if got.digest == want.digest {
				return ""
			}
			return fmt.Sprintf("running %s, compose pins %s", c.Image, desired)
		}
		if img, ok := byID(firstNonEmpty(c.ImageID, c.Image)); ok {
			if matchesImage(img) {
				return ""
			}
			return fmt.Sprintf("running %s, compose pins %s", c.Image, desired)
		}
		return ""
	}
	if got.name == want.name && got.tag == want.tag && got.digest == "" {
		return ""
	}
	return fmt.Sprintf("running %s, compose declares %s", c.Image, desired)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return id
}

func portDrift(want []desiredPort, c docker.Container) []string {
	type key struct {
		public, private int
		proto           string
	}
	actual := map[key]map[string]bool{}
	for _, p := range c.Ports {
		if p.Public == 0 {
			continue // exposed only
		}
		k := key{p.Public, p.Private, strings.ToLower(p.Type)}
		if actual[k] == nil {
			actual[k] = map[string]bool{}
		}
		actual[k][wildcardIP(p.IP)] = true
	}
	var out []string
	covered := map[key]bool{}
	anyHost := map[[2]any]bool{}
	for _, w := range want {
		if w.Host == 0 {
			anyHost[[2]any{w.Container, w.Proto}] = true
			found := false
			for k := range actual {
				if k.private == w.Container && k.proto == w.Proto {
					found, covered[k] = true, true
				}
			}
			if !found {
				out = append(out, fmt.Sprintf("container port %d/%s is not published (compose publishes it on a host-chosen port)", w.Container, w.Proto))
			}
			continue
		}
		k := key{w.Host, w.Container, w.Proto}
		ips, ok := actual[k]
		if !ok {
			out = append(out, fmt.Sprintf("host port %d -> %d/%s is not published", w.Host, w.Container, w.Proto))
			continue
		}
		covered[k] = true
		if w.IP != "" && !ips[w.IP] {
			out = append(out, fmt.Sprintf("host port %d is bound on %s, compose binds %s", w.Host, joinIPs(ips), w.IP))
		}
	}
	var extra []string
	for k := range actual {
		if !covered[k] {
			extra = append(extra, fmt.Sprintf("unexpected published port %d -> %d/%s", k.public, k.private, k.proto))
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func joinIPs(ips map[string]bool) string {
	var out []string
	for ip := range ips {
		if ip == "" {
			ip = "all interfaces"
		}
		out = append(out, ip)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func networkDrift(want []string, c docker.Container) []string {
	have := map[string]bool{}
	for _, n := range c.Networks {
		have[n] = true
	}
	var out []string
	wanted := map[string]bool{}
	for _, n := range want {
		wanted[n] = true
		if !have[n] {
			out = append(out, fmt.Sprintf("not attached to network %s", n))
		}
	}
	for _, n := range c.Networks {
		if !wanted[n] {
			out = append(out, fmt.Sprintf("attached to unexpected network %s", n))
		}
	}
	return out
}

var engineManagedMounts = map[string]bool{"/etc/hosts": true, "/etc/hostname": true, "/etc/resolv.conf": true}

// samePath compares lexically; symlinks resolve only for a local
// Engine, since a remote host's paths mean nothing on this filesystem.
func samePath(a, b string, local bool) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	if !local {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func describeMount(m docker.Mount) string {
	switch {
	case m.Type == "volume" && anonymousVolume.MatchString(m.Name), m.Type == "volume" && m.Name == "":
		return "an anonymous volume"
	case m.Type == "volume":
		return "named volume " + m.Name
	case m.Type == "bind":
		return "a bind of " + m.Source
	}
	return "a " + m.Type + " mount"
}

func mountDrift(want []desiredMount, c docker.Container, local bool) []string {
	actual := map[string]docker.Mount{}
	for _, m := range c.Mounts {
		actual[path.Clean(m.Destination)] = m
	}
	var out []string
	declared := map[string]bool{}
	for _, w := range want {
		declared[w.Target] = true
		got, ok := actual[w.Target]
		if !ok {
			out = append(out, fmt.Sprintf("mount %s is missing (compose declares a %s mount)", w.Target, w.Type))
			continue
		}
		if w.Secret {
			if got.RW {
				out = append(out, fmt.Sprintf("secret %s is mounted writable, compose mounts secrets read-only", w.Target))
			}
			continue
		}
		if got.Type != w.Type {
			out = append(out, fmt.Sprintf("mount %s is %s, compose declares a %s mount", w.Target, describeMount(got), w.Type))
			continue
		}
		switch w.Type {
		case "bind":
			if !samePath(got.Source, w.Source, local) {
				out = append(out, fmt.Sprintf("mount %s binds %s, compose declares %s", w.Target, got.Source, w.Source))
			}
		case "volume":
			if w.Source != "" && got.Name != w.Source {
				out = append(out, fmt.Sprintf("mount %s uses volume %s, compose declares %s", w.Target, got.Name, w.Source))
			}
		}
		if w.Type != "tmpfs" && got.RW == w.ReadOnly {
			if w.ReadOnly {
				out = append(out, fmt.Sprintf("mount %s is writable, compose declares it read-only", w.Target))
			} else {
				out = append(out, fmt.Sprintf("mount %s is read-only, compose declares it writable", w.Target))
			}
		}
	}
	var extra []string
	for dest, m := range actual {
		if declared[dest] || engineManagedMounts[dest] {
			continue
		}
		if m.Type == "volume" && (m.Name == "" || anonymousVolume.MatchString(m.Name)) {
			continue // image VOLUME declaration
		}
		extra = append(extra, fmt.Sprintf("unexpected mount %s (%s)", dest, describeMount(m)))
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func compareLive(desired desiredStack, inv docker.Inventory) ([]LiveFinding, string) {
	findings := []LiveFinding{}
	add := func(service, container, kind, detail, resolution string) {
		f := LiveFinding{Service: service, Container: container, Kind: kind, Detail: detail, Resolution: resolution}
		switch {
		case resolution == ResolveRecreate:
			f.Hint = fmt.Sprintf("docker compose -p %s rm -sf %s, then Restore managed state", desired.Project, service)
		case kind == LiveRestarting:
			f.Hint = fmt.Sprintf("docker compose -p %s logs %s", desired.Project, service)
		case kind == LiveExtra && container != "":
			f.Hint = fmt.Sprintf("if unwanted: docker rm -f %s", container)
		case kind == LiveExtra:
			f.Hint = fmt.Sprintf("docker ps -a --filter label=%s=%s --filter label=%s=%s", docker.LabelProject, desired.Project, docker.LabelService, service)
		}
		findings = append(findings, f)
	}
	// Images list lazily: only ID resolution and digest checks need it.
	var images []docker.Image
	resolved := false
	imageList := func() []docker.Image {
		if !resolved {
			resolved = true
			images = inv.Images
			if images == nil && inv.LoadImages != nil {
				if list, err := inv.LoadImages(); err == nil {
					images = list
				}
			}
		}
		return images
	}
	byService := map[string][]docker.Container{}
	for _, c := range inv.Containers {
		if c.Labels[docker.LabelProject] != desired.Project || c.Labels[docker.LabelOneOff] == "True" {
			continue
		}
		service := c.Labels[docker.LabelService]
		if service == "" {
			continue
		}
		byService[service] = append(byService[service], c)
	}
	expectedServices, activeServices := 0, 0
	names := make([]string, 0, len(desired.Services))
	for name := range desired.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		d := desired.Services[name]
		running := byService[name]
		if !d.Profiled && d.Expected > 0 {
			expectedServices++
			for _, c := range running {
				if c.State == "running" || c.State == "restarting" {
					activeServices++
					break
				}
			}
		}
		if d.Profiled && len(running) == 0 {
			continue
		}
		if len(running) == 0 {
			if d.Expected > 0 {
				add(name, "", LiveMissing, "no container exists for this service", ResolveRestore)
			}
			continue
		}
		if len(running) > d.Expected {
			add(name, "", LiveExtra, fmt.Sprintf("%d containers run for this service, compose expects %d", len(running), d.Expected), ResolveManual)
		}
		for _, c := range running {
			cname := containerName(c)
			switch c.State {
			case "running":
			case "restarting":
				add(name, cname, LiveRestarting, "container is restarting: "+c.Status, ResolveManual)
			default:
				if (d.Restart == "" || d.Restart == "no") && strings.HasPrefix(c.Status, "Exited (0)") {
					break
				}
				add(name, cname, LiveStopped, fmt.Sprintf("container is %s: %s", c.State, c.Status), ResolveRestore)
			}
			if detail := imageDrift(d.Image, c, imageList); detail != "" {
				add(name, cname, LiveImage, detail, ResolveRestore)
			}
			if c.State == "running" && d.NetworkMode == "" && !d.PortsUnsupported {
				for _, detail := range portDrift(d.Ports, c) {
					add(name, cname, LivePort, detail, ResolveRecreate)
				}
			}
			if d.NetworkMode == "" && !d.NetworksUnsupported {
				for _, detail := range networkDrift(d.Networks, c) {
					add(name, cname, LiveNetwork, detail, ResolveRecreate)
				}
			}
			if !d.MountsUnsupported {
				for _, detail := range mountDrift(d.Mounts, c, inv.Local) {
					add(name, cname, LiveMount, detail, ResolveRecreate)
				}
			}
		}
	}
	extraServices := make([]string, 0)
	for service := range byService {
		if _, ok := desired.Services[service]; !ok {
			extraServices = append(extraServices, service)
		}
	}
	sort.Strings(extraServices)
	for _, service := range extraServices {
		for _, c := range byService[service] {
			add(service, containerName(c), LiveExtra, "container belongs to the compose project but no compose service declares it", ResolveManual)
		}
	}
	state := ""
	if expectedServices > 0 && activeServices == 0 {
		// Nothing runs: a stopped stack is a run state (ADR-005), not
		// drift. Homestead persists no desired run state, so a stack
		// stopped by accident reads the same; only partial stacks are
		// drift. Other findings (image, mounts) still stand.
		state = "stopped"
		kept := findings[:0]
		for _, f := range findings {
			if f.Kind != LiveMissing && f.Kind != LiveStopped {
				kept = append(kept, f)
			}
		}
		findings = kept
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Detail < b.Detail
	})
	return findings, state
}

func containerName(c docker.Container) string {
	if len(c.Names) > 0 {
		return c.Names[0]
	}
	return c.ID
}
