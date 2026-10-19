package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// MountStorage serves storage setup (S235): discovered mounts,
// mount registration, and class assignment — the surfaces installs
// need before planning. Reads are live discovery; writes validate
// against it, so phantom paths refuse instead of recording.
//
// catalogDir (optional) lets the in-use checks also use what the database
// knows: which installed apps require which storage class (S288).
func (s *Server) MountStorage(st *store.Store, catalogDir ...string) {
	dir := ""
	if len(catalogDir) > 0 {
		dir = catalogDir[0]
	}
	s.mux.HandleFunc("GET /api/v1/storage/mounts", handleStorageMounts())
	s.mux.HandleFunc("GET /api/v1/storage/registrations", handleStorageRegistrations(st))
	s.mux.HandleFunc("POST /api/v1/storage/registrations", handleStorageRegister(st))
	s.mux.HandleFunc("GET /api/v1/storage/assignments", handleStorageAssignments(st))
	s.mux.HandleFunc("POST /api/v1/storage/assignments", handleStorageAssign(st, audit.New(st), dir))
	s.mux.HandleFunc("DELETE /api/v1/storage/assignments/{class}", handleStorageUnassign(st, audit.New(st), dir))
	s.mux.HandleFunc("DELETE /api/v1/storage/registrations", handleStorageUnregister(st, audit.New(st)))
}

// storageInUse is the 409 body for unassign and unregister: the apps (and
// classes) that still use what the owner asked to drop (S288).
type storageInUse struct {
	Error            string   `json:"error"`
	Mountpoint       string   `json:"mountpoint"`
	AffectedPackages []string `json:"affected_packages"`
	Classes          []string `json:"classes,omitempty"`
}

func writeStorageInUse(w http.ResponseWriter, body storageInUse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(body)
}

// diskUnreadableError means the in-use check could not look at the disk:
// a detached or unmounted disk must refuse, never pass (S288).
type diskUnreadableError struct{ mount string }

func (e *diskUnreadableError) Error() string {
	return fmt.Sprintf("the disk at %s is not readable; reattach it before changing storage", e.mount)
}

// requireReadable fails when mount cannot be read as a directory.
func requireReadable(mount string) error {
	if info, err := os.Stat(mount); err != nil || !info.IsDir() {
		return &diskUnreadableError{mount: mount}
	}
	return nil
}

// dirOnMount reports whether `<mount>/<name>` exists. Only a clean
// not-exist means no; any other stat error means "cannot tell".
func dirOnMount(mount, name string) (bool, error) {
	info, err := os.Stat(filepath.Join(mount, name))
	switch {
	case err == nil:
		return info.IsDir(), nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, &diskUnreadableError{mount: mount}
	}
}

// writeStorageCheckError answers a failed in-use check: an unreadable
// disk is a 409 with the reattach message, anything else a 500.
func writeStorageCheckError(w http.ResponseWriter, err error) {
	var unreadable *diskUnreadableError
	if errors.As(err, &unreadable) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// packagesOnMount lists installed packages holding a `<mount>/<pkg>`
// directory: the same evidence the reassign impact check uses.
func packagesOnMount(st *store.Store, mount string) ([]string, error) {
	if err := requireReadable(mount); err != nil {
		return nil, err
	}
	stacks, err := catalog.InstalledStacks(st)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range stacks {
		found, err := dirOnMount(mount, name)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, name)
		}
	}
	return out, nil
}

// appsRequiringClass lists installed apps whose manifest requires class:
// database evidence that survives an unreadable or empty mount.
func appsRequiringClass(st *store.Store, catalogDir string, class storage.Class) ([]string, error) {
	if catalogDir == "" {
		return nil, nil
	}
	stacks, err := catalog.InstalledStacks(st)
	if err != nil {
		return nil, err
	}
	if len(stacks) == 0 {
		return nil, nil
	}
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable: %w", err)
	}
	var out []string
	for _, name := range stacks {
		manifest, err := entries.Get(name)
		if err != nil {
			continue
		}
		for _, need := range manifest.Requirements.Storage {
			if need.Class == class {
				out = append(out, name)
				break
			}
		}
	}
	return out, nil
}

