package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"homestead/internal/audit"
	"homestead/internal/debianupdate"
	"homestead/internal/doctor"
	"homestead/internal/layout"
	"homestead/internal/operation"
	"homestead/internal/platformapply"
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
	h := &platformHandlers{s: s, rel: release.NewStore(st), audit: audit.New(st), trustYAML: trustYAML,
		marks: platformapply.NewStore(st), debian: debianupdate.Manager{}}
	h.applier = &platformapply.Applier{
		Releases: h.rel, Markers: h.marks, Runner: operation.NewRunner(st), Ops: operation.New(st), Audit: h.audit,
		Systemd: platformapply.ExecSystemd{}, BinDir: PlatformBinDir,
		Debian:     h.debian,
		StagingDir: filepath.Join(layout.LibDir.Path, "staging"), Unit: PlatformUnit, ApplyUnit: PlatformApplyUnit,
		Running: version.Version, Listen: s.addr, SocketPath: s.SocketPath,
	}
	if trustYAML != "" {
		if err := h.render(); err != nil {
			s.log.Warn("platform trust yaml render failed", "error", err)
		}
	}
	s.mux.HandleFunc("GET /api/v1/platform/updates", h.plan)
	s.mux.HandleFunc("POST /api/v1/platform/updates/verify", h.verify)
	s.mux.HandleFunc("POST /api/v1/platform/updates/apply", h.apply)
	s.mux.HandleFunc("POST /api/v1/platform/updates/clear-apply", h.clearApply)
	s.mux.HandleFunc("POST /api/v1/platform/updates/rollback", h.rollback)
	s.mux.HandleFunc("GET /api/v1/platform/trust-keys", h.listKeys)
	s.mux.HandleFunc("POST /api/v1/platform/trust-keys", h.addKey)
	s.mux.HandleFunc("PUT /api/v1/platform/trust-keys/{id}", h.replaceKey)
	s.mux.HandleFunc("DELETE /api/v1/platform/trust-keys/{id}", h.removeKey)
}

// Production locations for platform apply (S265, ADR-027): the
// installed binaries (read-only to the daemon; only the apply unit
// writes them), the daemon unit that restarts, and the privileged apply
// unit the daemon starts.
const (
	PlatformBinDir    = "/usr/local/bin"
	PlatformUnit      = "homestead-server.service"
	PlatformApplyUnit = "homestead-apply.service"
)

type platformHandlers struct {
	s         *Server
	rel       *release.Store
	audit     *audit.Service
	debian    debianupdate.Manager
	trustYAML string
	marks     *platformapply.Store
	applier   *platformapply.Applier
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
	// Previous names the binaries kept before the last replacement
	// (manual restore, S273 rollback); Pending is the apply awaiting
	// restart finalization, if any.
	Previous map[string]platformapply.Previous `json:"previous"`
	Pending  *platformapply.Marker             `json:"pending"`
}

func (h *platformHandlers) plan(w http.ResponseWriter, r *http.Request) {
	releases, err := h.rel.Releases()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	packageInstall, packageErr := h.debian.Installed(r.Context())
	packageManaged := packageErr == nil
	if packageErr != nil && !errors.Is(packageErr, debianupdate.ErrNotInstalled) {
		http.Error(w, packageErr.Error(), http.StatusInternalServerError)
		return
	}
	running := map[string]string{}
	installed := map[string]release.Installed{}
	for _, component := range []string{update.ComponentDaemon, update.ComponentCLI, update.ComponentDeb} {
		in, err := h.rel.Installed(component)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		installed[component] = in
	}
	planRunning := map[string]string{}
	if packageManaged {
		packageVersion, err := debianupdate.ReleaseVersion(packageInstall.Version)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if installed[update.ComponentDeb].Version == "" {
			if _, err := h.rel.SeedInstalled(update.ComponentDeb, packageVersion, 0); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			installed[update.ComponentDeb], _ = h.rel.Installed(update.ComponentDeb)
		}
		running[update.ComponentDeb], planRunning[update.ComponentDeb] = packageVersion, packageVersion
	} else {
		running[update.ComponentDaemon] = version.Version
		planRunning[update.ComponentDaemon] = version.Version
		running[update.ComponentCLI] = installed[update.ComponentCLI].Version
		if running[update.ComponentCLI] == "" {
			running[update.ComponentCLI] = unrecordedVersion
		} else {
			planRunning[update.ComponentCLI] = running[update.ComponentCLI]
		}
	}
	views := make([]releaseView, 0, len(releases))
	refused := []releaseView{}
	var desired []update.PlatformRelease
	for _, rel := range releases {
		view := releaseView{Release: rel}
		modeMismatch := (packageManaged && rel.Component != update.ComponentDeb) || (!packageManaged && rel.Component == update.ComponentDeb)
		if modeMismatch {
			view.PlanError = "refused: installation mode mismatch; use the " + map[bool]string{true: "homestead-deb package", false: "source binary"}[packageManaged] + " update component for this appliance"
			refused = append(refused, view)
		} else if rel.State != release.StateVerified {
			// Failed, rollback-refused, and unverified releases are shown,
			// never planned.
			view.PlanError = "refused: " + rel.State
			if rel.Detail != "" {
				view.PlanError += ": " + rel.Detail
			}
			refused = append(refused, view)
		} else if rel.Version != "" {
			candidate := update.PlatformRelease{Component: rel.Component, Version: rel.Version, Verification: rel.State}
			if _, known := planRunning[rel.Component]; !known {
				view.PlanError = "installed version is unrecorded, so no move can be planned; apply still works"
			} else if _, err := update.PlanPlatform(planRunning, []update.PlatformRelease{candidate}); err != nil {
				view.PlanError = err.Error()
			} else {
				desired = append(desired, candidate)
			}
		}
		views = append(views, view)
	}
	plan, err := update.PlanPlatform(planRunning, desired)
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
	previous, err := h.marks.AllPrevious()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view := platformPlanView{Running: running, Plan: plan, Releases: views, Refused: refused, Installed: installed, Previous: previous}
	if marker, ok, err := h.marks.Pending(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if ok {
		view.Pending = &marker
	}
	writeJSON(w, view)
}

// clearApply is the owner's way out of a stale apply marker (S265): it
// marks the operation failed, clears the marker, and audits. It refuses
// while the apply unit is running.
func (h *platformHandlers) clearApply(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	m, err := h.applier.ClearApply(r.Context(), op.Session.Username)
	switch {
	case errors.Is(err, platformapply.ErrNoPending):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, platformapply.ErrBusy):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		writeJSON(w, m)
	}
}

