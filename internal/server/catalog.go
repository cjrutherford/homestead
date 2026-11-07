package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"

	"homestead/internal/appaccess"
	"homestead/internal/appsso"
	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// MountCatalog serves catalog install and configure (S167): package
// list and detail, install planning, approved installation, and
// configuration — the dashboard install/configure flow end to end.
// Long executions run through the operation engine; progress streams
// on the existing operation endpoints, so these routes return
// operation IDs instead of blocking.
func (s *Server) MountCatalog(st *store.Store, catalogDir string, secrets catalog.SecretStore) {
	s.catalogDir = catalogDir
	setSingleOriginCatalog(catalogDir)
	runner := operation.NewRunner(st)
	s.mux.HandleFunc("GET /api/v1/catalog", handleCatalogList(s, st, catalogDir))
	s.mux.HandleFunc("GET /api/v1/catalog/bundles", handleCatalogBundles(st, catalogDir))
	s.mux.HandleFunc("GET /api/v1/catalog/{name}", handleCatalogGet(catalogDir))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/plan", handleCatalogPlan(s, st, catalogDir))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/access-plan", handleInstallAccessPlan(s, st, catalogDir))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/install", handleCatalogInstall(s, st, catalogDir, runner, secrets, false))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/install-admin", handleCatalogInstall(s, st, catalogDir, runner, secrets, true))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/configure", handleCatalogConfigure(s, st, catalogDir, runner, secrets))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/uninstall", handleCatalogUninstall(s, st, catalogDir, runner))
}

// stackDirFor resolves installed stack directories, stubbed in tests:
// production uses docker.DirFor. Install and configure tolerate the
// production path (their failures surface async); uninstall validates
// synchronously, so it needs the seam.
var stackDirFor = docker.DirFor

// discoverStorage reads live mounts, stubbed in tests.
var discoverStorage = discover.DiscoverStorage

// verifyMounts requires every directory the plan provisions to sit on
// a real, accessible, writable mountpoint (ADR-013): provisioning on a
// path that merely exists on the root disk would silently put app
// data there if the storage were ever unmounted.
func verifyMounts(plan catalog.InstallPlan) error {
	if len(plan.Directories) == 0 {
		return nil
	}
	discovered, err := discoverStorage()
	if err != nil {
		return fmt.Errorf("storage discovery: %w", err)
	}
	byPoint := map[string]discover.Mount{}
	for _, mount := range discovered.Mounts {
		byPoint[mount.Mountpoint] = mount
	}
	checked := map[string]bool{}
	for _, dir := range plan.Directories {
		if checked[dir.Mountpoint] {
			continue
		}
		checked[dir.Mountpoint] = true
		mount, ok := byPoint[filepath.Clean(dir.Mountpoint)]
		switch {
		case !ok:
			return fmt.Errorf("%s is not a mounted filesystem; mount the storage before installing", dir.Mountpoint)
		case !mount.Accessible:
			return fmt.Errorf("mount %s is not accessible", dir.Mountpoint)
		case mount.ReadOnly:
			return fmt.Errorf("mount %s is read-only", dir.Mountpoint)
		}
	}
	return nil
}

// planChecked plans an install and applies the live-state checks
// planning stays pure of: not already installed (409) and storage
// actually mounted (422). Returns the status for the failure.
func planChecked(st *store.Store, req catalog.InstallRequest) (catalog.InstallPlan, int, error) {
	if err := catalog.RefuseInstalled(st, req.Manifest.Name, stackDirFor(req.Manifest.Name), &req.Manifest); err != nil {
		return catalog.InstallPlan{}, http.StatusConflict, err
	}
	plan, err := catalog.PlanInstall(req)
	if err != nil {
		return catalog.InstallPlan{}, http.StatusUnprocessableEntity, err
	}
	// Declared handoff targets the appliance has not installed are an
	// advisory note, never a verdict (S341, ADR-051).
	noteInstalled := func(target string) bool {
		installed, checkErr := catalog.Installed(st, target, stackDirFor(target))
		return checkErr == nil && installed
	}
	plan.Notes = append(plan.Notes, catalog.HandoffNotes(req.Manifest, noteInstalled)...)
	if err := verifyMounts(plan); err != nil {
		return catalog.InstallPlan{}, http.StatusUnprocessableEntity, err
	}
	return plan, 0, nil
}

