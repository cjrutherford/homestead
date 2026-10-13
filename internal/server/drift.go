package server

import (
	"context"
	"errors"
	"net/http"
	"os"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// driftDirFor resolves stack directories, stubbed in tests — the
// composeUp seam pattern. Production uses docker.DirFor.
var driftDirFor = docker.DirFor

// driftLiveSource supplies the Engine inventory for running-container
// comparison (S262), stubbed in tests with a fake Docker client.
var driftLiveSource = func() catalog.LiveSource { return docker.NewClient("") }

// MountDrift serves drift review and resolution (S213): per-package
// managed-state review plus accept and restore choices. Review and
// accept answer inline; restore runs through the operation engine
// with progress on the existing operation endpoints, so it returns
// an operation ID instead of blocking.
func (s *Server) MountDrift(st *store.Store, catalogDir string) {
	runner := operation.NewRunner(st)
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/drift", handleDriftReview(st, catalogDir))
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/drift/live", handleDriftLive(st, catalogDir))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/drift", handleDriftResolve(st, catalogDir, runner))
}

// driftReview is one viewed file: the verdict with both sides
// attached for display.
type driftReview struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Recorded string `json:"recorded"`
	Current  string `json:"current"`
}

func handleDriftReview(st *store.Store, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		manifest, ok := loadPackage(catalogDir, r.PathValue("name"), w)
		if !ok {
			return
		}
		review, err := catalog.Review(st, manifest.Name, driftDirFor(manifest.Name))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := []driftReview{}
		for _, file := range review {
			out = append(out, driftReview{
				Path: file.Path, Kind: file.Kind,
				Recorded: file.Recorded, Current: file.Current,
			})
		}
		writeJSON(w, out)
	}
}

// handleDriftLive serves the read-only running-container comparison
// (S262). An unreachable Engine answers checked=false, not an error.
func handleDriftLive(st *store.Store, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		manifest, ok := loadPackage(catalogDir, r.PathValue("name"), w)
		if !ok {
			return
		}
		review, err := catalog.LiveDrift(r.Context(), driftLiveSource(), manifest.Name, driftDirFor(manifest.Name))
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, os.ErrNotExist):
				status = http.StatusNotFound
			case errors.Is(err, catalog.ErrLiveCompose):
				status = http.StatusUnprocessableEntity
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, review)
	}
}

// liveConverges reports whether restore (compose up) can act on a live
// finding: only `restore` findings qualify. Recreate findings need the
// container removed first, so restore alone would be a no-op, and an
// owner-stopped stack reports no findings at all.
func liveConverges(ctx context.Context, name string) bool {
	review, err := catalog.LiveDrift(ctx, driftLiveSource(), name, driftDirFor(name))
	if err != nil {
		return false
	}
	for _, f := range review.Findings {
		if f.Resolution == catalog.ResolveRestore {
			return true
		}
	}
	return false
}

// driftAction carries the owner's choice: adopt current files, or
// rewrite recorded files and converge the stack.
type driftAction struct {
	Action string `json:"action"`
}

func handleDriftResolve(st *store.Store, catalogDir string, runner *operation.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		manifest, ok := loadPackage(catalogDir, r.PathValue("name"), w)
		if !ok {
			return
		}
		var body driftAction
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		dir := driftDirFor(manifest.Name)
		switch body.Action {
		case "accept":
			adopted, err := catalog.Accept(st, manifest.Name, dir)
			if errors.Is(err, catalog.ErrSecretDrift) {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"accepted": adopted})
		case "restore":
			review, err := catalog.Review(st, manifest.Name, dir)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if len(review) == 0 && !liveConverges(r.Context(), manifest.Name) {
				writeJSON(w, map[string]any{"id": 0, "detail": "no drift"})
				return
			}
			def, err := catalog.RestoreDefinition(st, manifest.Name, dir, manifest.Name)
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
		default:
			http.Error(w, "action must be accept or restore", http.StatusBadRequest)
			return
		}
	}
}
