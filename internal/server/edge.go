package server

import (
	"net/http"

	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// MountEdge serves edge-proxy operations (S237): Caddy deployment
// and route-table reconciliation. Both run through the operation
// engine with progress on the existing operation endpoints, so
// routes return operation IDs instead of blocking. Reading the
// table back belongs to later slices; these routes write.
func (s *Server) MountEdge(st *store.Store) {
	runner := operation.NewRunner(st)
	s.mux.HandleFunc("POST /api/v1/edge/deploy", handleEdgeDeploy(runner))
	s.mux.HandleFunc("POST /api/v1/edge/routes", handleEdgeRoutes(runner))
}

func handleEdgeDeploy(runner *operation.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		def, err := docker.DeployDefinition(docker.CaddyStack())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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

// routeTableBody carries the desired route table: capitalized keys
// matching the docker model, the same convention as update moves
// (S193).
type routeTableBody struct {
	Routes []docker.Route `json:"routes"`
}

func handleEdgeRoutes(runner *operation.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body routeTableBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Routes == nil {
			body.Routes = []docker.Route{}
		}
		def, err := docker.ReconcileDefinition(docker.CaddyStack(), docker.RouteSet{Routes: body.Routes})
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