// unrecordedVersion is what the plan reports for a component with no
// installed record.
const unrecordedVersion = "unrecorded"

type platformRollbackBody struct {
	Component  string `json:"component"`
	AllowOlder bool   `json:"allow_older"`
}

// rollback starts a manual rollback to the previous binary an earlier
// apply recorded (S273, ADR-027). It runs through the same privileged
// apply unit, which re-verifies the previous binary's digest. Rolling
// back to an older counter is the explicit, audited owner override
// (ADR-025).
func (h *platformHandlers) rollback(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body platformRollbackBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	started, err := h.applier.Rollback(r.Context(), platformapply.RollbackRequest{
		Component: body.Component, Actor: op.Session.Username, AllowOlder: body.AllowOlder,
	})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, platformapply.ErrInput):
			status = http.StatusBadRequest
		case errors.Is(err, platformapply.ErrOlder), errors.Is(err, platformapply.ErrDigest), errors.Is(err, platformapply.ErrPrevious),
			errors.Is(err, platformapply.ErrCounterUnknown), errors.Is(err, platformapply.ErrCounterConflict):
			status = http.StatusUnprocessableEntity
		case errors.Is(err, platformapply.ErrNoPrevious), errors.Is(err, platformapply.ErrCurrent), errors.Is(err, platformapply.ErrBusy):
			status = http.StatusConflict
		}
		if errors.Is(err, platformapply.ErrOlder) {
			w.Header().Set(ErrorCodeHeader, ErrorCodeOlderRelease)
		}
		if status != http.StatusInternalServerError {
			h.record(audit.Entry{Actor: op.Session.Username, Action: platformapply.ActionRollback, Outcome: audit.OutcomeError,
				Detail: "rollback refused: " + err.Error()})
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, map[string]any{"id": started.ID})
}

type platformApplyBody struct {
	Component  string `json:"component"`
	Artifact   string `json:"artifact"`
	Checksums  string `json:"checksums"`
	Signature  string `json:"signature"`
	AllowOlder bool   `json:"allow_older"`
	PlanDigest string `json:"plan_digest"`
}

// apply starts a platform update operation for a verified release
// (S265, ADR-027). The paths are the files the release was verified
// from; the daemon verifies them again and stages copies, and the
// privileged apply unit it starts verifies the copies once more before
// replacing anything. The operation is finalized by the apply unit.
func (h *platformHandlers) apply(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body platformApplyBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	started, err := h.applier.Start(r.Context(), platformapply.Request{
		Component: body.Component, Artifact: body.Artifact, Checksums: body.Checksums, Signature: body.Signature,
		Actor: op.Session.Username, AllowOlder: body.AllowOlder, PlanDigest: body.PlanDigest,
	})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, platformapply.ErrInput):
			status = http.StatusBadRequest
		case errors.Is(err, platformapply.ErrNotVerified), errors.Is(err, platformapply.ErrOlder), errors.Is(err, platformapply.ErrDigest),
			errors.Is(err, platformapply.ErrMode), errors.Is(err, debianupdate.ErrPlanChanged):
			status = http.StatusUnprocessableEntity
		case errors.Is(err, platformapply.ErrCurrent), errors.Is(err, platformapply.ErrBusy):
			status = http.StatusConflict
		}
		if errors.Is(err, platformapply.ErrOlder) {
			w.Header().Set(ErrorCodeHeader, ErrorCodeOlderRelease)
		}
		if status != http.StatusInternalServerError {
			h.record(audit.Entry{Actor: op.Session.Username, Action: platformapply.ActionApply, Outcome: audit.OutcomeError,
				Detail: "apply refused: " + err.Error()})
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, map[string]any{"id": started.ID})
}

