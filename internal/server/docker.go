package server

import (
	"net/http"

	"homestead/internal/docker"
)

// MountDocker serves Docker platform health (S073): engine versions plus
// image, container, and network inventory, read-only. Lifecycle stays in
// the Compose CLI (S074/S075); this endpoint never mutates anything.
func (s *Server) MountDocker(client *docker.Client) {
	s.mux.HandleFunc("GET /api/v1/docker/health", handleDockerHealth(client))
}

func handleDockerHealth(client *docker.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		health, err := client.Health(r.Context())
		if err != nil {
			http.Error(w, "failed to read Docker health", http.StatusInternalServerError)
			return
		}
		writeJSON(w, health)
	}
}