// packageSummary is the list-view projection: enough to browse,
// details behind the detail route.
type packageSummary struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Title    string `json:"title"`
	Blurb    string `json:"blurb"`
	Category string `json:"category"`
	Icon     string `json:"icon"`
	// Installed lets clients disable Install for packages with a stack.
	Installed bool `json:"installed"`
	// Setup is "running" or "stopped" while the setup approved with the
	// install is unfinished (S324, ADR-048); empty otherwise.
	Setup string `json:"setup,omitempty"`
}

// loadPackage resolves a catalog entry or 404s unknown names: routing
// on unvalidated names would reach filesystems and stack paths.
func loadPackage(catalogDir, name string, w http.ResponseWriter) (catalog.Manifest, bool) {
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("catalog unavailable: %v", err), http.StatusInternalServerError)
		return catalog.Manifest{}, false
	}
	manifest, err := entries.Get(name)
	if err != nil {
		http.Error(w, "unknown package", http.StatusNotFound)
		return catalog.Manifest{}, false
	}
	return manifest, true
}

func handleCatalogList(s *Server, st *store.Store, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setups := s.appSetup.unfinished()
		entries, err := catalog.Load(catalogDir)
		if err != nil {
			http.Error(w, fmt.Sprintf("catalog unavailable: %v", err), http.StatusInternalServerError)
			return
		}
		var out []packageSummary
		for _, name := range entries.Names() {
			manifest, err := entries.Get(name)
			if err != nil {
				continue
			}
			installed, _ := catalog.Installed(st, name, stackDirFor(name))
			out = append(out, packageSummary{
				Installed: installed, Setup: setups[name],
				Name: manifest.Name, Version: manifest.Version, Title: manifest.Title,
				Blurb: manifest.Blurb, Category: manifest.Category, Icon: manifest.Icon,
			})
		}
		if out == nil {
			out = []packageSummary{}
		}
		writeJSON(w, out)
	}
}

// packageDetail is the catalog detail response (S341): the manifest plus
// its integrations resolved against the catalog, so no client re-resolves
// target names or derives levels. A package without declarations carries
// an empty list, never null.
type packageDetail struct {
	catalog.Manifest
	Works []catalog.ResolvedIntegration
}

func handleCatalogGet(catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := catalog.Load(catalogDir)
		if err != nil {
			http.Error(w, fmt.Sprintf("catalog unavailable: %v", err), http.StatusInternalServerError)
			return
		}
		manifest, err := entries.Get(r.PathValue("name"))
		if err != nil {
			http.Error(w, "unknown package", http.StatusNotFound)
			return
		}
		writeJSON(w, packageDetail{Manifest: manifest, Works: entries.IntegrationsFor(manifest)})
	}
}

// installBody carries owner inputs; host facts assemble server-side
// from live discovery, never trusting client-reported capacity.
type installBody struct {
	Domain         string                      `json:"domain"`
	Hostnames      map[string]string           `json:"hostnames"`
	ContainerPorts map[string]int              `json:"container_ports"`
	Storage        map[storage.Class]string    `json:"storage"`
	Volumes        []catalog.VolumeSpec        `json:"volumes"`
	Networks       []catalog.NetworkAttachment `json:"networks"`
	Zone           string                      `json:"zone"`
	PublicIP       string                      `json:"public_ip"`
	// PublicName is the public address the owner reviews with the
	// install (S313). The install itself ignores it; the plan's sign-in
	// preview counts it. Managed installs set up sign-in before publishing.
	PublicName string `json:"public_name,omitempty"`
	// Digest, on install, is the reviewed install plan's digest (S313):
	// a plan that changed since answers 409. Empty installs as planned
	// now, as before.
	Digest string `json:"digest,omitempty"`
	// Setup, on install, carries the steps the owner approved with it
	// (S324, ADR-048): the daemon runs them after the install succeeds.
	// Absent installs the app alone, as before.
	Setup   *setupApproval     `json:"setup,omitempty"`
	Choices []appaccess.Choice `json:"choices,omitempty"`
}

