package server

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"homestead/internal/audit"
	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/store"
)

// MountBackupRecipients serves owner-held backup recipients and the
// recovery identity export (S271, ADR-022). Recipients are public keys;
// the export is the one route that returns key material, once per
// explicit confirmed call, never stored or logged by the daemon.
// recipientsYAML is where the inspectable configuration renders.
func (s *Server) MountBackupRecipients(st *store.Store, secrets catalog.SecretStore, recipientsYAML string) {
	h := &recipientHandlers{s: s, rec: backup.NewRecipientStore(st), audit: audit.New(st), secrets: secrets, yaml: recipientsYAML}
	if recipientsYAML != "" {
		if err := h.rec.RenderYAML(recipientsYAML); err != nil {
			s.log.Warn("backup recipients yaml render failed", "error", err)
		}
	}
	s.mux.HandleFunc("GET /api/v1/backups/recipients", h.list)
	s.mux.HandleFunc("POST /api/v1/backups/recipients", h.add)
	s.mux.HandleFunc("DELETE /api/v1/backups/recipients/{fingerprint}", h.remove)
	s.mux.HandleFunc("GET /api/v1/backups/identity", h.identityStatus)
	s.mux.HandleFunc("POST /api/v1/backups/identity/export", h.export)
}

type recipientHandlers struct {
	s       *Server
	rec     *backup.RecipientStore
	audit   *audit.Service
	secrets catalog.SecretStore
	yaml    string
}

func (h *recipientHandlers) record(entry audit.Entry) {
	if _, err := h.audit.Record(entry); err != nil {
		h.s.log.Error("backup recipient audit record failed", "action", entry.Action, "error", err)
	}
}

func (h *recipientHandlers) render() error {
	if h.yaml == "" {
		return nil
	}
	return h.rec.RenderYAML(h.yaml)
}

func (h *recipientHandlers) appliance() (string, error) {
	keys, err := backupKeyStore(h.secrets)
	if err != nil {
		return "", err
	}
	id, err := backup.ApplianceIdentity(keys)
	if err != nil {
		return "", err
	}
	return id.Recipient().String(), nil
}

type recipientList struct {
	Recipients []backup.Recipient `json:"recipients"`
	// OwnerHeld reports whether the doctor owner-recipient check is
	// satisfied (an owner recipient exists or an export was recorded).
	OwnerHeld bool   `json:"owner_held"`
	Note      string `json:"note"`
}

func (h *recipientHandlers) view() (recipientList, error) {
	appliance, err := h.appliance()
	if err != nil {
		return recipientList{}, err
	}
	owners, err := h.rec.Owner()
	if err != nil {
		return recipientList{}, err
	}
	held, err := h.rec.OwnerProtected()
	if err != nil {
		return recipientList{}, err
	}
	all := append([]backup.Recipient{{Fingerprint: backup.Fingerprint(appliance), PublicKey: appliance,
		Label: "appliance identity", Kind: "appliance", Actor: "system"}}, owners...)
	return recipientList{Recipients: all, OwnerHeld: held,
		Note: "New backup runs encrypt to every recipient listed. Existing runs are not re-encrypted."}, nil
}

