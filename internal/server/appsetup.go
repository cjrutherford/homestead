package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/people"
	"homestead/internal/store"
)

// App setup (S324, ADR-048 §1–§3): the steps after a catalog install that
// the owner approved with it run on the appliance, as one app.setup
// operation, so closing the wizard, the tab or the browser changes
// nothing. In order: the app's address on the network, its public
// address by the approved digest, sign-in with Authentik by the approved
// digest, and the people chosen to grant. Each step is planned again and
// compared with its approval first; a changed plan or a failure stops the
// setup there, and the owner resumes it with a fresh approval or skips it.

// Setup step names, in the order they run.
const (
	setupAddress = "address"
	setupPublish = "publish"
	setupSignIn  = "sign-in"
	setupPeople  = "people"
	// setupFilesLink, "nextcloud-link" (S326), runs after the address
	// for a craft app and last for Nextcloud: see craftlink.go.
)

// Step states.
const (
	stepWaiting = "waiting"
	stepRunning = "running"
	stepDone    = "done"
	stepChanged = "changed"
	stepFailed  = "failed"
	stepSkipped = "skipped"
)

// Setup statuses. Waiting is the install still running.
const (
	setupNone     = "none"
	setupWaiting  = "waiting"
	setupRunning  = "running"
	setupStopped  = "stopped"
	setupFinished = "done"
	setupEnded    = "skipped"
)

// setupStepLabels are the operation's step names, as the owner reads them.
var setupStepLabels = map[string]string{
	setupAddress: "address on your network",
	setupPublish: "public address",
	setupSignIn:  "sign-in with Authentik",
	setupPeople:  "people who can sign in",
	// S326: a craft app's files in Nextcloud.
	setupFilesLink: "link to Nextcloud for files",
}

// setupApproval is what the install request carries (ADR-048 §1): the
// approved public address request and digest, the approved sign-in
// digest and the people to grant. Its presence alone asks for the
// address step, which the install digest already approves.
type setupApproval struct {
	Public *setupPublicApproval `json:"public,omitempty"`
	SignIn *setupDigest         `json:"sign_in,omitempty"`
	People []int64              `json:"people,omitempty"`
	// FilesLink approves the Nextcloud link for craft files the install
	// review listed (S326, ADR-048 §4), by its digest.
	FilesLink *setupDigest `json:"files_link,omitempty"`
}

type setupPublicApproval struct {
	Hostname string `json:"hostname,omitempty"`
	Route    string `json:"route"`
	Digest   string `json:"digest"`
}

type setupDigest struct {
	Digest string `json:"digest"`
}

// setupStep is one step's approval and state.
type setupStep struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Digest string `json:"digest"`
	Reason string `json:"reason,omitempty"`
	// Operation is the step's own operation (the edge reconcile, the
	// publish, the sign-in), audited as it is outside the setup.
	Operation int64 `json:"operation,omitempty"`
}

// setupRecord is one app's setup, kept in app_setup as JSON. It holds
// digests, names and ids only: never a token or secret.
type setupRecord struct {
	App string `json:"app"`
	// Status is waiting, running, stopped, done or skipped.
	Status string `json:"status"`
	// Install is the install operation; Operation the latest setup run.
	Install   int64       `json:"install_operation"`
	Operation int64       `json:"operation,omitempty"`
	Steps     []setupStep `json:"steps"`
	// Reason says where and why a stopped setup stopped.
	Reason string `json:"reason,omitempty"`
	Actor  string `json:"actor"`
	// Routes are the local routes the reviewed install plan gives the app.
	Routes []docker.Route       `json:"routes"`
	Public *setupPublicApproval `json:"public,omitempty"`
	People []int64              `json:"people,omitempty"`
	// FilesApps are the craft apps the nextcloud-link step links (S326).
	FilesApps []string `json:"files_apps,omitempty"`
	Digest    string   `json:"install_digest,omitempty"`
	At        string   `json:"updated_at,omitempty"`
}

func (r *setupRecord) step(name string) *setupStep {
	for i := range r.Steps {
		if r.Steps[i].Name == name {
			return &r.Steps[i]
		}
	}
	return nil
}

// stopped is the step a stopped setup stopped at.
func (r *setupRecord) stopped() *setupStep {
	for i := range r.Steps {
		if s := r.Steps[i].State; s == stepChanged || s == stepFailed {
			return &r.Steps[i]
		}
	}
	return nil
}

