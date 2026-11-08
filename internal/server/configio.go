package server

import (
	"errors"
	"fmt"
	"net/http"

	"homestead/internal/audit"
	"homestead/internal/desired"
	"homestead/internal/secret"
)

// MountConfigIO serves configuration export and import (S234):
// disaster-recovery affordances over the S231 document. Export
// answers the portable document (secret names only, never values);
// import validates it, demands every secret value, and restores
// settings plus secrets. Values arrive per call and never persist
// in the document.
func (s *Server) MountConfigIO(desiredPath string, secrets *secret.Store, auditSvc *audit.Service) {
	s.mux.HandleFunc("GET /api/v1/config/export", handleConfigExport(desiredPath, secrets))
	s.mux.HandleFunc("POST /api/v1/config/import/preview", handleConfigImportPreview(desiredPath))
	s.mux.HandleFunc("POST /api/v1/config/import", handleConfigImport(desiredPath, secrets, auditSvc))
}

// previewBody carries the document text (YAML or JSON) to check.
type previewBody struct {
	Text string `json:"text"`
}

// handleConfigImportPreview validates an uploaded document and shows
// the settings it would change, writing nothing (S483).
func handleConfigImportPreview(desiredPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body previewBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, desired.PreviewImport([]byte(body.Text), desiredPath))
	}
}

// importBody carries the inline document plus secret values keyed by
// manifest name. Values are required input, never stored output.
type importBody struct {
	Document desired.Export    `json:"document"`
	Secrets  map[string]string `json:"secrets"`
}

func handleConfigExport(desiredPath string, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wantYAML := r.URL.Query().Get("format") == "yaml"
		export, err := desired.ExportState(desiredPath, secrets)
		if err != nil {
			if errors.Is(err, desired.ErrNotFound) {
				http.Error(w, "nothing configured yet", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if wantYAML {
			// The same bytes `config export -f` writes. The document
			// carries secret names, never values, so no fresh sign-in
			// is needed (S483).
			body, err := desired.MarshalExportYAML(export)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/yaml")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(body)
			return
		}
		writeJSON(w, export)
	}
}

func handleConfigImport(desiredPath string, secrets *secret.Store, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body importBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Secrets == nil {
			body.Secrets = map[string]string{}
		}
		actor := "owner"
		if op, ok := operatorFromContext(r.Context()); ok && op.Session.Username != "" {
			actor = op.Session.Username
		}
		summary, err := desired.ImportExport(body.Document, desiredPath, secrets, body.Secrets)
		if err != nil {
			recordImportAudit(auditSvc, actor, audit.OutcomeError, "config import refused")
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		recordImportAudit(auditSvc, actor, audit.OutcomeOK,
			fmt.Sprintf("config import restored %d settings and %d secrets (names only)", summary.Settings, summary.Secrets))
		writeJSON(w, map[string]any{"settings": summary.Settings, "secrets": summary.Secrets})
	}
}

// recordImportAudit leaves the import in the audit log: who, outcome,
// counts. Secret names and values are never written.
func recordImportAudit(svc *audit.Service, actor, outcome, detail string) {
	if svc == nil {
		return
	}
	_, _ = svc.Record(audit.Entry{Actor: actor, Action: "config.import", Outcome: outcome, Detail: detail})
}