func (h *recipientHandlers) list(w http.ResponseWriter, r *http.Request) {
	v, err := h.view()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func recipientStatus(err error) int {
	switch {
	case errors.Is(err, backup.ErrInvalidRecipient):
		return http.StatusUnprocessableEntity
	case errors.Is(err, backup.ErrRecipientExists), errors.Is(err, backup.ErrApplianceRecipient):
		return http.StatusConflict
	case errors.Is(err, backup.ErrRecipientNotFound):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func (h *recipientHandlers) add(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body struct {
		PublicKey string `json:"public_key"`
		Label     string `json:"label"`
	}
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	appliance, err := h.appliance()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	actor := op.Session.Username
	rec, err := h.rec.Add(body.PublicKey, body.Label, actor, appliance)
	if err != nil {
		h.record(audit.Entry{Actor: actor, Action: "backup.recipient.add", Outcome: audit.OutcomeError, Detail: err.Error()})
		http.Error(w, err.Error(), recipientStatus(err))
		return
	}
	detail := fmt.Sprintf("recipient %s added (%s); applies to new runs only", rec.Fingerprint, rec.PublicKey)
	h.finish(w, actor, "backup.recipient.add", detail, rec)
}

func (h *recipientHandlers) remove(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	appliance, err := h.appliance()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	actor := op.Session.Username
	rec, err := h.rec.Remove(r.PathValue("fingerprint"), appliance)
	if err != nil {
		h.record(audit.Entry{Actor: actor, Action: "backup.recipient.remove", Outcome: audit.OutcomeError, Detail: err.Error()})
		http.Error(w, err.Error(), recipientStatus(err))
		return
	}
	detail := fmt.Sprintf("recipient %s removed (%s); earlier runs stay readable by it", rec.Fingerprint, rec.PublicKey)
	h.finish(w, actor, "backup.recipient.remove", detail, rec)
}

// finish renders the YAML, audits, and answers with the recipient.
func (h *recipientHandlers) finish(w http.ResponseWriter, actor, action, detail string, rec backup.Recipient) {
	if err := h.render(); err != nil {
		h.record(audit.Entry{Actor: actor, Action: action, Outcome: audit.OutcomeError, Detail: detail + "; saved but YAML render failed: " + err.Error()})
		http.Error(w, "saved but YAML render failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.record(audit.Entry{Actor: actor, Action: action, Outcome: audit.OutcomeOK, Detail: detail})
	writeJSON(w, rec)
}

type identityStatus struct {
	Recipient   string `json:"recipient"`
	Fingerprint string `json:"fingerprint"`
	Exported    bool   `json:"exported"`
	ExportedAt  string `json:"exported_at,omitempty"`
	ExportedBy  string `json:"exported_by,omitempty"`
}

func (h *recipientHandlers) identityStatus(w http.ResponseWriter, r *http.Request) {
	appliance, err := h.appliance()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := identityStatus{Recipient: appliance, Fingerprint: backup.Fingerprint(appliance)}
	e, ok, err := h.rec.LastExport()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ok {
		out.Exported, out.ExportedAt, out.ExportedBy = true, e.ExportedAt.Format(time.RFC3339), e.Actor
	}
	writeJSON(w, out)
}

type exportResult struct {
	// Identity is the age secret key. It appears in this response only.
	Identity    string `json:"identity"`
	Recipient   string `json:"recipient"`
	Fingerprint string `json:"fingerprint"`
	ExportedAt  string `json:"exported_at"`
	Actor       string `json:"actor"`
}

// export returns the appliance recovery identity once per explicit,
// confirmed request. The export is recorded (time, actor, fingerprint)
// and audited before the value leaves; the value itself is never
// stored by the daemon beyond the secret store, never logged, never
// cached.
func (h *recipientHandlers) export(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	actor := op.Session.Username
	if !body.Confirm {
		http.Error(w, "exporting the recovery identity requires \"confirm\": true", http.StatusBadRequest)
		return
	}
	keys, err := backupKeyStore(h.secrets)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, err := backup.ApplianceIdentity(keys)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fp := backup.Fingerprint(id.Recipient().String())
	e, err := h.rec.RecordExport(fp, actor)
	if err != nil {
		h.record(audit.Entry{Actor: actor, Action: "backup.identity.export", Outcome: audit.OutcomeError, Detail: "export not recorded; identity withheld"})
		http.Error(w, "export could not be recorded; identity withheld", http.StatusInternalServerError)
		return
	}
	detail := fmt.Sprintf("recovery identity for recipient %s (%s) exported", fp, id.Recipient().String())
	if err := h.render(); err != nil {
		detail += "; YAML render failed: " + err.Error()
	}
	h.record(audit.Entry{Actor: actor, Action: "backup.identity.export", Outcome: audit.OutcomeOK, Detail: detail})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, exportResult{Identity: id.String(), Recipient: id.Recipient().String(),
		Fingerprint: fp, ExportedAt: e.ExportedAt.Format(time.RFC3339), Actor: actor})
}
