package catalog

import (
	"context"
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
}

// LiveDrift reads the stack's compose files and compares them with the
// Engine inventory from src. Unreachable engines report Checked false.
func LiveDrift(ctx context.Context, src LiveSource, stack, dir string) (LiveReview, error) {
	inv, err := src.Inventory(ctx)
	if err != nil {
		return LiveReview{}, fmt.Errorf("catalog: read live state: %w", err)
	}
	return LiveDriftInventory(stack, dir, inv)
}

// LiveDriftInventory compares one stack with an inventory already read,
// so scans query the Engine once for every package.
func LiveDriftInventory(stack, dir string, inv docker.Inventory) (LiveReview, error) {
	desired, err := loadDesired(stack, dir)
	if err != nil {
		return LiveReview{}, err
	}
	if !inv.Reachable {
		return LiveReview{Findings: []LiveFinding{}}, nil
	}
	return LiveReview{Checked: true, Findings: compareLive(desired, inv)}, nil
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
}

type desiredStack struct {
	Project  string
	Services map[string]*desiredService
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
		return doc, false, fmt.Errorf("catalog: parse %s: %w", filepath.Base(file), err)
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
	for name, svc := range base.Services {
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
		for _, p := range svc.Ports {
			d.Ports = append(d.Ports, parsePort(p)...)
		}
		for _, v := range svc.Volumes {
			if m, ok := parseMount(v, dir, project, base.Volumes); ok {
				d.Mounts = append(d.Mounts, m)
			}
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
func imageDrift(desired string, c docker.Container, images []docker.Image) string {
	if desired == "" {
		return ""
	}
	want := normalizeImage(desired)
	byID := func(id string) (docker.Image, bool) { return findImage(images, id) }
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

func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
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

func mountDrift(want []desiredMount, c docker.Container) []string {
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
			if !samePath(got.Source, w.Source) {
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

func compareLive(desired desiredStack, inv docker.Inventory) []LiveFinding {
	findings := []LiveFinding{}
	add := func(service, container, kind, detail, resolution string) {
		findings = append(findings, LiveFinding{Service: service, Container: container, Kind: kind, Detail: detail, Resolution: resolution})
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
	for name, d := range desired.Services {
		running := byService[name]
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
			if detail := imageDrift(d.Image, c, inv.Images); detail != "" {
				add(name, cname, LiveImage, detail, ResolveRestore)
			}
			if c.State == "running" && d.NetworkMode == "" {
				for _, detail := range portDrift(d.Ports, c) {
					add(name, cname, LivePort, detail, ResolveRecreate)
				}
			}
			if d.NetworkMode == "" {
				for _, detail := range networkDrift(d.Networks, c) {
					add(name, cname, LiveNetwork, detail, ResolveRecreate)
				}
			}
			for _, detail := range mountDrift(d.Mounts, c) {
				add(name, cname, LiveMount, detail, ResolveRecreate)
			}
		}
	}
	for service, cs := range byService {
		if _, ok := desired.Services[service]; ok {
			continue
		}
		for _, c := range cs {
			add(service, containerName(c), LiveExtra, "container belongs to the compose project but no compose service declares it", ResolveManual)
		}
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
	return findings
}

func containerName(c docker.Container) string {
	if len(c.Names) > 0 {
		return c.Names[0]
	}
	return c.ID
}
