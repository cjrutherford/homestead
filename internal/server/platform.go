package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"homestead/internal/audit"
	"homestead/internal/doctor"
	"homestead/internal/release"
	"homestead/internal/store"
	"homestead/internal/update"
	"homestead/internal/version"
)

// MountPlatform serves platform release verification (S264, ADR-025):
// the verified-state plan read, local artifact verification, and
// owner-managed trusted keys. Nothing here fetches; the owner supplies
// artifact, checksums, and signature paths on the appliance. trustYAML
// is where the trusted-key configuration renders for inspection.
func (s *Server) MountPlatform(st *store.Store, trustYAML string) {
	h := &platformHandlers{s: s, rel: release.NewStore(st), audit: audit.New(st), trustYAML: trustYAML}
	if trustYAML != "" {
		if err := h.render(); err != nil {
			s.log.Warn("platform trust yaml render failed", "error", err)
		}
	}
	s.mux.HandleFunc("GET /api/v1/platform/updates", h.plan)
	s.mux.HandleFunc("POST /api/v1/platform/updates/verify", h.verify)
	s.mux.HandleFunc("GET /api/v1/platform/trust-keys", h.listKeys)
	s.mux.HandleFunc("POST /api/v1/platform/trust-keys", h.addKey)
	s.mux.HandleFunc("PUT /api/v1/platform/trust-keys/{id}", h.replaceKey)
	s.mux.HandleFunc("DELETE /api/v1/platform/trust-keys/{id}", h.removeKey)
}

type platformHandlers struct {
	s         *Server
	rel       *release.Store
	audit     *audit.Service
	trustYAML string
}

func (h *platformHandlers) render() error {
	if h.trustYAML == "" {
		return nil
	}
	return h.rel.RenderYAML(h.trustYAML)
}

func (h *platformHandlers) record(entry audit.Entry) {
	if _, err := h.audit.Record(entry); err != nil {
		h.s.log.Error("platform audit record failed", "action", entry.Action, "error", err)
	}
}

// releaseView is a verification record with its plan outcome.
type releaseView struct {
	release.Release
	// PlanError explains why PlanPlatform refused this release (for
	// example a downgrade) or why it was never offered to the planner
	// (not verified); empty when it planned.
	PlanError string `json:"plan_error"`
}

type platformPlanView struct {
	Running  map[string]string   `json:"running"`
	Plan     update.PlatformPlan `json:"plan"`
	Releases []releaseView       `json:"releases"`
	// Refused lists releases that failed or are not verified. They never
	// enter the plan: nothing unverified is plannable.
	Refused   []releaseView                `json:"refused"`
	Installed map[string]release.Installed `json:"installed"`
}

func (h *platformHandlers) plan(w http.ResponseWriter, r *http.Request) {
	releases, err := h.rel.Releases()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The daemon and CLI ship from one build, so both run this version.
	running := map[string]string{update.ComponentDaemon: version.Version, update.ComponentCLI: version.Version}
	installed := map[string]release.Installed{}
	for component := range running {
		in, err := h.rel.Installed(component)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		installed[component] = in
	}
	views := make([]releaseView, 0, len(releases))
	refused := []releaseView{}
	var desired []update.PlatformRelease
	for _, rel := range releases {
		view := releaseView{Release: rel}
		if rel.State != release.StateVerified {
			// Failed, rollback-refused, and unverified releases are shown,
			// never planned.
			view.PlanError = "refused: " + rel.State
			if rel.Detail != "" {
				view.PlanError += ": " + rel.Detail
			}
			refused = append(refused, view)
		} else if rel.Version != "" {
			candidate := update.PlatformRelease{Component: rel.Component, Version: rel.Version, Verification: rel.State}
			if _, err := update.PlanPlatform(running, []update.PlatformRelease{candidate}); err != nil {
				view.PlanError = err.Error()
			} else {
				desired = append(desired, candidate)
			}
		}
		views = append(views, view)
	}
	plan, err := update.PlanPlatform(running, desired)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if plan.Components == nil {
		plan.Components = []update.ComponentUpdate{}
	}
	if plan.Warnings == nil {
		plan.Warnings = []string{}
	}
	writeJSON(w, platformPlanView{Running: running, Plan: plan, Releases: views, Refused: refused, Installed: installed})
}