// installRequest assembles the planner input: manifest from the
// catalog, facts from the host, everything else from the owner.
func installRequest(st *store.Store, catalogDir, name string, body installBody) (catalog.InstallRequest, error) {
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return catalog.InstallRequest{}, fmt.Errorf("catalog unavailable: %w", err)
	}
	manifest, err := entries.Get(name)
	if err != nil {
		return catalog.InstallRequest{}, fmt.Errorf("unknown package")
	}
	facts, err := hostFacts(st)
	if err != nil {
		return catalog.InstallRequest{}, err
	}
	// Class mounts come from the owner's storage assignments (S276):
	// catalog data declarations resolve against them, so a request
	// body only adds or overrides entries, never has to repeat them.
	mountpoints := map[storage.Class]string{}
	assigned, err := storage.NewAssignments(st).List()
	if err != nil {
		return catalog.InstallRequest{}, fmt.Errorf("assignments: %w", err)
	}
	for _, assignment := range assigned {
		mountpoints[assignment.Class] = assignment.Mountpoint
	}
	for class, mountpoint := range body.Storage {
		mountpoints[class] = mountpoint
	}
	if len(body.Storage) > 0 {
		// A mount chosen in the request has no assignment, so its free
		// space is read from live discovery instead of failing the floor
		// check with "no space data".
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			return catalog.InstallRequest{}, fmt.Errorf("storage discovery: %w", err)
		}
		applyStorageOverrides(&facts, body.Storage, discovered.Mounts)
	}
	var publicIP netip.Addr
	if body.PublicIP != "" {
		publicIP, err = netip.ParseAddr(body.PublicIP)
		if err != nil {
			return catalog.InstallRequest{}, fmt.Errorf("public address: %w", err)
		}
	}
	// An app published as part of the install renders its public address
	// into the settings that take a single base URL (S407).
	publicHostname := strings.ToLower(strings.TrimSpace(body.PublicName))
	if publicHostname == "" && body.Setup != nil && body.Setup.Public != nil {
		publicHostname = strings.ToLower(strings.TrimSpace(body.Setup.Public.Hostname))
	}
	return catalog.InstallRequest{
		PublicHostname: publicHostname,
		Manifest:       manifest, Host: facts, Domain: body.Domain,
		Hostnames: body.Hostnames, ContainerPorts: body.ContainerPorts,
		Storage: mountpoints, Volumes: body.Volumes, Networks: body.Networks,
		Zone: body.Zone, PublicIP: publicIP, ApplianceNames: applianceNames(st),
	}, nil
}

// applianceNames are the dashboard route's local names (ADR-044): the
// names a package served by path answers under. Nil before the
// dashboard route exists, which means the defaults.
func applianceNames(st *store.Store) []string {
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return nil
	}
	dash, ok := set.DashboardRoute()
	if !ok {
		return nil
	}
	var out []string
	for _, n := range dash.Names() {
		out = append(out, string(n))
	}
	return out

}

// hostFactsFor is the bundle grading seam: tests stub the facts, the
// daemon grades against live host state.
var hostFactsFor = hostFacts

// handleCatalogBundles grades the curated profiles against the current
// host (S344): combined memory and per-class storage, plus the curated
// members the catalog does not carry yet.
func handleCatalogBundles(st *store.Store, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := catalog.Load(catalogDir)
		if err != nil {
			http.Error(w, fmt.Sprintf("catalog unavailable: %v", err), http.StatusInternalServerError)
			return
		}
		facts, err := hostFactsFor(st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out := []catalog.BundleReport{}
		for _, bundle := range catalog.Bundles {
			report, err := catalog.CheckBundle(entries, bundle, facts)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			out = append(out, report)
		}
		writeJSON(w, out)
	}
}

