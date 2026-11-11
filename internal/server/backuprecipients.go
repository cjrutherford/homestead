package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"homestead/internal/audit"
	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/operation"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// MountBackupRecipients serves owner-held backup recipients and the
// recovery identity export (S271, ADR-022). Recipients are public keys;
// the export is the one route that returns key material, once per
// explicit confirmed call, never stored or logged by the daemon.
// recipientsYAML is where the inspectable configuration renders.
func (s *Server) MountBackupRecipients(st *store.Store, secrets catalog.SecretStore, recipientsYAML string) {
	h := &recipientHandlers{s: s, rec: backup.NewRecipientStore(st), audit: audit.New(st), secrets: secrets, yaml: recipientsYAML, runner: operation.NewRunner(st),
		destination: func() (string, error) {
			discovered, err := discover.DiscoverStorage()
			if err != nil {
				return "", err
			}
			return backup.ResolveDestination(storage.NewAssignments(st), discovered)
		}}
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
	s.mux.HandleFunc("POST /api/v1/backups/identity/ack", h.acknowledge)
	s.mux.HandleFunc("POST /api/v1/backups/identity/rotate", h.rotate)
	s.mux.HandleFunc("POST /api/v1/backups/identity/rewrap", h.rewrap)
}

type recipientHandlers struct {
	s       *Server
	rec     *backup.RecipientStore
	audit   auditRecorder
	secrets catalog.SecretStore
	yaml    string
	// destination resolves the backup root whose runs a re-wrap covers;
	// nil or an error means there are no runs to re-wrap.
	destination func() (string, error)
	// runner runs the background re-wrap (backup.rewrap).
	runner *operation.Runner
	// rewrapReported, when set, runs as a re-wrap reports its summary (a
	// test seam: it holds the operation open).
	rewrapReported func()
}

// record audits best-effort for routes that are already committed; the
// key-revealing export uses audited() instead and fails closed.
func (h *recipientHandlers) record(entry audit.Entry) {
	if err := h.audited(entry); err != nil {
		h.s.log.Error("backup recipient audit record failed", "action", entry.Action, "error", err)
	}
}

func (h *recipientHandlers) audited(entry audit.Entry) error {
	_, err := h.audit.Record(entry)
	return err
}

// renderAfterCommit renders the YAML for a change already committed to
// SQLite. A failure is logged and audited as its own entry so the
// action itself is not misreported; the next mount re-renders.
func (h *recipientHandlers) renderAfterCommit(actor, action string) {
	if err := h.render(); err != nil {
		h.s.log.Warn("backup recipients yaml render failed", "action", action, "error", err)
		h.record(audit.Entry{Actor: actor, Action: "backup.recipient.render", Outcome: audit.OutcomeError,
			Detail: "after " + action + ": YAML render failed: " + err.Error()})
	}
}

func (h *recipientHandlers) render() error {
	if h.yaml == "" {
		return nil
	}
	return h.rec.RenderYAML(h.yaml)
}

// appliance returns the appliance recipient without creating the
// identity: "" means it does not exist yet (first backup or an explicit
// export creates it).
func (h *recipientHandlers) appliance() (string, error) {
	keys, err := backupKeyStore(h.secrets)
	if err != nil {
		return "", err
	}
	id, ok, err := backup.PeekApplianceIdentity(keys)
	if err != nil || !ok {
		return "", err
	}
	return id.Recipient().String(), nil
}

