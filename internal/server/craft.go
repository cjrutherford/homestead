package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/craft"
	"homestead/internal/layout"
	"homestead/internal/operation"
	"homestead/internal/release"
	"homestead/internal/store"
)

// Craft manifest and build-context locations under the catalog
// directory (S279). The packaging step copies images/craft there.
const (
	craftSubdir       = "craft"
	craftContextSub   = "context"
	craftManifestFile = "craft-manifest.yaml"
	craftSigSuffix    = ".minisig"
)

// MountCraft serves the craft build pipeline (S279, ADR-028): status,
// plan (verify only), build (verify-then-build as an operation), and the
// separate craft trust-key class. trustYAML is where the craft key
// configuration renders. It also wires the catalog seams so install and
// drift use the recorded local digests.
func (s *Server) MountCraft(st *store.Store, catalogDir, trustYAML string, builder craft.Builder) {
	keys := release.NewCraftStore(st)
	s.craft = &craft.Pipeline{
		Keys:       keys.PublicKeys,
		Store:      craft.NewStore(st),
		Builder:    builder,
		ContextDir: filepath.Join(catalogDir, craftSubdir, craftContextSub),
		StagingDir: filepath.Join(layout.LibDir.Path, "craft-staging"),
	}
	catalog.CraftChecker = s.craft.CheckImage
	catalog.CraftRecordedID = s.craft.RecordedID
	h := &craftHandlers{s: s, pipe: s.craft, keys: keys, audit: audit.New(st), runner: operation.NewRunner(st), st: st,
		catalogDir: catalogDir, trustYAML: trustYAML}
	if trustYAML != "" {
		if err := keys.RenderYAML(trustYAML); err != nil {
			s.log.Warn("craft trust yaml render failed", "error", err)
		}
	}
	s.mux.HandleFunc("GET /api/v1/craft", h.status)
	s.mux.HandleFunc("POST /api/v1/craft/plan", h.plan)
	s.mux.HandleFunc("POST /api/v1/craft/build", h.build)
	s.mux.HandleFunc("GET /api/v1/craft/apps", storeCraftAppsSource(st, catalogDir).handler())
	s.mux.HandleFunc("GET /api/v1/craft/trust-keys", h.listKeys)
	s.mux.HandleFunc("POST /api/v1/craft/trust-keys", h.addKey)
	s.mux.HandleFunc("PUT /api/v1/craft/trust-keys/{id}", h.replaceKey)
	s.mux.HandleFunc("DELETE /api/v1/craft/trust-keys/{id}", h.removeKey)
}

type craftHandlers struct {
	s          *Server
	pipe       *craft.Pipeline
	keys       *release.Store
	audit      *audit.Service
	runner     *operation.Runner
	st         *store.Store
	catalogDir string
	trustYAML  string
}

func (h *craftHandlers) record(entry audit.Entry) {
	if _, err := h.audit.Record(entry); err != nil {
		h.s.log.Error("craft audit record failed", "action", entry.Action, "error", err)
	}
}

func (h *craftHandlers) render() error {
	if h.trustYAML == "" {
		return nil
	}
	return h.keys.RenderYAML(h.trustYAML)
}

// craftImageView is a recorded build with its live comparison.
type craftImageView struct {
	craft.Image
	// Present is false when the tag is gone from the local Engine; Match
	// is true when the local image ID equals the recorded one.
	Present bool   `json:"present"`
	LiveID  string `json:"live_id"`
	Match   bool   `json:"match"`
}

type craftStatusView struct {
	Images    []craftImageView             `json:"images"`
	Verdicts  []craft.Verdict              `json:"verdicts"`
	Baselines map[string]release.Installed `json:"baselines"`
	// ManifestPath and SignaturePath are the defaults used when a
	// request names none; ContextDir is the verified build context.
	ManifestPath  string `json:"manifest_path"`
	SignaturePath string `json:"signature_path"`
	ContextDir    string `json:"context_dir"`
}