// unfinished says the setup still needs the owner or is running.
func (r setupRecord) unfinished() bool {
	return r.Status == setupWaiting || r.Status == setupRunning || r.Status == setupStopped
}

// appSetup runs and records app setups.
type appSetup struct {
	s          *Server
	st         *store.Store
	catalogDir string
	local      *cert.LocalTLS
	runner     *operation.Runner
	now        func() time.Time
	// startFn and reviewFn are startStep and review, stubbed in tests.
	startFn  func(ctx context.Context, rec setupRecord, name, approved, token string) (int64, error)
	reviewFn func(ctx context.Context, rec setupRecord, name string) stepReview
	// linkFn links this appliance's Nextcloud (the craft files manager's
	// LinkAppliance), stubbed in tests; linkWait and linkRetry bound the
	// wait for a Nextcloud still setting itself up (S326).
	linkFn    func(ctx context.Context, root, actor string) error
	linkWait  time.Duration
	linkRetry time.Duration
	// mu serializes reads and writes of setup records, so a setup run
	// never reads its record before the launch that started it saved it.
	mu sync.Mutex
}

// MountAppSetup registers the app setup routes (S324, ADR-048): read an
// app's setup, resume a stopped one with a fresh approval of the stopped
// step's plan, or skip it. Mount it after MountSSO and MountAppPublic.
func (s *Server) MountAppSetup(st *store.Store, catalogDir string, local *cert.LocalTLS) {
	a := &appSetup{s: s, st: st, catalogDir: catalogDir, local: local, runner: operation.NewRunner(st), now: time.Now,
		linkWait: 5 * time.Minute, linkRetry: 5 * time.Second}
	s.appSetup = a
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/setup", a.status)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/setup/resume", a.resume)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/setup/skip", a.skip)
}

// ---- records ----

func (a *appSetup) load(app string) (setupRecord, bool, error) {
	var encoded string
	err := a.st.DB().QueryRow(`SELECT record FROM app_setup WHERE app = ?`, app).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return setupRecord{}, false, nil
	}
	if err != nil {
		return setupRecord{}, false, fmt.Errorf("app setup: read %s: %w", app, err)
	}
	var rec setupRecord
	if err := json.Unmarshal([]byte(encoded), &rec); err != nil {
		return setupRecord{}, false, fmt.Errorf("app setup: decode %s: %w", app, err)
	}
	return rec, true, nil
}

func (a *appSetup) save(rec *setupRecord) error {
	rec.At = a.now().UTC().Format(time.RFC3339)
	encoded, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = a.st.DB().Exec(`INSERT INTO app_setup (app, status, record, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(app) DO UPDATE SET status = excluded.status, record = excluded.record, updated_at = excluded.updated_at`,
		rec.App, rec.Status, string(encoded), rec.At)
	if err != nil {
		return fmt.Errorf("app setup: record %s: %w", rec.App, err)
	}
	return nil
}

func (a *appSetup) forget(app string) {
	_, _ = a.st.DB().Exec(`DELETE FROM app_setup WHERE app = ?`, app)
}

// forgetFinished drops a setup that is not running, before a new
// install of the app or after its uninstall: what is left of an old one
// belongs to an app that is gone.
func (a *appSetup) forgetFinished(app string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec, ok, _ := a.load(app); ok && rec.Status != setupRunning {
		a.forget(app)
	}
}