type verifyBody struct {
	Component  string `json:"component"`
	Artifact   string `json:"artifact"`
	Checksums  string `json:"checksums"`
	Signature  string `json:"signature"`
	AllowOlder bool   `json:"allow_older"`
}

func (h *platformHandlers) verify(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body verifyBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !update.IsPlatformComponent(body.Component) {
		http.Error(w, fmt.Sprintf("unknown platform component %q", body.Component), http.StatusBadRequest)
		return
	}
	for name, path := range map[string]string{"artifact": body.Artifact, "checksums": body.Checksums, "signature": body.Signature} {
		if !filepath.IsAbs(path) {
			http.Error(w, name+" must be an absolute path on the appliance", http.StatusBadRequest)
			return
		}
	}
	keys, err := h.rel.PublicKeys()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	installed, err := h.rel.Installed(body.Component)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	res, verr := release.Verify(release.Inputs{
		ArtifactPath: body.Artifact, ChecksumsPath: body.Checksums, SignaturePath: body.Signature,
		Trusted: keys, Installed: installed, AllowOlder: body.AllowOlder,
	})
	if verr == nil {
		// Even a signed version must be orderable by the planner.
		if err := (update.PlatformRelease{Component: body.Component, Version: res.Version}).Validate(); err != nil {
			verr = fmt.Errorf("%w: %v", release.ErrBadChecksums, err)
		}
	}
	// Unreadable inputs are a bad request, not a verification verdict. The
	// answer is generic on purpose: it names the field, never the path,
	// and does not say whether the path exists.
	if verr != nil && (errors.Is(verr, release.ErrNotRegularFile) || errors.Is(verr, release.ErrTooLarge)) {
		http.Error(w, verifyFileError(verr, body), http.StatusBadRequest)
		return
	}
	rec := release.Release{Component: body.Component, Actor: op.Session.Username}
	planJSON, _ := json.Marshal(map[string]any{
		"component": body.Component, "artifact": body.Artifact, "checksums": body.Checksums,
		"signature": body.Signature, "allow_older": body.AllowOlder,
	})
	entry := audit.Entry{Actor: op.Session.Username, Action: "platform.verify", Plan: string(planJSON)}
	switch {
	case verr == nil:
		rec.State, rec.Version, rec.Counter, rec.KeyID, rec.SHA256, rec.Override = release.StateVerified, res.Version, res.Counter, res.KeyID, res.SHA256, res.Override
		rec.Detail = fmt.Sprintf("verified %s counter %d with key %s", res.Version, res.Counter, res.KeyID)
		// Seed the rollback baseline: a verified release equal to the
		// running version is what is installed. Never overwrites a
		// record, and never seeds from an override (that is by
		// definition older than a record that exists).
		if installed.Counter == 0 && res.Version == version.Version {
			if seeded, err := h.rel.SeedInstalled(body.Component, res.Version, res.Counter); err != nil {
				h.s.log.Error("platform installed baseline seed failed", "component", body.Component, "error", err)
			} else if seeded {
				rec.Detail += "; recorded as the installed baseline"
			}
		}
		entry.Outcome, entry.Detail = audit.OutcomeOK, rec.Detail
		if res.Override {
			entry.Override = true
			entry.OverrideReason = fmt.Sprintf("owner allowed older release: installed %s (counter %d), artifact %s (counter %d)",
				installed.Version, installed.Counter, res.Version, res.Counter)
			entry.Detail = rec.Detail + "; " + entry.OverrideReason
			rec.Detail = entry.Detail
		}
	default:
		rec.State, rec.Detail = release.StateFailed, verr.Error()
		rec.Refusal = release.RefusalAuth
		switch {
		case errors.Is(verr, release.ErrRollback):
			rec.Refusal = release.RefusalRollback
		case errors.Is(verr, release.ErrCounterConflict):
			rec.Refusal = release.RefusalCounter
		}
		if rec.Refusal != release.RefusalAuth {
			// Signed and digest-checked: version and counter are trusted.
			rec.Version, rec.Counter, rec.KeyID, rec.SHA256 = res.Version, res.Counter, res.KeyID, res.SHA256
		}
		entry.Outcome, entry.Detail = audit.OutcomeError, verr.Error()
	}
	if err := h.rel.SaveRelease(rec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.record(entry)
	saved, _ := h.rel.Releases()
	for _, candidate := range saved {
		if candidate.Component == rec.Component {
			rec = candidate
		}
	}
	writeJSON(w, rec)
}

// verifyFileError renders a file refusal without echoing paths.
func verifyFileError(err error, body verifyBody) string {
	if errors.Is(err, release.ErrTooLarge) {
		return "a supplied file exceeds the size limit"
	}
	return "artifact, checksums, and signature must each be a readable regular file"
}

func (h *platformHandlers) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.rel.Keys()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if keys == nil {
		keys = []release.TrustKey{}
	}
	writeJSON(w, keys)
}

