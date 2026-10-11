package server

import (
	"errors"
	"net/http"

	"homestead/internal/desired"
	"homestead/internal/secret"
)

// MountConfigIO serves configuration export and import (S234):
// disaster-recovery affordances over the S231 document. Export
// answers the portable document (secret names only, never values);
// import validates it, demands every secret value, and restores
// settings plus secrets. Values arrive per call and never persist
// in the document.
func (s *Server) MountConfigIO(desiredPath string, secrets *secret.Store) {
	s.mux.HandleFunc("GET /api/v1/config/export", handleConfigExport(desiredPath, secrets))
	s.mux.HandleFunc("POST /api/v1/config/import", handleConfigImport(desiredPath, secrets))
}

// importBody carries the inline document plus secret values keyed by
// manifest name. Values are required input, never stored output.
type importBody struct {
	Document desired.Export    `json:"document"`
	Secrets  map[string]string `json:"secrets"`
}

func handleConfigExport(desiredPath string, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		export, err := desired.ExportState(desiredPath, secrets)
		if err != nil {
			if errors.Is(err, desired.ErrNotFound) {
				http.Error(w, "nothing configured yet", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, export)
	}
}

func handleConfigImport(desiredPath string, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body importBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Secrets == nil {
			body.Secrets = map[string]string{}
		}
		summary, err := desired.ImportExport(body.Document, desiredPath, secrets, body.Secrets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, map[string]any{"settings": summary.Settings, "secrets": summary.Secrets})
	}
}