// all lists every recorded setup.
func (a *appSetup) all() ([]setupRecord, error) {
	rows, err := a.st.DB().Query(`SELECT app FROM app_setup ORDER BY app`)
	if err != nil {
		return nil, err
	}
	var apps []string
	for rows.Next() {
		var app string
		if err := rows.Scan(&app); err != nil {
			rows.Close()
			return nil, err
		}
		apps = append(apps, app)
	}
	rows.Close()
	var out []setupRecord
	for _, app := range apps {
		rec, ok, err := a.current(app)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

// current reads a setup and settles what a dead run left behind: a run
// whose operation ended without finishing the step (a restart, a cancel)
// is stopped there, and a setup whose install failed is forgotten.
func (a *appSetup) current(app string) (setupRecord, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok, err := a.load(app)
	if err != nil || !ok {
		return rec, ok, err
	}
	ops := operation.New(a.st)
	switch rec.Status {
	case setupWaiting:
		if rec.Install == 0 {
			break
		}
		op, err := ops.Get(rec.Install)
		if err == nil && operation.Terminal(op.Status) && op.Status != operation.StatusSucceeded {
			a.forget(app)
			return setupRecord{}, false, nil
		}
	case setupRunning:
		if rec.Operation == 0 {
			break
		}
		op, err := ops.Get(rec.Operation)
		if err == nil && operation.Terminal(op.Status) {
			reason := "The setup stopped before this step finished"
			if op.Error != "" {
				reason += ": " + op.Error
			}
			for i := range rec.Steps {
				if rec.Steps[i].State == stepRunning || (rec.Steps[i].State == stepWaiting && rec.stopped() == nil) {
					rec.Steps[i].State, rec.Steps[i].Reason = stepFailed, reason+"."
					break
				}
			}
			if rec.stopped() == nil {
				rec.Status = setupFinished
			} else {
				rec.Status, rec.Reason = setupStopped, stopReason(rec.stopped())
			}
			if err := a.save(&rec); err != nil {
				return rec, true, err
			}
		}
	}
	return rec, true, nil
}

// ---- approval at install ----

func routesDigest(routes []docker.Route) string {
	data, _ := json.Marshal(routes)
	sum := sha256.Sum256(append([]byte("routes="), data...))
	return hex.EncodeToString(sum[:])
}

func peopleDigest(app string, ids []int64) string {
	sorted := append([]int64(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	data, _ := json.Marshal(sorted)
	sum := sha256.Sum256([]byte("people=" + app + "=" + string(data)))
	return hex.EncodeToString(sum[:])
}

// validatePeople checks the whole selection before starting any grants.
// Grant still rechecks each person because their state can change later.
func (a *appSetup) validatePeople(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if a.s.people == nil {
		return errors.New("people are not managed on this appliance")
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return fmt.Errorf("invalid or duplicate person %d", id)
		}
		seen[id] = true
		active, _, err := a.s.people.AppGrants(id)
		if errors.Is(err, people.ErrNotFound) || (err == nil && !active) {
			return fmt.Errorf("person %d is missing or disabled", id)
		}
		if err != nil {
			return fmt.Errorf("check person %d: %w", id, err)
		}
	}
	return nil
}

// prepare builds the setup record for an install from the owner's
// approvals and the reviewed install plan. Its steps are the approved
// ones only. A sign-in approval needs the kept Authentik API token (a
// token is never stored); without one the step is recorded as skipped,
// with the way to set sign-in up later, and so are the people.
func (a *appSetup) prepare(manifest catalog.Manifest, plan catalog.InstallPlan, approval setupApproval, actor string) (setupRecord, error) {
	rec := setupRecord{App: manifest.Name, Status: setupWaiting, Actor: actor, Routes: []docker.Route{}, Digest: installDigest(plan)}
	if len(approval.People) > 0 && approval.SignIn == nil {
		return rec, errors.New("people require an approved sign-in plan")
	}
	for _, r := range plan.Routes {
		if r.Exposure != docker.ExposurePublic {
			rec.Routes = append(rec.Routes, r)
		}
	}
	if len(rec.Routes) > 0 {
		rec.Steps = append(rec.Steps, setupStep{Name: setupAddress, State: stepWaiting, Digest: routesDigest(rec.Routes)})
	}
	if p := approval.Public; p != nil {
		if strings.TrimSpace(p.Digest) == "" || strings.TrimSpace(p.Route) == "" {
			return rec, errors.New("an approved public address names its route and digest")
		}
		pub := *p
		pub.Hostname = strings.ToLower(strings.TrimSpace(pub.Hostname))
		pub.Route = strings.ToLower(strings.TrimSpace(pub.Route))
		rec.Public = &pub
		rec.Steps = append(rec.Steps, setupStep{Name: setupPublish, State: stepWaiting, Digest: pub.Digest})
	}
	if si := approval.SignIn; si != nil {
		if strings.TrimSpace(si.Digest) == "" {
			return rec, errors.New("an approved sign-in names its digest")
		}
		if manifest.SSO.OIDC == nil {
			return rec, fmt.Errorf("%s has no sign-in with Authentik", manifest.Title)
		}
		step := setupStep{Name: setupSignIn, State: stepWaiting, Digest: si.Digest}
		kept := a.s.appSignIn != nil && a.s.appSignIn.tokenKept()
		if !kept {
			step.State = stepSkipped
			step.Reason = "Homestead keeps no Authentik API token, so sign-in was not set up with the install. Set it up under Manage on the catalog page, or homesteadctl apps sign-in apply " + manifest.Name + "."
		}
		rec.Steps = append(rec.Steps, step)
		if len(approval.People) > 0 {
			if kept {
				if err := a.validatePeople(approval.People); err != nil {
					return rec, err
				}
			}
			rec.People = append([]int64(nil), approval.People...)
			ps := setupStep{Name: setupPeople, State: stepWaiting, Digest: peopleDigest(manifest.Name, rec.People)}
			if !kept {
				ps.State, ps.Reason = stepSkipped, "Sign-in was not set up, so no one was granted the app. Grant people on the People page once sign-in is on."
			}
			rec.Steps = append(rec.Steps, ps)
		}
	}
	if fl := approval.FilesLink; fl != nil {
		step, apps, err := a.approveFilesLink(manifest, fl.Digest)
		if err != nil {
			return rec, err
		}
		rec.FilesApps = apps
		if manifest.Name == nextcloudPackage || len(rec.Steps) == 0 || rec.Steps[0].Name != setupAddress {
			// Nextcloud links the craft apps last, once it is set up.
			rec.Steps = append(rec.Steps, step)
		} else {
			// A craft app is linked once it has its address.
			rec.Steps = append(rec.Steps[:1], append([]setupStep{step}, rec.Steps[1:]...)...)
		}
	}
	return rec, nil
}

// attach records the setup and adds the install operation's last step,
// which starts the setup once everything before it succeeded. An install
// that ends without reaching that step forgets the setup.
func (a *appSetup) attach(rec setupRecord, def *operation.Definition) error {
	a.mu.Lock()
	err := a.save(&rec)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	app := rec.App
	started := false
	def.Steps = append(def.Steps, operation.StepDef{Name: "start the approved setup", Run: func(ctx context.Context) error {
		started = true
		return a.begin(ctx, app)
	}})
	prev := def.Cleanup
	def.Cleanup = func() {
		if prev != nil {
			prev()
		}
		if !started {
			a.mu.Lock()
			if r, ok, _ := a.load(app); ok && r.Status == setupWaiting {
				a.forget(app)
			}
			a.mu.Unlock()
		}
	}
	return nil
}

// installed records the install operation id once it started.
func (a *appSetup) installed(app string, id int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec, ok, _ := a.load(app); ok && rec.Status == setupWaiting && rec.Install == 0 {
		rec.Install = id
		_ = a.save(&rec)
	}
}

// begin starts the setup after the install succeeded. It never fails
// the install: a setup that cannot start is recorded as stopped.
func (a *appSetup) begin(ctx context.Context, app string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok, err := a.load(app)
	if err != nil || !ok || rec.Status != setupWaiting {
		return nil
	}
	if _, err := a.launch(ctx, &rec, ""); err != nil {
		a.s.log.Warn("app setup did not start", "app", app, "error", err)
	}
	return nil
}

// launch starts a setup run over the steps not done or skipped, and
// audits it as app.setup with the install and the approved digests.
// a.mu is held. token is an Authentik API token for this run only.
func (a *appSetup) launch(ctx context.Context, rec *setupRecord, token string) (int64, error) {
	def := operation.Definition{Type: "app.setup"}
	app := rec.App
	for _, step := range rec.Steps {
		if step.State == stepDone || step.State == stepSkipped {
			continue
		}
		name := step.Name
		def.Steps = append(def.Steps, operation.StepDef{Name: setupStepLabels[name], Run: func(ctx context.Context) error {
			return a.runStep(ctx, app, name, token)
		}})
	}
	if len(def.Steps) == 0 {
		rec.Status, rec.Reason = setupFinished, ""
		return 0, a.save(rec)
	}
	op, err := a.runner.Start(ctx, def, "")
	if err != nil {
		rec.Status, rec.Reason = setupStopped, "The setup could not start: "+err.Error()
		_ = a.save(rec)
		return 0, err
	}
	rec.Status, rec.Reason, rec.Operation = setupRunning, "", op.ID
	if err := a.save(rec); err != nil {
		return op.ID, err
	}
	a.s.auditOperation(a.st, a.runner, op.ID, rec.Actor, "app.setup", fmt.Sprintf("operation %d: %s", op.ID, setupAuditDetail(*rec)))
	return op.ID, nil
}

// setupAuditDetail names the install and the approved digests.
func setupAuditDetail(rec setupRecord) string {
	parts := []string{fmt.Sprintf("%s after install operation %d", rec.App, rec.Install)}
	for _, step := range rec.Steps {
		if step.State == stepSkipped {
			parts = append(parts, step.Name+" skipped")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", step.Name, step.Digest))
	}
	return strings.Join(parts, "; ")
}

// ---- running ----

func (a *appSetup) update(app string, change func(rec *setupRecord)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok, err := a.load(app)
	if err != nil || !ok {
		return
	}
	change(&rec)
	if err := a.save(&rec); err != nil {
		a.s.log.Warn("app setup: record failed", "app", app, "error", err)
	}
}

// runStep plans one step again, compares it with its approval, and runs
// it. A changed plan or a failure stops the setup: the error fails the
// setup operation, so no later step runs.
func (a *appSetup) runStep(ctx context.Context, app, name, token string) error {
	a.mu.Lock()
	rec, ok, err := a.load(app)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the setup record is gone")
	}
	step := rec.step(name)
	if step == nil || step.State == stepDone || step.State == stepSkipped {
		return nil
	}
	approved := step.Digest
	a.update(app, func(r *setupRecord) {
		if s := r.step(name); s != nil {
			s.State, s.Reason, s.Operation = stepRunning, "", 0
		}
	})
	start := a.startStep
	if a.startFn != nil {
		start = a.startFn
	}
	id, err := start(ctx, rec, name, approved, token)
	if err == nil && id != 0 {
		a.update(app, func(r *setupRecord) {
			if s := r.step(name); s != nil {
				s.Operation = id
			}
		})
		var op operation.Operation
		if op, err = a.runner.Wait(ctx, id); err == nil && op.Status != operation.StatusSucceeded {
			err = fmt.Errorf("%s", firstNonEmpty(op.Error, "operation "+op.Status))
		}
	}
	if err != nil {
		state, reason := stepFailed, err.Error()
		if errors.Is(err, errPlanChanged) {
			state, reason = stepChanged, "The plan changed since it was approved, so nothing was changed. Review the new plan, then resume or skip."
		}
		a.update(app, func(r *setupRecord) {
			s := r.step(name)
			s.State, s.Reason = state, reason
			r.Status, r.Reason = setupStopped, stopReason(s)
		})
		return fmt.Errorf("%s: %s", setupStepLabels[name], reason)
	}
	a.update(app, func(r *setupRecord) {
		r.step(name).State = stepDone
		last := true
		for _, s := range r.Steps {
			if s.State != stepDone && s.State != stepSkipped {
				last = false
			}
		}
		if last {
			r.Status, r.Reason = setupFinished, ""
		}
	})
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// stopReason says where the setup stopped and why.
func stopReason(s *setupStep) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("Stopped at the %s: %s", setupStepLabels[s.Name], s.Reason)
}

// startStep re-plans the step, compares it with the approved digest
// (errPlanChanged when they differ), and starts it through the code its
// own route uses. It returns the step's operation, or 0 when the step
// finished in place.
func (a *appSetup) startStep(ctx context.Context, rec setupRecord, name, approved, token string) (int64, error) {
	switch name {
	case setupAddress:
		review, err := a.reviewAddress(rec)
		if err != nil {
			return 0, err
		}
		if review.Digest != approved {
			return 0, errPlanChanged
		}
		if len(review.add) == 0 {
			return 0, nil
		}
		id, _, err := startEdgeReconcile(ctx, a.runner, a.st, a.local, review.add, rec.Actor)
		return id, err
	case setupPublish:
		if a.s.appPublic == nil || rec.Public == nil {
			return 0, errors.New("public addresses are not available on this appliance")
		}
		on := true
		id, err := a.s.appPublic.start(ctx, rec.App, appPublicRequest{Enabled: &on, Hostname: rec.Public.Hostname, Route: rec.Public.Route, Digest: approved}, rec.Actor)
		return id, planErrorText(err)
	case setupSignIn:
		if a.s.appSignIn == nil {
			return 0, errors.New("sign-in with Authentik is not available on this appliance")
		}
		return a.s.appSignIn.start(ctx, rec.App, true, approved, token, rec.Actor)
	case setupPeople:
		if peopleDigest(rec.App, rec.People) != approved {
			return 0, errPlanChanged
		}
		if a.s.people == nil {
			return 0, errors.New("people are not managed on this appliance")
		}
		if err := a.validatePeople(rec.People); err != nil {
			return 0, err
		}
		for _, id := range rec.People {
			if _, err := a.s.people.Grant(ctx, rec.Actor, id, people.Grant{App: rec.App, Role: people.RoleUser}); err != nil {
				return 0, fmt.Errorf("granting %s did not finish: %w", personName(a.st, id), err)
			}
		}
		return 0, nil
	case setupFilesLink:
		return 0, a.runFilesLink(ctx, rec, approved)
	}
	return 0, fmt.Errorf("unknown setup step %q", name)
}

// planErrorText keeps errPlanChanged and turns a refusal into its words.
func planErrorText(err error) error {
	var refusal *planRefusal
	if err != nil && !errors.Is(err, errPlanChanged) && errors.As(err, &refusal) {
		return errors.New(refusal.msg)
	}
	return err
}

func personName(st *store.Store, id int64) string {
	var display, username string
	if err := st.DB().QueryRow(`SELECT display_name, username FROM people WHERE id = ?`, id).Scan(&display, &username); err != nil {
		return fmt.Sprintf("person %d", id)
	}
	return fmt.Sprintf("%s (%s)", display, username)
}

// ---- review ----

// stepReview is the current plan of a step, for a stopped setup's
// review: its digest and what it changes.
type stepReview struct {
	Step   string `json:"step"`
	Digest string `json:"digest,omitempty"`
	// Problem is why the step cannot be planned now; only skip is left.
	Problem string `json:"problem,omitempty"`
	// Addresses are the routes the address step adds; empty when the app
	// already has its address.
	Addresses []string       `json:"addresses,omitempty"`
	Public    *appPublicPlan `json:"public,omitempty"`
	SignIn    *appsso.Plan   `json:"sign_in,omitempty"`
	People    []string       `json:"people,omitempty"`
	TokenKept *bool          `json:"token_kept,omitempty"`
	// FilesLink is the nextcloud-link step's plan (S326).
	FilesLink *filesLinkPlan `json:"files_link,omitempty"`
	add       []docker.Route
}

// reviewAddress is the address step's plan: the reviewed routes merged
// by host over the current table, as the wizard merged them, or nothing
// once the app has an address on the edge.
func (a *appSetup) reviewAddress(rec setupRecord) (stepReview, error) {
	out := stepReview{Step: setupAddress, Digest: routesDigest(rec.Routes)}
	set, err := loadEdgeRoutes(a.st)
	if err != nil {
		return out, err
	}
	hosts := map[docker.Hostname]bool{}
	for _, r := range rec.Routes {
		hosts[r.Host] = true
		out.Addresses = append(out.Addresses, string(r.Host))
	}
	have := func(r docker.Route) {
		out.Addresses = nil
		if r.CraftApp != "" && !r.IsPath() {
			// A craft app is never on a name of its own (ADR-045 §1,
			// S326): reconciling the table as it is moves it to its path.
			out.add = append([]docker.Route(nil), set.Routes...)
		}
	}
	for _, r := range set.Routes {
		if hosts[r.Host] {
			have(r)
			return out, nil
		}
	}
	if entries, err := catalog.Load(a.catalogDir); err == nil {
		if m, err := entries.Get(rec.App); err == nil {
			if existing := appsso.AppRoutes(m, set.Routes); len(existing) > 0 {
				have(existing[0])
				return out, nil
			}
		}
	}
	merged := []docker.Route{}
	for _, r := range set.Routes {
		if !hosts[r.Host] {
			merged = append(merged, r)
		}
	}
	out.add = append(merged, rec.Routes...)
	return out, nil
}

// review plans the stopped step as it stands now.
func (a *appSetup) review(ctx context.Context, rec setupRecord, name string) stepReview {
	if a.reviewFn != nil {
		return a.reviewFn(ctx, rec, name)
	}
	out := stepReview{Step: name}
	switch name {
	case setupAddress:
		r, err := a.reviewAddress(rec)
		if err != nil {
			out.Problem = err.Error()
			return out
		}
		return r
	case setupPublish:
		if a.s.appPublic == nil || rec.Public == nil {
			out.Problem = "Public addresses are not available on this appliance."
			return out
		}
		on := true
		plan, err := a.s.appPublic.buildPlan(ctx, rec.App, appPublicRequest{Enabled: &on, Hostname: rec.Public.Hostname, Route: rec.Public.Route})
		if err != nil {
			out.Problem = planErrorText(err).Error()
			return out
		}
		out.Digest, out.Public = plan.Digest, &plan
	case setupSignIn:
		if a.s.appSignIn == nil {
			out.Problem = "Sign-in with Authentik is not available on this appliance."
			return out
		}
		in, err := a.s.appSignIn.inputs(rec.App)
		if err != nil {
			out.Problem = err.Error()
			return out
		}
		plan, err := appsso.BuildPlan(in, true)
		if err != nil {
			out.Problem = err.Error()
			return out
		}
		kept := a.s.appSignIn.tokenKept()
		out.Digest, out.SignIn, out.TokenKept = plan.Digest, &plan, &kept
	case setupPeople:
		out.Digest = peopleDigest(rec.App, rec.People)
		for _, id := range rec.People {
			out.People = append(out.People, personName(a.st, id))
		}
	case setupFilesLink:
		plan, err := a.filesLinkNow(rec)
		switch {
		case err != nil:
			out.Problem = err.Error()
		case plan == nil:
			out.Problem = "Nothing is left to link: the apps are gone, set to keep documents in the browser, or have no address, or Nextcloud is not installed. Skip the rest, or link on the Craft files page."
		default:
			out.Digest, out.FilesLink = plan.Digest, plan
		}
	}
	return out
}

// ---- routes ----

// setupView is GET /api/v1/catalog/{name}/setup.
type setupView struct {
	App       string      `json:"app"`
	Status    string      `json:"status"`
	Install   int64       `json:"install_operation,omitempty"`
	Operation int64       `json:"operation,omitempty"`
	Steps     []setupStep `json:"steps"`
	Reason    string      `json:"reason,omitempty"`
	// Review is the stopped step's plan as it stands now: approving its
	// digest resumes the setup.
	Review *stepReview `json:"review,omitempty"`
}

func (a *appSetup) known(w http.ResponseWriter, name string) (catalog.Manifest, bool) {
	return loadPackage(a.catalogDir, name, w)
}

func (a *appSetup) view(ctx context.Context, name string) (setupView, error) {
	rec, ok, err := a.current(name)
	if err != nil {
		return setupView{}, err
	}
	if !ok {
		return setupView{App: name, Status: setupNone, Steps: []setupStep{}}, nil
	}
	out := setupView{App: name, Status: rec.Status, Install: rec.Install, Operation: rec.Operation, Steps: rec.Steps, Reason: rec.Reason}
	if out.Steps == nil {
		out.Steps = []setupStep{}
	}
	if rec.Status == setupStopped {
		if s := rec.stopped(); s != nil {
			review := a.review(ctx, rec, s.Name)
			out.Review = &review
		}
	}
	return out, nil
}

func (a *appSetup) status(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := a.known(w, name); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	v, err := a.view(r.Context(), name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

// setupResumeBody approves the stopped step's current plan by digest.
// Token is an Authentik API token for a sign-in step, for this run only:
// it is never stored or logged.
type setupResumeBody struct {
	Digest string `json:"digest"`
	Token  string `json:"token,omitempty"`
}

func (a *appSetup) resume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := a.known(w, name); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var body setupResumeBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Digest) == "" {
		http.Error(w, "digest is required: review the stopped step's plan, then approve it with its digest", http.StatusBadRequest)
		return
	}
	rec, ok, err := a.current(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok || rec.Status != setupStopped || rec.stopped() == nil {
		http.Error(w, "there is no stopped setup to resume for this app", http.StatusConflict)
		return
	}
	stopped := rec.stopped()
	review := a.review(r.Context(), rec, stopped.Name)
	if review.Problem != "" {
		http.Error(w, review.Problem, http.StatusUnprocessableEntity)
		return
	}
	if review.Digest != body.Digest {
		http.Error(w, errPlanChanged.Error(), http.StatusConflict)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok, err = a.load(name)
	if err != nil || !ok || rec.Status != setupStopped || rec.stopped() == nil || rec.stopped().Name != stopped.Name {
		http.Error(w, "the setup changed meanwhile; read it again", http.StatusConflict)
		return
	}
	s := rec.stopped()
	s.Digest, s.State, s.Reason, s.Operation = body.Digest, stepWaiting, "", 0
	actor := operatorActor(r)
	_, _ = audit.New(a.st).Record(audit.Entry{Actor: actor, Action: "app.setup.resume", Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("%s: %s approved again with %s", name, s.Name, body.Digest)})
	rec.Actor = actor
	id, err := a.launch(context.WithoutCancel(r.Context()), &rec, body.Token)
	if err != nil {
		http.Error(w, "the setup could not start: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (a *appSetup) skip(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := a.known(w, name); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var body struct{}
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, _, err := a.current(name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.mu.Lock()
	rec, ok, err := a.load(name)
	if err != nil || !ok || rec.Status != setupStopped {
		a.mu.Unlock()
		http.Error(w, "there is no stopped setup to skip for this app", http.StatusConflict)
		return
	}
	at := rec.stopped()
	for i := range rec.Steps {
		if st := rec.Steps[i].State; st != stepDone && st != stepSkipped {
			if rec.Steps[i].Reason == "" || &rec.Steps[i] != at {
				rec.Steps[i].Reason = "Skipped by the owner."
			}
			rec.Steps[i].State = stepSkipped
		}
	}
	rec.Status, rec.Reason = setupEnded, ""
	err = a.save(&rec)
	a.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stepName := ""
	if at != nil {
		stepName = at.Name
	}
	_, _ = audit.New(a.st).Record(audit.Entry{Actor: operatorActor(r), Action: "app.setup.skip", Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("%s: setup ended at %s", name, firstNonEmpty(stepName, "its stopped step"))})
	v, err := a.view(r.Context(), name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

// ---- catalog list and doctor ----

// unfinishedSetups maps each installed app with an unfinished setup to
// running or stopped, for the catalog list.
func (a *appSetup) unfinished() map[string]string {
	out := map[string]string{}
	if a == nil {
		return out
	}
	recs, err := a.all()
	if err != nil {
		return out
	}
	for _, rec := range recs {
		if !rec.unfinished() {
			continue
		}
		state := setupRunning
		if rec.Status == setupStopped {
			state = setupStopped
		}
		out[rec.App] = state
	}
	return out
}

// setupDoctorCheck names each installed app whose approved setup did not
// finish (ADR-048 §3), with the way to resume it.
func (a *appSetup) doctorChecks() []doctor.Check {
	var out []doctor.Check
	recs, err := a.all()
	if err != nil {
		return nil
	}
	entries, loadErr := catalog.Load(a.catalogDir)
	for _, rec := range recs {
		if !rec.unfinished() {
			continue
		}
		if installed, _ := catalog.Installed(a.st, rec.App, stackDirFor(rec.App)); !installed && rec.Status != setupWaiting {
			continue
		}
		title := rec.App
		if loadErr == nil {
			if m, err := entries.Get(rec.App); err == nil {
				title = m.Title
			}
		}
		check := doctor.Check{Name: "app-setup:" + rec.App, Status: doctor.StatusWarn, Group: "Apps",
			Why: "The steps you approved with the install (its address, public address, sign-in and people) run on the appliance until they finish or you skip them."}
		if rec.Status == setupStopped {
			check.Title = title + "'s setup stopped"
			check.Detail = firstNonEmpty(rec.Reason, title+"'s setup stopped before it finished.")
			check.Fix = liveFix("Open the catalog, review the stopped step on "+title+"'s card, then resume or skip it.", "/admin/catalog",
				"homesteadctl apps setup status "+rec.App)
		} else {
			check.Title = title + "'s setup is running"
			check.Detail = title + "'s approved setup is still running."
			check.Fix = liveFix("Wait for it to finish; its card in the catalog shows each step.", "/admin/catalog", "homesteadctl apps setup status "+rec.App)
		}
		out = append(out, check)
	}
	return out
}