func (h *craftHandlers) defaults() (manifest, sig string) {
	manifest = filepath.Join(h.catalogDir, craftSubdir, craftManifestFile)
	return manifest, manifest + craftSigSuffix
}

func (h *craftHandlers) status(w http.ResponseWriter, r *http.Request) {
	images, err := h.pipe.Store.Images()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verdicts, err := h.pipe.Store.Verdicts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view := craftStatusView{Images: []craftImageView{}, Verdicts: verdicts, Baselines: map[string]release.Installed{}, ContextDir: h.pipe.ContextDir}
	view.ManifestPath, view.SignaturePath = h.defaults()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	for _, img := range images {
		iv := craftImageView{Image: img}
		if id, err := h.pipe.Builder.ImageID(ctx, img.Tag); err == nil {
			iv.Present, iv.LiveID, iv.Match = true, id, id == img.ImageID
		}
		view.Images = append(view.Images, iv)
		if base, err := h.pipe.Store.Baseline(img.App); err == nil {
			view.Baselines[img.App] = base
		}
	}
	for _, v := range verdicts {
		if _, ok := view.Baselines[v.App]; !ok {
			if base, err := h.pipe.Store.Baseline(v.App); err == nil && base.Counter > 0 {
				view.Baselines[v.App] = base
			}
		}
	}
	writeJSON(w, view)
}

type craftBody struct {
	App        string `json:"app"`
	Version    string `json:"version"`
	Manifest   string `json:"manifest"`
	Signature  string `json:"signature"`
	Artifact   string `json:"artifact"`
	AllowOlder bool   `json:"allow_older"`
}

func (h *craftHandlers) request(b craftBody, actor string) craft.Request {
	manifest, sig := h.defaults()
	if b.Manifest != "" {
		manifest = b.Manifest
		sig = b.Signature
		if sig == "" {
			sig = manifest + craftSigSuffix
		}
	} else if b.Signature != "" {
		sig = b.Signature
	}
	return craft.Request{App: b.App, Version: b.Version, ManifestPath: manifest, SignaturePath: sig,
		ArtifactPath: b.Artifact, AllowOlder: b.AllowOlder, Actor: actor}
}

// craftPlanResponse is the shared verdict shape for plan and refused
// builds: what was checked, and the saved verdict.
type craftPlanResponse struct {
	Plan    craft.Plan    `json:"plan"`
	Verdict craft.Verdict `json:"verdict"`
}

// refusalStatus maps a check failure to an HTTP status.
func refusalStatus(err error) int {
	var ce *craft.CheckError
	if errors.As(err, &ce) && ce.Check == craft.CheckInput {
		return http.StatusBadRequest
	}
	return http.StatusUnprocessableEntity
}

func (h *craftHandlers) overrideEntry(actor, action string, plan craft.Plan) audit.Entry {
	return audit.Entry{Actor: actor, Action: action, Outcome: audit.OutcomeOK, Override: true,
		Detail: fmt.Sprintf("%s %s", plan.App, plan.Version),
		OverrideReason: fmt.Sprintf("owner allowed older craft manifest: recorded %s, manifest %s counter %d",
			plan.Baseline, plan.Version, plan.Counter)}
}

func (h *craftHandlers) plan(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body craftBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := h.request(body, op.Session.Username)
	plan, verdict, err := h.pipe.Check(r.Context(), req)
	planJSON, _ := json.Marshal(map[string]any{"app": body.App, "manifest": req.ManifestPath, "signature": req.SignaturePath,
		"artifact": body.Artifact, "allow_older": body.AllowOlder})
	entry := audit.Entry{Actor: op.Session.Username, Action: "craft.plan", Plan: string(planJSON)}
	switch {
	case err != nil && refusalStatus(err) == http.StatusBadRequest:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		entry.Outcome, entry.Detail = audit.OutcomeError, err.Error()
	default:
		entry.Outcome, entry.Detail = audit.OutcomeOK, verdict.Detail
		if plan.Override {
			entry.Override, entry.OverrideReason = true, h.overrideEntry("", "", plan).OverrideReason
		}
	}
	h.record(entry)
	// A refusal is a verdict, not a request error: 200 with state failed.
	writeJSON(w, craftPlanResponse{Plan: plan, Verdict: verdict})
}