// hostFacts assembles evaluation facts from live host state: platform
// identity, present GPUs (best-effort — absent discovery reads as no
// GPUs rather than failing the plan), free bytes per assigned class,
// and bound host ports. Discovery failures on required facts fail the
// request instead of planning blind.
func hostFacts(st *store.Store) (catalog.HostFacts, error) {
	osFacts, err := discover.DiscoverOS()
	if err != nil {
		return catalog.HostFacts{}, fmt.Errorf("host discovery: %w", err)
	}
	facts := catalog.HostFacts{
		Arch: osFacts.Arch, MemoryMB: osFacts.MemoryBytes / (1024 * 1024),
		CPUCount: osFacts.CPUCount, FreeSpace: map[storage.Class]uint64{},
		UsedPorts: map[int]bool{},
	}
	if hardware, err := discover.DiscoverHardware(); err == nil {
		for _, gpu := range hardware.GPUs {
			label := gpu.Kind
			if gpu.Driver != "" {
				label += ":" + gpu.Driver
			}
			facts.GPUs = append(facts.GPUs, label)
		}
	}
	assignments, err := storage.NewAssignments(st).List()
	if err != nil {
		return catalog.HostFacts{}, fmt.Errorf("assignments: %w", err)
	}
	if len(assignments) > 0 {
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			return catalog.HostFacts{}, fmt.Errorf("storage discovery: %w", err)
		}
		byPoint := map[string]discover.Mount{}
		for _, mount := range discovered.Mounts {
			byPoint[mount.Mountpoint] = mount
		}
		for _, assignment := range assignments {
			if mount, ok := byPoint[assignment.Mountpoint]; ok && mount.Accessible {
				facts.FreeSpace[assignment.Class] = mount.FreeBytes
			}
		}
	}
	for port := range router.ListeningPorts() {
		facts.UsedPorts[int(port)] = true
	}
	return facts, nil
}

// applyStorageOverrides records the free space of each mount the request
// chose for a class, from discovered mounts. An unknown or inaccessible
// mount adds nothing, so the requirement check still fails for it.
func applyStorageOverrides(facts *catalog.HostFacts, chosen map[storage.Class]string, mounts []discover.Mount) {
	byPoint := map[string]discover.Mount{}
	for _, mount := range mounts {
		byPoint[mount.Mountpoint] = mount
	}
	if facts.FreeSpace == nil {
		facts.FreeSpace = map[storage.Class]uint64{}
	}
	for class, mountpoint := range chosen {
		if mount, ok := byPoint[mountpoint]; ok && mount.Accessible {
			facts.FreeSpace[class] = mount.FreeBytes
		} else {
			delete(facts.FreeSpace, class)
		}
	}
}

// maxJSONBody bounds every JSON request decoded through decodeBody.
const maxJSONBody = 128 << 10