type verifyBody struct {
	Component  string `json:"component"`
	Artifact   string `json:"artifact"`
	Checksums  string `json:"checksums"`
	Signature  string `json:"signature"`
	AllowOlder bool   `json:"allow_older"`
}

type platformVerifyResult struct {
	release.Release
	PackagePlan *debianupdate.Preview `json:"package_plan,omitempty"`
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
	packageInstall, packageErr := h.debian.Installed(r.Context())
	packageManaged := packageErr == nil
	if packageErr != nil && !errors.Is(packageErr, debianupdate.ErrNotInstalled) {
		http.Error(w, packageErr.Error(), http.StatusInternalServerError)
		return
	}
	if (body.Component == update.ComponentDeb) != packageManaged {
		http.Error(w, "update component does not match the appliance installation mode", http.StatusConflict)
		return
	}
	if body.Component == update.ComponentDeb && body.AllowOlder {
		http.Error(w, "Debian package updates are forward-only", http.StatusUnprocessableEntity)
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
	if body.Component == update.ComponentDeb && installed.Version == "" {
		packageVersion, err := debianupdate.ReleaseVersion(packageInstall.Version)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := h.rel.SeedInstalled(body.Component, packageVersion, 0); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		installed, err = h.rel.Installed(body.Component)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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
	var packagePlan *debianupdate.Preview
	if verr == nil && body.Component == update.ComponentDeb {
		preview, err := h.debian.Preview(r.Context(), body.Artifact, res.Version)
		if err != nil {
			h.record(audit.Entry{Actor: op.Session.Username, Action: "platform.verify", Outcome: audit.OutcomeError,
				Detail: "Debian package preview refused: " + err.Error()})
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if preview.ArtifactSHA != res.SHA256 {
			http.Error(w, "package bytes changed during verification; retry verification", http.StatusUnprocessableEntity)
			return
		}
		packagePlan = &preview
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
		// definition older than a record that exists). Only the daemon
		// knows its own version; the CLI's is not known to it.
		if body.Component == update.ComponentDaemon && installed.Counter == 0 && res.Version == version.Version {
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
	writeJSON(w, platformVerifyResult{Release: rec, PackagePlan: packagePlan})
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

// ApplyStaleAfter is how long an apply marker may stand before doctor
// warns: well past the apply unit's health timeout.
const ApplyStaleAfter = 15 * time.Minute

// platformApplyDoctorCheck reports a stale apply marker (the apply unit
// died) and fails while the latest platform update failed, so a failed
// apply stays visible until a later one succeeds.
func platformApplyDoctorCheck(st *store.Store, now func() time.Time) func() []doctor.Check {
	return func() []doctor.Check {
		const name = "platform-apply"
		var checks []doctor.Check
		m, pending, err := platformapply.NewStore(st).Pending()
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot read the apply marker: " + err.Error()}}
		}
		if pending && now().Sub(m.CreatedAt) > ApplyStaleAfter {
			checks = append(checks, doctor.Check{Name: name, Status: doctor.StatusWarn,
				Detail: fmt.Sprintf("apply of %s %s (operation %d) has been pending since %s; if %s is not running, clear it with homesteadctl updates platform clear-apply",
					m.Component, m.ToVersion, m.OpID, m.CreatedAt.Format(time.RFC3339), PlatformApplyUnit)})
		}
		ops, err := operation.New(st).List(0)
		if err != nil {
			return append(checks, doctor.Check{Name: name, Status: doctor.StatusWarn, Detail: "cannot read operations: " + err.Error()})
		}
		for _, op := range ops { // newest first
			if op.Type != platformapply.OperationType || !operation.Terminal(op.Status) {
				continue
			}
			if op.Status == operation.StatusFailed {
				checks = append(checks, doctor.Check{Name: name, Status: doctor.StatusFail,
					Detail: fmt.Sprintf("the latest platform update (operation %d) failed: %s", op.ID, op.Error)})
			}
			break
		}
		if len(checks) == 0 {
			checks = append(checks, doctor.Check{Name: name, Status: doctor.StatusOK, Detail: "no failed or stale platform update"})
		}
		return checks
	}
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