type recipientList struct {
	Recipients []backup.Recipient `json:"recipients"`
	// OwnerHeld reports whether the doctor owner-recipient check is
	// satisfied (an owner recipient exists or an export was acknowledged).
	OwnerHeld bool `json:"owner_held"`
	// ApplianceCreated is false until the first backup or export creates
	// the appliance identity; listing never creates it.
	ApplianceCreated bool   `json:"appliance_created"`
	Note             string `json:"note"`
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
	all := owners
	if appliance != "" {
		all = append([]backup.Recipient{{Fingerprint: backup.Fingerprint(appliance), PublicKey: appliance,
			Label: "appliance identity", Kind: "appliance", Actor: "system"}}, owners...)
	}
	return recipientList{Recipients: all, OwnerHeld: held, ApplianceCreated: appliance != "",
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

// finish audits the committed change, renders the YAML, and answers.
func (h *recipientHandlers) finish(w http.ResponseWriter, actor, action, detail string, rec backup.Recipient) {
	h.record(audit.Entry{Actor: actor, Action: action, Outcome: audit.OutcomeOK, Detail: detail})
	h.renderAfterCommit(actor, action)
	writeJSON(w, rec)
}

type identityStatus struct {
	Created        bool   `json:"created"`
	Recipient      string `json:"recipient,omitempty"`
	Fingerprint    string `json:"fingerprint,omitempty"`
	Exported       bool   `json:"exported"`
	ExportedAt     string `json:"exported_at,omitempty"`
	ExportedBy     string `json:"exported_by,omitempty"`
	Acknowledged   bool   `json:"acknowledged"`
	AcknowledgedAt string `json:"acknowledged_at,omitempty"`
	// RetiredCount and RetiredUnreadable describe the earlier identities
	// kept to read older runs (S505); Rewrap is the last re-wrap summary.
	RetiredCount      int                   `json:"retired_count"`
	RetiredUnreadable int                   `json:"retired_unreadable"`
	Rewrap            *backup.RewrapSummary `json:"rewrap,omitempty"`
}

func (h *recipientHandlers) statusView() (identityStatus, error) {
	appliance, err := h.appliance()
	if err != nil {
		return identityStatus{}, err
	}
	out := identityStatus{Created: appliance != ""}
	if appliance != "" {
		out.Recipient, out.Fingerprint = appliance, backup.Fingerprint(appliance)
	}
	if keys, kerr := backupKeyStore(h.secrets); kerr == nil {
		if ids, bad, rerr := backup.RetiredIdentityReport(keys); rerr == nil {
			out.RetiredCount, out.RetiredUnreadable = len(ids), bad
		}
	}
	if sum, found, rerr := h.rec.RewrapLast(); rerr == nil && found {
		out.Rewrap = &sum
	}
	e, ok, err := h.rec.LastExport()
	if err != nil {
		return identityStatus{}, err
	}
	if ok {
		out.Exported, out.ExportedAt, out.ExportedBy = true, e.ExportedAt.Format(time.RFC3339), e.Actor
		if e.Acknowledged() {
			out.Acknowledged, out.AcknowledgedAt = true, e.AcknowledgedAt.Format(time.RFC3339)
		}
	}
	return out, nil
}

func (h *recipientHandlers) identityStatus(w http.ResponseWriter, r *http.Request) {
	out, err := h.statusView()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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

// export returns the appliance recovery identity on an explicit,
// confirmed request. It fails closed: the export is recorded and the
// audit entry written before the key is returned, and if either fails
// the key is withheld. Exports are rate limited. The value is never
// stored beyond the secret store, never logged, never cached. The
// export alone does not clear the doctor warning; the owner must
// acknowledge it (acknowledge).
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
		h.record(audit.Entry{Actor: actor, Action: "backup.identity.export", Outcome: audit.OutcomeError, Detail: "refused: " + err.Error()})
		if errors.Is(err, backup.ErrExportRateLimited) {
			http.Error(w, err.Error(), http.StatusTooManyRequests)
			return
		}
		http.Error(w, "export could not be recorded; identity withheld", http.StatusInternalServerError)
		return
	}
	detail := fmt.Sprintf("recovery identity for recipient %s (%s) exported", fp, id.Recipient().String())
	if err := h.audited(audit.Entry{Actor: actor, Action: "backup.identity.export", Outcome: audit.OutcomeOK, Detail: detail}); err != nil {
		h.s.log.Error("backup identity export audit failed; identity withheld", "error", err)
		http.Error(w, "export could not be audited; identity withheld", http.StatusInternalServerError)
		return
	}
	h.renderAfterCommit(actor, "backup.identity.export")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, exportResult{Identity: id.String(), Recipient: id.Recipient().String(),
		Fingerprint: fp, ExportedAt: e.ExportedAt.Format(time.RFC3339), Actor: actor})
}

