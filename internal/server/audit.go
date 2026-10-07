package server

import (
	"net/http"
	"strconv"

	"homestead/internal/audit"
)

// MountAudit serves the audit trail read API (S063): newest-first
// entries with an optional limit. The trail is append-only; no write,
// update, or delete route exists.
func (s *Server) MountAudit(svc *audit.Service) {
	s.mux.HandleFunc("GET /api/v1/audit", handleAuditList(svc))
}

func handleAuditList(svc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				http.Error(w, "invalid limit", http.StatusBadRequest)
				return
			}
			limit = parsed
		}
		entries, err := svc.List(limit)
		if err != nil {
			http.Error(w, "failed to read audit trail", http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []audit.Entry{}
		}
		writeJSON(w, entries)
	}
}