func decodeBody(r *http.Request, body any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

// installPlanView is the install plan with its sign-in preview (S298).
type installPlanView struct {
	catalog.InstallPlan
	SignIn *signInPreview     `json:"sign_in,omitempty"`
	Access *installAccessView `json:"access,omitempty"`
	// Path is where a package served by path answers (ADR-044), and
	// Addresses are the local addresses the install gives the app: the
	// path under each of the appliance's names, or the app's own names.
	Path      string   `json:"path,omitempty"`
	Addresses []string `json:"addresses"`
	Digest    string   `json:"digest"`
	// FilesLink is the Nextcloud link for craft files the install's
	// setup makes (S326), approved with setup.files_link.
	FilesLink *filesLinkPlan `json:"files_link,omitempty"`
}

// installAddresses are the local addresses an install plan gives the app.
func installAddresses(req catalog.InstallRequest, plan catalog.InstallPlan) []string {
	out := []string{}
	if req.Manifest.ServedByPath() {
		names := req.ApplianceNames
		if len(names) == 0 {
			names = catalog.DefaultApplianceNames
		}
		for _, n := range names {
			out = append(out, docker.PathURL(docker.Hostname(n), req.Manifest.AppPath()))
		}
		return out
	}
	for _, r := range plan.Routes {
		if r.Exposure == docker.ExposurePublic {
			continue
		}
		for _, n := range r.Names() {
			out = append(out, "https://"+string(n)+"/")
		}
	}
	return out
}

// installDigest binds an install approval to what it changes (S313):
// routes, generated files, DNS, certificates, firewall, folders, mounts
// and actions. The requirement report and notes describe the host as it
// is now (free space moves) and are left out. Each list is hashed in a
// sorted order, so map order in the planner never changes it.
func installDigest(plan catalog.InstallPlan) string {
	h := sha256.New()
	part := func(name string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(h, "%s=%s\n", name, data)
	}
	sorted := func(name string, items []any) {
		lines := make([]string, 0, len(items))
		for _, item := range items {
			data, _ := json.Marshal(item)
			lines = append(lines, string(data))
		}
		sort.Strings(lines)
		part(name, lines)
	}
	part("package", plan.Package)
	part("compose", plan.Compose)
	part("edge-network", plan.EdgeNetwork)
	sorted("routes", anySlice(plan.Routes))
	sorted("dns", anySlice(plan.DNS))
	sorted("tls", anySlice(plan.TLS))
	sorted("firewall", anySlice(plan.Firewall))
	sorted("directories", anySlice(plan.Directories))
	sorted("mounts", anySlice(plan.Mounts))
	sorted("actions", anySlice(plan.Actions))
	return hex.EncodeToString(h.Sum(nil))
}

func anySlice[T any](in []T) []any {
	out := make([]any, len(in))
	for i := range in {
		out[i] = in[i]
	}
	return out
}

func handleCatalogPlan(s *Server, st *store.Store, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body installBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req, err := installRequest(st, catalogDir, r.PathValue("name"), body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		plan, status, err := planChecked(st, req)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		view := installPlanView{InstallPlan: plan, Digest: installDigest(plan),
			Path: req.Manifest.AppPath(), Addresses: installAddresses(req, plan)}
		if s.appSignIn != nil {
			view.SignIn = s.appSignIn.preview(req.Manifest, plan, strings.ToLower(strings.TrimSpace(body.PublicName)))
		}
		access, _, err := s.installAccess(r.Context(), st, req.Manifest, plan, body.PublicName)
		if err != nil {
			http.Error(w, "cannot read app access state", http.StatusInternalServerError)
			return
		}
		view.Access = &access
		if s.appSetup != nil {
			view.FilesLink = s.appSetup.filesLinkPreview(req.Manifest)
		}
		writeJSON(w, view)
	}
}

func handleCatalogInstall(s *Server, st *store.Store, catalogDir string, runner *operation.Runner, secrets catalog.SecretStore, adminAuthorized bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body installBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req, err := installRequest(st, catalogDir, r.PathValue("name"), body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if refusal := req.Manifest.PublicRefusal(); refusal != "" &&
			(strings.TrimSpace(body.PublicName) != "" || (body.Setup != nil && body.Setup.Public != nil)) {
			http.Error(w, refusal, http.StatusUnprocessableEntity)
			return
		}
		plan, status, err := planChecked(st, req)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		if body.Digest != "" && body.Digest != installDigest(plan) {
			http.Error(w, "the install plan changed since it was reviewed; review it again", http.StatusConflict)
			return
		}
		access, integration, err := s.installAccess(r.Context(), st, req.Manifest, plan, body.PublicName)
		if err != nil {
			http.Error(w, "cannot read app access state", http.StatusInternalServerError)
			return
		}
		if access.Mode == "unavailable" {
			http.Error(w, access.Reason, http.StatusUnprocessableEntity)
			return
		}
		if access.Mode == "self-managed" {
			// The app keeps its own accounts, so no per-person claim can be
			// honored; refuse any, and bind the reviewed mode if one is sent.
			if len(body.Choices) != 0 || (body.Setup != nil && len(body.Setup.People) != 0) {
				http.Error(w, "this app keeps its own accounts; Homestead cannot apply per-person access choices", http.StatusUnprocessableEntity)
				return
			}
			if body.Setup != nil && body.Setup.Access != nil {
				if len(body.Setup.Access.Choices) != 0 {
					http.Error(w, "this app keeps its own accounts; Homestead cannot apply per-person access choices", http.StatusUnprocessableEntity)
					return
				}
				if err := appaccess.Verify(body.Setup.Access.Digest, req.Manifest.Name, nil, nil, false, integration); err != nil {
					http.Error(w, err.Error(), http.StatusConflict)
					return
				}
				// Verified; nothing is managed, so no access step is recorded.
				body.Setup.Access = nil
			}
		}
		if body.Setup != nil && body.Setup.Access != nil {
			if err := validateInstallAdmin(body.Setup.Access.Choices, adminAuthorized); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			if access.Mode != "managed" {
				http.Error(w, "this app does not accept managed access decisions", http.StatusBadRequest)
				return
			}
		}
		if access.Mode == "managed" {
			if s.appSetup == nil {
				http.Error(w, "app access setup is unavailable", http.StatusServiceUnavailable)
				return
			}
			if body.Setup == nil || body.Setup.Access == nil || body.Setup.SignIn == nil {
				http.Error(w, "review app access and Authentik sign-in before installing", http.StatusBadRequest)
				return
			}
			a := body.Setup.Access
			si := s.appSignIn.preview(req.Manifest, plan, strings.ToLower(strings.TrimSpace(body.PublicName)))
			if si == nil || si.Plan == nil || body.Setup.SignIn.Digest != si.Plan.Digest {
				http.Error(w, "the Authentik sign-in plan changed; review it again", http.StatusConflict)
				return
			}
			if err := appaccess.Verify(a.Digest, req.Manifest.Name, accessPeople(access), a.Choices, access.AdminSupported, integration); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			if body.Digest == "" {
				http.Error(w, "review the install plan before installing", http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(body.PublicName) != "" && body.Setup.Public == nil {
				http.Error(w, "review the public address plan before installing", http.StatusBadRequest)
				return
			}
		}
		if body.Setup != nil && body.Setup.Public != nil {
			if s.appPublic == nil {
				http.Error(w, "public addresses are unavailable", http.StatusServiceUnavailable)
				return
			}
			pub := body.Setup.Public
			if access.Mode == "managed" && strings.ToLower(strings.TrimSpace(body.PublicName)) != strings.ToLower(strings.TrimSpace(pub.Hostname)) {
				http.Error(w, "the public name and sign-in review must match", http.StatusConflict)
				return
			}
			on := true
			request := appPublicRequest{Enabled: &on, BeforeInstall: true, Hostname: pub.Hostname, Route: pub.Route}
			build := s.appPublic.buildPlan
			if access.Mode == "managed" {
				build = s.appPublic.buildAnticipatedSignInPlan
			}
			publicPlan, err := build(r.Context(), req.Manifest.Name, request)
			if err != nil {
				s.appPublic.writeError(w, err)
				return
			}
			if publicPlan.Digest != pub.Digest || publicPlan.Hostname != strings.ToLower(strings.TrimSpace(pub.Hostname)) || publicPlan.Route != strings.ToLower(strings.TrimSpace(pub.Route)) {
				http.Error(w, "the public address plan changed; review it again", http.StatusConflict)
				return
			}
		}
		def, err := catalog.InstallDefinition(plan, storage.NewRegistry(st), "", st, secrets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if req.Manifest.FirstRun != nil {
			if req.Manifest.FirstRun.Routine == catalog.FirstRunKomga {
				def.Steps = append([]operation.StepDef{freshKomgaDataStep(plan)}, def.Steps...)
			}
			owner := func(ctx context.Context) string { return s.appSignIn.firstRunOwner(ctx, req.Manifest) }
			def.Steps = append(def.Steps, firstRunStep(req.Manifest, secrets, owner))
		}
		setup := s.appSetup
		if setup != nil {
			setup.forgetFinished(req.Manifest.Name)
		}
		withSetup := body.Setup != nil && setup != nil
		if withSetup {
			rec, err := setup.prepare(req.Manifest, plan, *body.Setup, operatorActor(r))
			if err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, errPlanChanged) {
					status = http.StatusConflict
				}
				http.Error(w, err.Error(), status)
				return
			}
			if err := setup.attach(rec, &def); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			if withSetup {
				setup.forget(req.Manifest.Name)
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if withSetup {
			setup.installed(req.Manifest.Name, op.ID)
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

func handleCatalogConfigure(s *Server, st *store.Store, catalogDir string, runner *operation.Runner, secrets catalog.SecretStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		manifest, ok := loadPackage(catalogDir, name, w)
		if !ok {
			return
		}
		var body struct {
			Changes []catalog.ConfigChange `json:"changes"`
			// RotateSecrets names declared secrets to replace with
			// fresh values (S266); responses never carry values.
			RotateSecrets []string `json:"rotate_secrets"`
			// AdoptSecrets regenerates only the secret wiring of a
			// stack installed before S266 (never data or passwords).
			AdoptSecrets bool `json:"adopt_secrets"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		plan := catalog.ConfigPlan{Manifest: manifest, Changes: body.Changes, Rotate: body.RotateSecrets, Adopt: body.AdoptSecrets}
		if err := plan.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		def, err := catalog.ConfigureDefinition(plan, docker.DirFor(name), name, st, secrets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(plan.Rotate) > 0 || plan.Adopt {
			action, detail := "app.secret.rotate", fmt.Sprintf("operation %d: %s rotating %v", op.ID, name, plan.Rotate)
			if plan.Adopt {
				action, detail = "app.secret.adopt", fmt.Sprintf("operation %d: %s adopting secret wiring", op.ID, name)
			}
			// Names only; secret values are never logged or audited.
			s.auditOperation(st, runner, op.ID, operatorActor(r), action, detail)
		}
		resp := map[string]any{"id": op.ID}
		if warnings := rotateSignInWarnings(st, manifest, plan.Rotate); len(warnings) > 0 {
			resp["warnings"] = warnings
		}
		writeJSON(w, resp)
	}
}

// rotateSignInWarnings says when a rotated secret is the client secret of
// an applied env-oidc sign-in: Authentik keeps the old value until
// sign-in is applied again (S404; re-sync deferred).
func rotateSignInWarnings(st *store.Store, m catalog.Manifest, rotate []string) []string {
	if m.SSO.OIDC == nil || m.SSO.OIDC.Env == nil {
		return nil
	}
	for _, name := range rotate {
		if name != m.SSO.OIDC.Env.Secret {
			continue
		}
		if _, ok, err := (appsso.Records{DB: st.DB()}).Get(m.Name); err == nil && ok {
			return []string{fmt.Sprintf("%s's sign-in keeps the old secret at Authentik until sign-in is applied again.", m.Title)}
		}
	}
	return nil
}

func handleCatalogUninstall(s *Server, st *store.Store, catalogDir string, runner *operation.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		manifest, ok := loadPackage(catalogDir, name, w)
		if !ok {
			return
		}
		var body struct {
			RemoveData bool `json:"remove_data"`
			// LeaveSignIn acknowledges leaving the app's provider and
			// application in Authentik (S299, audited).
			LeaveSignIn bool `json:"leave_sign_in"`
			// AbandonVolumes acknowledges uninstalling an app whose
			// data still lives in Docker volumes (audited).
			AbandonVolumes bool `json:"abandon_volumes"`
			DataDirs       []struct {
				Mountpoint string `json:"mountpoint"`
				Path       string `json:"path"`
			} `json:"data_dirs"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !catalog.StackInstalled(stackDirFor(name)) {
			_ = catalog.RemoveEmptyStackDir(stackDirFor(name))
			http.Error(w, fmt.Sprintf("%q is not installed", name), http.StatusNotFound)
			return
		}
		if status, kind, refusal, err := uninstallLeftovers(st, manifest, body.LeaveSignIn); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if status != 0 {
			w.Header().Set("X-Homestead-Refusal", kind)
			http.Error(w, refusal, status)
			return
		}
		if status, err := uninstallDataStatus(r.Context(), st, catalogDir, name); err != nil && !body.AbandonVolumes {
			w.Header().Set("X-Homestead-Refusal", refuseVolumes)
			http.Error(w, "cannot confirm where this app's data lives: "+err.Error()+"; pass abandon_volumes to proceed anyway", http.StatusBadGateway)
			return
		} else if err == nil && status == catalog.DataNeedsMigration {
			if !body.AbandonVolumes {
				w.Header().Set("X-Homestead-Refusal", refuseVolumes)
				http.Error(w, fmt.Sprintf("%q keeps data in Docker volumes outside mounted storage; uninstalling would orphan it. Run `homesteadctl data plan %s` and migrate first, or pass abandon_volumes to acknowledge leaving the data behind", name, name), http.StatusConflict)
				return
			}
			_, _ = audit.New(st).Record(audit.Entry{
				Actor: operatorActor(r), Action: "app.uninstall.abandon-volumes", Outcome: audit.OutcomeOK,
				Detail: fmt.Sprintf("%s uninstalled with data left in Docker volumes (acknowledged)", name),
			})
		}
		removal := catalog.Removal{Package: name, RemoveData: body.RemoveData}
		for _, dir := range body.DataDirs {
			removal.DataDirs = append(removal.DataDirs, catalog.DataDir{Mountpoint: dir.Mountpoint, Path: dir.Path})
		}
		def, err := catalog.RemoveDefinition(removal, stackDirFor(name), st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if body.LeaveSignIn {
			def.Steps = append(def.Steps, s.forgetSignInStep(st, name, operatorActor(r)))
		}
		// People's grants for an app that is gone mean nothing, and a left-over
		// grant makes the next install of it refuse the access choice (S444).
		def.Steps = append(def.Steps, operation.StepDef{Name: "forget people's access to the app", Run: func(context.Context) error {
			_, err := st.DB().Exec(`DELETE FROM people_grants WHERE app = ?`, name)
			return err
		}})
		if setup := s.appSetup; setup != nil {
			def.Steps = append(def.Steps, operation.StepDef{Name: "forget the app's setup", Run: func(context.Context) error {
				setup.forgetFinished(name)
				return nil
			}})
		}
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

// operatorActor names the audit actor: the session user, or "local" on
// the root-only socket.
func operatorActor(r *http.Request) string {
	if op, ok := operatorFromContext(r.Context()); ok {
		return op.Session.Username
	}
	return "local"
}

// auditOperation records the start of a secret operation and, once it
// finishes, its outcome (a failed rotation or adoption is an audit
// error). Entries carry names and operation ids, never values.
func (s *Server) auditOperation(st *store.Store, runner *operation.Runner, id int64, actor, action, detail string) {
	svc := audit.New(st)
	_, _ = svc.Record(audit.Entry{Actor: actor, Action: action + ".start", Outcome: audit.OutcomeOK, Detail: detail})
	go func() {
		done, err := runner.Wait(s.bg, id)
		if err != nil {
			return
		}
		entry := audit.Entry{Actor: actor, Action: action, Outcome: audit.OutcomeOK, Detail: fmt.Sprintf("operation %d succeeded", id)}
		if done.Status != operation.StatusSucceeded {
			entry.Outcome = audit.OutcomeError
			entry.Detail = fmt.Sprintf("operation %d %s: %s", id, done.Status, done.Error)
		}
		if _, err := svc.Record(entry); err != nil {
			s.log.Error("secret operation audit failed", "action", action, "error", err)
		}
	}()
}

// uninstallDataStatus reports the S277 placement verdict for one
// package, so uninstall can refuse to orphan volume-held data.
func uninstallDataStatus(ctx context.Context, st *store.Store, catalogDir, name string) (string, error) {
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return "", err
	}
	manifest, err := entries.Get(name)
	if err != nil {
		return "", err
	}
	assignments, err := storage.NewAssignments(st).List()
	if err != nil {
		return "", fmt.Errorf("assignments: %w", err)
	}
	containers, err := migrationContainers(ctx)
	if err != nil {
		return "", fmt.Errorf("engine unavailable: %w", err)
	}
	return catalog.DetectData(manifest, catalog.ProjectContainers(containers, name), assignedMountpoints(assignments)).State, nil
}
