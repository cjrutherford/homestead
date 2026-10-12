package server

import (
	"fmt"
	"net/http"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// MountSecrets serves secret-store key custody (S258, ADR-021): the
// current key version and the audited owner re-key operation. Responses
// and audit entries carry versions and operation ids, never key material.
func (s *Server) MountSecrets(st *store.Store, secrets *secret.Store) {
	s.mux.HandleFunc("GET /api/v1/secrets/key", handleSecretKeyStatus(secrets))
	s.mux.HandleFunc("POST /api/v1/secrets/rekey", handleSecretRekey(s, st, secrets))
}

func handleSecretKeyStatus(secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"version":     secrets.KeyVersion(),
			"in_progress": secrets.RekeyInProgress(),
		})
	}
}

// rekeyBody carries the explicit owner confirmation: a re-key rewrites
// every stored secret, so a bare POST is refused.
type rekeyBody struct {
	Confirm bool `json:"confirm"`
}

func handleSecretRekey(s *Server, st *store.Store, secrets *secret.Store) http.HandlerFunc {
	runner := operation.NewRunner(st)
	auditSvc := audit.New(st)
	return func(w http.ResponseWriter, r *http.Request) {
		op, ok := requireOperator(w, r)
		if !ok {
			return
		}
		var body rekeyBody
		if err := decodeBody(r, &body); err != nil || !body.Confirm {
			http.Error(w, "re-key must be confirmed: send {\"confirm\": true}", http.StatusBadRequest)
			return
		}
		if secrets.RekeyInProgress() {
			http.Error(w, secret.ErrRekeyInProgress.Error(), http.StatusConflict)
			return
		}
		from := secrets.KeyVersion()
		started, err := runner.Start(r.Context(), secrets.RekeyDefinition(), "")
		if err != nil {
			http.Error(w, "failed to start re-key", http.StatusInternalServerError)
			return
		}
		actor := op.Session.Username
		record := func(entry audit.Entry) {
			if _, err := auditSvc.Record(entry); err != nil {
				s.log.Error("secret re-key audit record failed", "action", entry.Action, "operation", started.ID, "error", err)
			}
		}
		record(audit.Entry{
			Actor: actor, Action: "secret.rekey.start", Outcome: audit.OutcomeOK,
			Detail: fmt.Sprintf("operation %d from key version %d", started.ID, from),
		})
		go func() {
			done, err := runner.Wait(s.bg, started.ID)
			if err != nil && s.bg.Err() != nil {
				// Daemon shutdown: the operation is failed by Recover at
				// next start; there is no outcome to audit yet.
				s.log.Warn("secret re-key audit skipped at shutdown", "operation", started.ID)
				return
			}
			entry := audit.Entry{
				Actor: actor, Action: "secret.rekey", Outcome: audit.OutcomeOK,
				Detail: fmt.Sprintf("operation %d: key version %d -> %d", started.ID, from, secrets.KeyVersion()),
			}
			if err != nil || done.Status != operation.StatusSucceeded {
				entry.Outcome = audit.OutcomeError
				entry.Detail = fmt.Sprintf("operation %d did not complete (key version %d)", started.ID, secrets.KeyVersion())
			}
			record(entry)
		}()
		writeJSON(w, map[string]any{"id": started.ID})
	}
}