// affectedOnMount is the union of apps with data under mount and apps the
// database says use class. Backups are exempt: their package directories
// hold backup runs, not application data.
func affectedOnMount(st *store.Store, catalogDir string, class storage.Class, mount string) ([]string, error) {
	if class == storage.ClassBackup {
		return nil, nil
	}
	onDisk, err := packagesOnMount(st, mount)
	if err != nil {
		return nil, err
	}
	declared, err := appsRequiringClass(st, catalogDir, class)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, name := range append(onDisk, declared...) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// handleStorageUnassign drops a class binding. It refuses (409) while any
// installed app keeps data under the class's assignment, even when another
// class shares the mount (that exemption is for reassign only), and when
// the disk cannot be read. Only the binding row goes: nothing on disk is
// touched, and the audit record commits with the change.
func handleStorageUnassign(st *store.Store, auditor *audit.Service, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		class := storage.Class(r.PathValue("class"))
		if !class.Valid() {
			http.Error(w, "unknown storage class", http.StatusBadRequest)
			return
		}
		assignments := storage.NewAssignments(st)
		current, err := assignments.Get(class)
		if err != nil {
			http.Error(w, fmt.Sprintf("storage class %s has no assignment", class), http.StatusNotFound)
			return
		}
		affected, err := affectedOnMount(st, catalogDir, class, current.Mountpoint)
		if err != nil {
			writeStorageCheckError(w, err)
			return
		}
		if len(affected) > 0 {
			writeStorageInUse(w, storageInUse{
				Error: fmt.Sprintf("cannot unassign %s from %s: installed apps keep data there (%s). Uninstall them or move their data first",
					class, current.Mountpoint, strings.Join(affected, ", ")),
				Mountpoint: current.Mountpoint, AffectedPackages: affected,
			})
			return
		}
		tx, err := st.DB().Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()
		if err := assignments.UnassignTx(tx, class); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := auditor.RecordTx(tx, audit.Entry{
			Actor: operatorActor(r), Action: "storage.unassign", Outcome: audit.OutcomeOK,
			Detail: fmt.Sprintf("%s unassigned from %s; no data on the disk was touched", class, current.Mountpoint),
		}); err != nil {
			http.Error(w, "audit: "+err.Error()+" (nothing was changed)", http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleStorageUnregister drops a mount's registration (?mountpoint=). It
// refuses (409) while installed apps keep data on the mount or a class is
// still assigned to it. Only the registration row goes: the disk, its
// files and its mount are never touched.
func handleStorageUnregister(st *store.Store, auditor *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mountpoint := r.URL.Query().Get("mountpoint")
		if mountpoint == "" {
			http.Error(w, "mountpoint is required", http.StatusBadRequest)
			return
		}
		registry := storage.NewRegistry(st)
		if _, err := registry.Get(mountpoint); err != nil {
			http.Error(w, fmt.Sprintf("%s is not registered", mountpoint), http.StatusNotFound)
			return
		}
		all, err := storage.NewAssignments(st).List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var classes []string
		for _, assignment := range all {
			if assignment.Mountpoint == mountpoint {
				classes = append(classes, string(assignment.Class))
			}
		}
		affected, err := packagesOnMount(st, mountpoint)
		if err != nil {
			writeStorageCheckError(w, err)
			return
		}
		if len(affected) > 0 || len(classes) > 0 {
			reasons := []string{}
			if len(affected) > 0 {
				reasons = append(reasons, "installed apps keep data there ("+strings.Join(affected, ", ")+")")
			}
			if len(classes) > 0 {
				reasons = append(reasons, "storage classes are assigned to it ("+strings.Join(classes, ", ")+")")
			}
			writeStorageInUse(w, storageInUse{
				Error:      fmt.Sprintf("cannot unregister %s: %s", mountpoint, strings.Join(reasons, " and ")),
				Mountpoint: mountpoint, AffectedPackages: affected, Classes: classes,
			})
			return
		}
		tx, err := st.DB().Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()
		if err := registry.UnregisterTx(tx, mountpoint); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := auditor.RecordTx(tx, audit.Entry{
			Actor: operatorActor(r), Action: "storage.unregister", Outcome: audit.OutcomeOK,
			Detail: fmt.Sprintf("%s unregistered; no data on the disk was touched", mountpoint),
		}); err != nil {
			http.Error(w, "audit: "+err.Error()+" (nothing was changed)", http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// storageMountView adds registrability to a discovered mount. The
// embedded fields keep their PascalCase names, so existing clients are
// unaffected; the new fields are snake_case.
type storageMountView struct {
	discover.Mount
	Registrable       bool   `json:"registrable"`
	RegistrableReason string `json:"registrable_reason"`
}

func storageMountViews(mounts []discover.Mount) []storageMountView {
	out := make([]storageMountView, 0, len(mounts))
	for _, mount := range mounts {
		view := storageMountView{Mount: mount, Registrable: true}
		if err := storage.Registrable(mount); err != nil {
			view.Registrable = false
			view.RegistrableReason = err.Error()
		}
		out = append(out, view)
	}
	return out
}

// storageImpact is the 409 body: installed packages whose data sits on
// the mount a class would leave.
type storageImpact struct {
	Error            string   `json:"error"`
	Class            string   `json:"class"`
	From             string   `json:"from"`
	To               string   `json:"to"`
	AffectedPackages []string `json:"affected_packages"`
}

// affectedByReassign lists installed packages holding `<from>/<pkg>`
// data when `from` would no longer be bound to any class. Backups
// follow the current class bindings, so those packages would stop
// finding their data. The backup class is exempt: its package
// directories hold backup runs, not application data.
func affectedByReassign(st *store.Store, catalogDir string, class storage.Class, from string, assignments []storage.Assignment) ([]string, error) {
	if class == storage.ClassBackup {
		return nil, nil
	}
	for _, other := range assignments {
		if other.Class != class && other.Mountpoint == from {
			return nil, nil // another class keeps the mount in play
		}
	}
	return affectedOnMount(st, catalogDir, class, from)
}

func handleStorageMounts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, storageMountViews(discovered.Mounts))
	}
}

func handleStorageRegistrations(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		registrations, err := storage.NewRegistry(st).List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if registrations == nil {
			registrations = []storage.Registration{}
		}
		writeJSON(w, registrations)
	}
}

func handleStorageRegister(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mountpoint string `json:"mountpoint"`
			Label      string `json:"label"`
			Actor      string `json:"actor"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		registration, err := storage.NewRegistry(st).Register(body.Mountpoint, body.Label, body.Actor, discovered)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, registration)
	}
}

func handleStorageAssignments(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assignments, err := storage.NewAssignments(st).List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if assignments == nil {
			assignments = []storage.Assignment{}
		}
		writeJSON(w, assignments)
	}
}

func handleStorageAssign(st *store.Store, auditor auditRecorder, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Class      storage.Class `json:"class"`
			Mountpoint string        `json:"mountpoint"`
			Actor      string        `json:"actor"`
			// AcknowledgeImpact is the owner's explicit consent to move a
			// class off a mount that installed packages still use.
			AcknowledgeImpact bool `json:"acknowledge_impact"`
		}
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !body.Class.Valid() {
			http.Error(w, "unknown storage class", http.StatusBadRequest)
			return
		}
		assignments := storage.NewAssignments(st)
		if current, err := assignments.Get(body.Class); err == nil && current.Mountpoint != body.Mountpoint {
			all, err := assignments.List()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			affected, err := affectedByReassign(st, catalogDir, body.Class, current.Mountpoint, all)
			if err != nil {
				writeStorageCheckError(w, err)
				return
			}
			if len(affected) > 0 {
				if !body.AcknowledgeImpact {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(storageImpact{
						Error: fmt.Sprintf("moving %s off %s affects installed packages: their backups stop finding data there; re-send with acknowledge_impact to proceed",
							body.Class, current.Mountpoint),
						Class: string(body.Class), From: current.Mountpoint, To: body.Mountpoint, AffectedPackages: affected,
					})
					return
				}
				if _, err := auditor.Record(audit.Entry{
					Actor: body.Actor, Action: "storage.assign.impact_acknowledged", Outcome: audit.OutcomeOK,
					Detail: fmt.Sprintf("%s moved from %s to %s; affected packages: %v", body.Class, current.Mountpoint, body.Mountpoint, affected),
				}); err != nil {
					http.Error(w, "audit: "+err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
		discovered, err := discover.DiscoverStorage()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		assignment, err := storage.NewAssignments(st).Assign(body.Class, body.Mountpoint, body.Actor, storage.NewRegistry(st), discovered, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, assignment)
	}
}
