package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"

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
func (s *Server) MountCatalog(st *store.Store, catalogDir string) {
	runner := operation.NewRunner(st)
	s.mux.HandleFunc("GET /api/v1/catalog", handleCatalogList(catalogDir))
	s.mux.HandleFunc("GET /api/v1/catalog/{name}", handleCatalogGet(catalogDir))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/plan", handleCatalogPlan(st, catalogDir))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/install", handleCatalogInstall(st, catalogDir, runner))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/configure", handleCatalogConfigure(st, catalogDir, runner))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/uninstall", handleCatalogUninstall(st, catalogDir, runner))
}

// stackDirFor resolves installed stack directories, stubbed in tests:
// production uses docker.DirFor. Install and configure tolerate the
// production path (their failures surface async); uninstall validates
// synchronously, so it needs the seam.
var stackDirFor = docker.DirFor

// packageSummary is the list-view projection: enough to browse,
// details behind the detail route.
type packageSummary struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Title    string `json:"title"`
	Blurb    string `json:"blurb"`
	Category string `json:"category"`
	Icon     string `json:"icon"`
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

func handleCatalogList(catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			out = append(out, packageSummary{
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

func handleCatalogGet(catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		manifest, ok := loadPackage(catalogDir, r.PathValue("name"), w)
		if !ok {
			return
		}
		writeJSON(w, manifest)
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
	var publicIP netip.Addr
	if body.PublicIP != "" {
		publicIP, err = netip.ParseAddr(body.PublicIP)
		if err != nil {
			return catalog.InstallRequest{}, fmt.Errorf("public address: %w", err)
		}
	}
	return catalog.InstallRequest{
		Manifest: manifest, Host: facts, Domain: body.Domain,
		Hostnames: body.Hostnames, ContainerPorts: body.ContainerPorts,
		Storage: body.Storage, Volumes: body.Volumes, Networks: body.Networks,
		Zone: body.Zone, PublicIP: publicIP,
	}, nil
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

func decodeBody(r *http.Request, body any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

func handleCatalogPlan(st *store.Store, catalogDir string) http.HandlerFunc {
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
		plan, err := catalog.PlanInstall(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, plan)
	}
}

func handleCatalogInstall(st *store.Store, catalogDir string, runner *operation.Runner) http.HandlerFunc {
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
		plan, err := catalog.PlanInstall(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		def, err := catalog.InstallDefinition(plan, storage.NewRegistry(st), "", st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

func handleCatalogConfigure(st *store.Store, catalogDir string, runner *operation.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		manifest, ok := loadPackage(catalogDir, name, w)
		if !ok {
			return
		}
		var body struct {
			Changes []catalog.ConfigChange `json:"changes"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		plan := catalog.ConfigPlan{Manifest: manifest, Changes: body.Changes}
		if err := plan.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		def, err := catalog.ConfigureDefinition(plan, docker.DirFor(name), name, st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
}

func handleCatalogUninstall(st *store.Store, catalogDir string, runner *operation.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, ok := loadPackage(catalogDir, name, w); !ok {
			return
		}
		var body struct {
			RemoveData bool `json:"remove_data"`
			DataDirs   []struct {
				Mountpoint string `json:"mountpoint"`
				Path       string `json:"path"`
			} `json:"data_dirs"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
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
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
}