// acknowledge records that the owner stored the exported key offline.
// This, not the export itself, clears the doctor warning.
func (h *recipientHandlers) acknowledge(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	actor := op.Session.Username
	e, err := h.rec.Acknowledge()
	if err != nil {
		h.record(audit.Entry{Actor: actor, Action: "backup.identity.ack", Outcome: audit.OutcomeError, Detail: err.Error()})
		status := http.StatusInternalServerError
		if errors.Is(err, backup.ErrNothingToAcknowledge) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	h.record(audit.Entry{Actor: actor, Action: "backup.identity.ack", Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("owner confirmed the recovery identity for recipient %s is stored offline", e.Fingerprint)})
	h.renderAfterCommit(actor, "backup.identity.ack")
	out, err := h.statusView()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

type rotateResult struct {
	exportResult
	PreviousFingerprint string `json:"previous_fingerprint,omitempty"`
	// RewrapOperationID follows the background re-wrap of existing runs;
	// absent when none could start (see RunsNote).
	RewrapOperationID int64 `json:"rewrap_operation_id,omitempty"`
	// RunsNote says what happens to existing runs, or why nothing does.
	RunsNote string `json:"runs_note"`
}

// rewrapPayload is the operation payload of a re-wrap: where it looks,
// never a key.
type rewrapPayload struct {
	Destination string `json:"destination"`
}

var errNoRewrapDestination = errors.New("no backup destination is available")

// startRewrap resolves the destination and starts the background re-wrap
// of existing runs to the current key. The summary lands in the store
// when the walk ends.
func (h *recipientHandlers) startRewrap(keys backup.KeyStore) (int64, error) {
	if h.destination == nil || h.runner == nil {
		return 0, errNoRewrapDestination
	}
	root, err := h.destination()
	if err != nil {
		return 0, fmt.Errorf("%w (%v)", errNoRewrapDestination, err)
	}
	ready := make(chan struct{})
	var opID int64
	def, err := backup.RewrapDefinition(keys, root, func(sum backup.RewrapSummary) {
		<-ready
		if h.rewrapReported != nil {
			h.rewrapReported()
		}
		sum.OperationID = opID
		if err := h.rec.SetRewrapLast(sum); err != nil {
			h.s.log.Warn("backup re-wrap summary not recorded", "error", err)
		}
	})
	if err != nil {
		close(ready)
		return 0, err
	}
	payload, _ := json.Marshal(rewrapPayload{Destination: root})
	op, err := h.runner.Start(context.Background(), def, string(payload))
	if err != nil {
		def.Cleanup()
		close(ready)
		return 0, err
	}
	opID = op.ID
	close(ready)
	return op.ID, nil
}

// rotate mints a new appliance recovery identity (S492, S505). It needs
// the fresh password of an Authentik session (step-up) and an explicit
// confirm. The request is quick: it retires the old identity, stores the
// new one, and starts a background re-wrap of existing runs (header only,
// nothing deleted; the old identity stays as a fallback). It is refused
// (409) while a backup runs, while a re-wrap runs, or while the current
// key's export is unacknowledged: the owner exports that key again rather
// than minting a second one after a lost response. Like export it fails
// closed: the new key is returned only after the export record and the
// audit entry are written, and it is shown once. The owner must
// acknowledge it, which re-arms the doctor warning until they do.
func (h *recipientHandlers) rotate(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "rotating the recovery identity requires \"confirm\": true", http.StatusBadRequest)
		return
	}
	keys, err := backupKeyStore(h.secrets)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	refuse := func(status int, msg string) {
		h.record(audit.Entry{Actor: actor, Action: "backup.identity.rotate", Outcome: audit.OutcomeError, Detail: "refused: " + msg})
		http.Error(w, msg, status)
	}
	if cur, ok, perr := backup.PeekApplianceIdentity(keys); perr != nil {
		http.Error(w, perr.Error(), http.StatusInternalServerError)
		return
	} else if ok {
		fp := backup.Fingerprint(cur.Recipient().String())
		last, exported, lerr := h.rec.LastExport()
		if lerr != nil {
			http.Error(w, lerr.Error(), http.StatusInternalServerError)
			return
		}
		if exported && last.Fingerprint == fp && !last.Acknowledged() {
			refuse(http.StatusConflict, "the current key "+fp+" has not been confirmed stored; export it again (`homesteadctl backup identity export`) and acknowledge it before rotating")
			return
		}
	}
	if err := h.rec.ExportBudget(); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, backup.ErrExportRateLimited) {
			status = http.StatusTooManyRequests
		}
		refuse(status, err.Error())
		return
	}
	res, err := backup.RotateIdentity(keys)
	if err != nil {
		if errors.Is(err, backup.ErrBackupRunning) || errors.Is(err, backup.ErrRewrapRunning) {
			refuse(http.StatusConflict, err.Error())
			return
		}
		h.record(audit.Entry{Actor: actor, Action: "backup.identity.rotate", Outcome: audit.OutcomeError, Detail: err.Error()})
		http.Error(w, "rotation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	fp := backup.Fingerprint(res.New.Recipient().String())
	// Pending until a re-wrap completes; survives a restart or an
	// unmounted destination.
	if err := h.rec.SetRewrapLast(backup.RewrapSummary{Fingerprint: fp}); err != nil {
		h.s.log.Warn("backup re-wrap pending marker not recorded", "error", err)
	}
	e, err := h.rec.RecordExport(fp, actor)
	if err != nil {
		h.record(audit.Entry{Actor: actor, Action: "backup.identity.rotate", Outcome: audit.OutcomeError,
			Detail: "rotated to " + fp + " but the export could not be recorded; the key is withheld, export it again: " + err.Error()})
		http.Error(w, "the identity was rotated but could not be recorded; export the new key with the export command", http.StatusInternalServerError)
		return
	}
	opID, rerr := h.startRewrap(keys)
	note := "existing runs are being re-wrapped to the new key in the background"
	if rerr != nil {
		note = "existing runs were not re-wrapped (" + rerr.Error() + "); run `homesteadctl backup identity rewrap` once it can"
	}
	detail := fmt.Sprintf("recovery identity rotated from %s to %s (%s); %s", res.OldFingerprint, fp, res.New.Recipient().String(), note)
	if opID != 0 {
		detail += fmt.Sprintf(" (operation %d)", opID)
	}
	if err := h.audited(audit.Entry{Actor: actor, Action: "backup.identity.rotate", Outcome: audit.OutcomeOK, Detail: detail}); err != nil {
		h.s.log.Error("backup identity rotation audit failed; identity withheld", "error", err)
		http.Error(w, "rotation could not be audited; export the new key with the export command", http.StatusInternalServerError)
		return
	}
	h.renderAfterCommit(actor, "backup.identity.rotate")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, rotateResult{
		exportResult: exportResult{Identity: res.New.String(), Recipient: res.New.Recipient().String(),
			Fingerprint: fp, ExportedAt: e.ExportedAt.Format(time.RFC3339), Actor: actor},
		PreviousFingerprint: res.OldFingerprint, RewrapOperationID: opID, RunsNote: note,
	})
}

// rewrap re-wraps existing runs to the current recovery key without
// minting one (S505). It reveals and changes no key, so it needs no
// step-up; it is audited and refused (409) while a re-wrap runs or when
// there is no destination.
func (h *recipientHandlers) rewrap(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	actor := op.Session.Username
	keys, err := backupKeyStore(h.secrets)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, ok, perr := backup.PeekApplianceIdentity(keys); perr != nil {
		http.Error(w, perr.Error(), http.StatusInternalServerError)
		return
	} else if !ok {
		http.Error(w, "there is no recovery identity yet, so there is nothing to re-wrap to", http.StatusConflict)
		return
	}
	opID, err := h.startRewrap(keys)
	if err != nil {
		h.record(audit.Entry{Actor: actor, Action: "backup.identity.rewrap", Outcome: audit.OutcomeError, Detail: "refused: " + err.Error()})
		status := http.StatusInternalServerError
		if errors.Is(err, backup.ErrRewrapRunning) || errors.Is(err, errNoRewrapDestination) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	h.record(audit.Entry{Actor: actor, Action: "backup.identity.rewrap", Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("re-wrap of existing backups to the current recovery key started (operation %d)", opID)})
	writeJSON(w, map[string]int64{"id": opID})
}