type keyBody struct {
	PublicKey string `json:"public_key"`
	Label     string `json:"label"`
}

func keyStatus(err error) int {
	switch {
	case errors.Is(err, release.ErrKeyNotFound):
		return http.StatusNotFound
	case errors.Is(err, release.ErrKeyExists):
		return http.StatusConflict
	case errors.Is(err, release.ErrInvalidKey):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}

func (h *platformHandlers) addKey(w http.ResponseWriter, r *http.Request) { h.saveKey(w, r, "") }

func (h *platformHandlers) replaceKey(w http.ResponseWriter, r *http.Request) {
	h.saveKey(w, r, strings.ToUpper(r.PathValue("id")))
}

func (h *platformHandlers) saveKey(w http.ResponseWriter, r *http.Request, oldID string) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body keyBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	action := "platform.trust.add"
	var key release.TrustKey
	var err error
	if oldID != "" {
		action = "platform.trust.replace"
		key, err = h.rel.ReplaceKey(oldID, body.PublicKey, body.Label, op.Session.Username)
	} else {
		key, err = h.rel.AddKey(body.PublicKey, body.Label, op.Session.Username)
	}
	if err != nil {
		h.record(audit.Entry{Actor: op.Session.Username, Action: action, Outcome: audit.OutcomeError, Detail: err.Error()})
		http.Error(w, err.Error(), keyStatus(err))
		return
	}
	detail := "trusted key " + key.KeyID
	if oldID != "" {
		detail = fmt.Sprintf("trusted key %s replaced by %s", oldID, key.KeyID)
	}
	if err := h.render(); err != nil {
		h.record(audit.Entry{Actor: op.Session.Username, Action: action, Outcome: audit.OutcomeError,
			Detail: detail + "; saved but YAML render failed: " + err.Error()})
		http.Error(w, "key saved but YAML render failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.record(audit.Entry{Actor: op.Session.Username, Action: action, Outcome: audit.OutcomeOK, Detail: detail})
	writeJSON(w, key)
}

func (h *platformHandlers) removeKey(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	id := strings.ToUpper(r.PathValue("id"))
	if err := h.rel.RemoveKey(id); err != nil {
		h.record(audit.Entry{Actor: op.Session.Username, Action: "platform.trust.remove", Outcome: audit.OutcomeError, Detail: err.Error()})
		http.Error(w, err.Error(), keyStatus(err))
		return
	}
	if err := h.render(); err != nil {
		h.record(audit.Entry{Actor: op.Session.Username, Action: "platform.trust.remove", Outcome: audit.OutcomeError,
			Detail: "removed trusted key " + id + "; removed but YAML render failed: " + err.Error()})
		http.Error(w, "key removed but YAML render failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.record(audit.Entry{Actor: op.Session.Username, Action: "platform.trust.remove", Outcome: audit.OutcomeOK, Detail: "removed trusted key " + id})
	w.WriteHeader(http.StatusNoContent)
}

// platformDoctorCheck warns while no real release key is trusted: with
// only the embedded placeholder (whose private half was discarded) or no
// key at all, no platform release can verify.
func platformDoctorCheck(st *store.Store) func() []doctor.Check {
	return func() []doctor.Check {
		const name = "release-trust"
		keys, err := release.NewStore(st).Keys()
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot read trusted release keys: " + err.Error()}}
		}
		real := 0
		for _, k := range keys {
			if !k.Placeholder {
				real++
			}
		}
		switch {
		case len(keys) == 0:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: "no trusted release keys: platform releases cannot verify; add one with homesteadctl updates trust add"}}
		case real == 0:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: "the only trusted release key is the embedded placeholder (its private half was discarded): no release can verify until you add a real key with homesteadctl updates trust add"}}
		}
		return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: fmt.Sprintf("%d trusted release key(s), %d real", len(keys), real)}}
	}
}