func (h *craftHandlers) build(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body craftBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := h.request(body, op.Session.Username)
	// Refuse early and loudly; the operation re-verifies everything.
	plan, _, err := h.pipe.Check(r.Context(), req)
	if err != nil {
		status := refusalStatus(err)
		var ce *craft.CheckError
		if errors.As(err, &ce) && ce.Check == craft.CheckCounter {
			w.Header().Set(ErrorCodeHeader, ErrorCodeOlderRelease)
		}
		h.record(audit.Entry{Actor: op.Session.Username, Action: "craft.build", Outcome: audit.OutcomeError, Detail: "build refused: " + err.Error()})
		http.Error(w, err.Error(), status)
		return
	}
	def, err := h.pipe.Definition(req)
	if err != nil {
		http.Error(w, err.Error(), refusalStatus(err))
		return
	}
	started, err := h.runner.Start(r.Context(), def, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if plan.Override {
		h.record(h.overrideEntry(op.Session.Username, "craft.build.override", plan))
	}
	h.s.auditOperation(h.st, h.runner, started.ID, op.Session.Username, "craft.build",
		fmt.Sprintf("%s %s from manifest counter %d", plan.App, plan.Version, plan.Counter))
	writeJSON(w, map[string]any{"id": started.ID})
}

func (h *craftHandlers) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.keys.Keys()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if keys == nil {
		keys = []release.TrustKey{}
	}
	writeJSON(w, keys)
}

func (h *craftHandlers) addKey(w http.ResponseWriter, r *http.Request) { h.saveKey(w, r, "") }

func (h *craftHandlers) replaceKey(w http.ResponseWriter, r *http.Request) {
	h.saveKey(w, r, strings.ToUpper(r.PathValue("id")))
}

func (h *craftHandlers) saveKey(w http.ResponseWriter, r *http.Request, oldID string) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var body keyBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	action := "craft.trust.add"
	var key release.TrustKey
	var err error
	if oldID != "" {
		action = "craft.trust.replace"
		key, err = h.keys.ReplaceKey(oldID, body.PublicKey, body.Label, op.Session.Username)
	} else {
		key, err = h.keys.AddKey(body.PublicKey, body.Label, op.Session.Username)
	}
	if err != nil {
		h.record(audit.Entry{Actor: op.Session.Username, Action: action, Outcome: audit.OutcomeError, Detail: err.Error()})
		http.Error(w, err.Error(), keyStatus(err))
		return
	}
	detail := "trusted craft key " + key.KeyID
	if oldID != "" {
		detail = fmt.Sprintf("trusted craft key %s replaced by %s", oldID, key.KeyID)
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

func (h *craftHandlers) removeKey(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	id := strings.ToUpper(r.PathValue("id"))
	if err := h.keys.RemoveKey(id); err != nil {
		h.record(audit.Entry{Actor: op.Session.Username, Action: "craft.trust.remove", Outcome: audit.OutcomeError, Detail: err.Error()})
		http.Error(w, err.Error(), keyStatus(err))
		return
	}
	if err := h.render(); err != nil {
		h.record(audit.Entry{Actor: op.Session.Username, Action: "craft.trust.remove", Outcome: audit.OutcomeError,
			Detail: "removed craft key " + id + "; removed but YAML render failed: " + err.Error()})
		http.Error(w, "key removed but YAML render failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.record(audit.Entry{Actor: op.Session.Username, Action: "craft.trust.remove", Outcome: audit.OutcomeOK, Detail: "removed craft key " + id})
	w.WriteHeader(http.StatusNoContent)
}
