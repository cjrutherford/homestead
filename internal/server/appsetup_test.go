package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/operation"
	"homestead/internal/people"
	"homestead/internal/store"
)

// setupFixture is an app setup with its step starts stubbed: calls
// records the steps in the order they started.
type setupFixture struct {
	srv   *Server
	st    *store.Store
	a     *appSetup
	mu    sync.Mutex
	calls []string
	// fail maps a step to what starting it returns.
	fail map[string]error
	// subop maps a step to an operation it starts and the setup waits on.
	subop map[string]func(ctx context.Context) error
}

func newSetupFixture(t *testing.T) *setupFixture {
	t.Helper()
	f := &setupFixture{st: statelessStore(t), fail: map[string]error{}, subop: map[string]func(context.Context) error{}}
	f.srv = New("127.0.0.1:0")
	f.srv.MountAppSetup(f.st, statelessCatalog(t), nil)
	f.a = f.srv.appSetup
	f.a.startFn = func(ctx context.Context, rec setupRecord, name, approved, token string) (int64, error) {
		f.mu.Lock()
		f.calls = append(f.calls, name+"="+approved)
		err, run := f.fail[name], f.subop[name]
		f.mu.Unlock()
		if err != nil {
			return 0, err
		}
		if run != nil {
			op, err := f.a.runner.Start(ctx, operation.Definition{Type: "test.step", Steps: []operation.StepDef{{Name: "run", Run: run}}}, "")
			return op.ID, err
		}
		return 0, nil
	}
	f.a.reviewFn = func(_ context.Context, rec setupRecord, name string) stepReview {
		return stepReview{Step: name, Digest: "new-" + name}
	}
	return f
}

func (f *setupFixture) started() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// install runs an install operation carrying the approved setup, with a
// request context that is already gone, and waits for the setup run.
func (f *setupFixture) install(t *testing.T) setupRecord {
	t.Helper()
	rec := setupRecord{App: "statelessapp", Status: setupWaiting, Actor: "owner", Steps: []setupStep{
		{Name: setupAddress, State: stepWaiting, Digest: "d-address"},
		{Name: setupPublish, State: stepWaiting, Digest: "d-publish"},
		{Name: setupSignIn, State: stepWaiting, Digest: "d-sign-in"},
		{Name: setupPeople, State: stepWaiting, Digest: "d-people"},
	}}
	def := operation.Definition{Type: "app.install", Steps: []operation.StepDef{{Name: "install", Run: func(context.Context) error { return nil }}}}
	if err := f.a.attach(rec, &def); err != nil {
		t.Fatal(err)
	}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	op, err := f.a.runner.Start(gone, def, "")
	if err != nil {
		t.Fatal(err)
	}
	f.a.installed(rec.App, op.ID)
	if done, err := f.a.runner.Wait(context.Background(), op.ID); err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("install = %+v, %v", done, err)
	}
	return f.waitSetup(t)
}

func (f *setupFixture) waitSetup(t *testing.T) setupRecord {
	t.Helper()
	got, ok, err := f.a.load("statelessapp")
	if err != nil || !ok || got.Operation == 0 {
		t.Fatalf("setup record = %+v, %v, %v", got, ok, err)
	}
	if _, err := f.a.runner.Wait(context.Background(), got.Operation); err != nil {
		t.Fatal(err)
	}
	got, _, err = f.a.current("statelessapp")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (f *setupFixture) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body)))
	return rec
}

func states(rec setupRecord) string {
	var out []string
	for _, s := range rec.Steps {
		out = append(out, s.Name+":"+s.State)
	}
	return strings.Join(out, " ")
}

func TestAppSetupRunsApprovedStepsInOrderWithoutAClient(t *testing.T) {
	f := newSetupFixture(t)
	got := f.install(t)
	want := "address=d-address publish=d-publish sign-in=d-sign-in people=d-people"
	if calls := strings.Join(f.started(), " "); calls != want {
		t.Fatalf("steps = %q, want %q", calls, want)
	}
	if got.Status != setupFinished || states(got) != "address:done publish:done sign-in:done people:done" {
		t.Fatalf("setup = %s %s", got.Status, states(got))
	}
	if got.Install == 0 {
		t.Fatal("setup does not name its install")
	}
	entries, err := audit.New(f.st).List(50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "app.setup.start" && strings.Contains(e.Detail, "publish d-publish") && strings.Contains(e.Detail, "install operation") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no app.setup audit naming the install and digests: %+v", entries)
	}
}

func TestAppSetupStopsWhenAPlanChanged(t *testing.T) {
	f := newSetupFixture(t)
	f.fail[setupPublish] = errPlanChanged
	got := f.install(t)
	if calls := strings.Join(f.started(), " "); calls != "address=d-address publish=d-publish" {
		t.Fatalf("steps = %q; nothing after a changed plan may run", calls)
	}
	if got.Status != setupStopped || states(got) != "address:done publish:changed sign-in:waiting people:waiting" {
		t.Fatalf("setup = %s %s", got.Status, states(got))
	}
	if !strings.Contains(got.Reason, "public address") || !strings.Contains(got.Reason, "plan changed") {
		t.Fatalf("reason = %q", got.Reason)
	}
}

func TestAppSetupStopsWhenAStepFails(t *testing.T) {
	f := newSetupFixture(t)
	f.subop[setupSignIn] = func(context.Context) error { return errors.New("Authentik refused the provider") }
	got := f.install(t)
	if calls := len(f.started()); calls != 3 {
		t.Fatalf("steps started = %v; people must not run after a failure", f.started())
	}
	if got.Status != setupStopped || states(got) != "address:done publish:done sign-in:failed people:waiting" {
		t.Fatalf("setup = %s %s", got.Status, states(got))
	}
	if s := got.step(setupSignIn); !strings.Contains(s.Reason, "Authentik refused the provider") || s.Operation == 0 {
		t.Fatalf("sign-in step = %+v", s)
	}
}

func TestAppSetupResumeNeedsAFreshApprovalAndRunsTheRest(t *testing.T) {
	f := newSetupFixture(t)
	f.fail[setupPublish] = errPlanChanged
	f.install(t)
	delete(f.fail, setupPublish)

	if rec := f.post(t, "/api/v1/catalog/statelessapp/setup/resume", `{"digest":"d-publish"}`); rec.Code != http.StatusConflict {
		t.Fatalf("resume with the old digest = %d %s, want 409", rec.Code, rec.Body)
	}
	rec := f.post(t, "/api/v1/catalog/statelessapp/setup/resume", `{"digest":"new-publish"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume = %d %s", rec.Code, rec.Body)
	}
	got := f.waitSetup(t)
	want := "address=d-address publish=d-publish publish=new-publish sign-in=d-sign-in people=d-people"
	if calls := strings.Join(f.started(), " "); calls != want {
		t.Fatalf("steps = %q, want %q", calls, want)
	}
	if got.Status != setupFinished {
		t.Fatalf("setup = %s %s", got.Status, states(got))
	}
	if rec := f.post(t, "/api/v1/catalog/statelessapp/setup/resume", `{"digest":"new-publish"}`); rec.Code != http.StatusConflict {
		t.Fatalf("resume of a finished setup = %d, want 409", rec.Code)
	}
}

func TestAppSetupSkipEndsIt(t *testing.T) {
	f := newSetupFixture(t)
	f.fail[setupAddress] = errors.New("the edge refused the route")
	f.install(t)
	rec := f.post(t, "/api/v1/catalog/statelessapp/setup/skip", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip = %d %s", rec.Code, rec.Body)
	}
	var view setupView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Status != setupEnded {
		t.Fatalf("status = %s", view.Status)
	}
	for _, s := range view.Steps {
		if s.State != stepSkipped {
			t.Fatalf("step %s = %s, want skipped", s.Name, s.State)
		}
	}
	if len(f.started()) != 1 {
		t.Fatalf("skip ran steps: %v", f.started())
	}
	if got := f.a.unfinished(); len(got) != 0 {
		t.Fatalf("unfinished after skip = %v", got)
	}
	if rec := f.post(t, "/api/v1/catalog/statelessapp/setup/skip", `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("second skip = %d, want 409", rec.Code)
	}
}

func TestAppSetupStatusAndCatalogFlag(t *testing.T) {
	f := newSetupFixture(t)
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/statelessapp/setup", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"none"`) {
		t.Fatalf("status without a setup = %d %s", rec.Code, rec.Body)
	}
	f.fail[setupSignIn] = errPlanChanged
	f.install(t)
	rec = httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/statelessapp/setup", nil))
	var view setupView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Status != setupStopped || view.Review == nil || view.Review.Step != setupSignIn || view.Review.Digest != "new-sign-in" {
		t.Fatalf("view = %+v", view)
	}
	if got := f.a.unfinished()["statelessapp"]; got != setupStopped {
		t.Fatalf("catalog flag = %q", got)
	}
}

func TestAppSetupAfterAFailedInstallIsForgotten(t *testing.T) {
	f := newSetupFixture(t)
	rec := setupRecord{App: "statelessapp", Status: setupWaiting, Actor: "owner", Steps: []setupStep{{Name: setupAddress, State: stepWaiting, Digest: "d"}}}
	def := operation.Definition{Type: "app.install", Steps: []operation.StepDef{{Name: "install", Run: func(context.Context) error { return errors.New("pull failed") }}}}
	if err := f.a.attach(rec, &def); err != nil {
		t.Fatal(err)
	}
	op, err := f.a.runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.runner.Wait(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.a.current("statelessapp"); ok {
		t.Fatal("a setup outlived its failed install")
	}
	if len(f.started()) != 0 {
		t.Fatalf("steps ran after a failed install: %v", f.started())
	}
}

func TestAppSetupInterruptedRunIsStopped(t *testing.T) {
	f := newSetupFixture(t)
	op, err := f.a.runner.Start(context.Background(), operation.Definition{Type: "app.setup", Steps: []operation.StepDef{{Name: "x", Run: func(context.Context) error { return errors.New("interrupted by restart") }}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.runner.Wait(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
	rec := setupRecord{App: "statelessapp", Status: setupRunning, Operation: op.ID, Steps: []setupStep{
		{Name: setupAddress, State: stepDone}, {Name: setupPublish, State: stepRunning, Digest: "d"}, {Name: setupSignIn, State: stepWaiting},
	}}
	if err := f.a.save(&rec); err != nil {
		t.Fatal(err)
	}
	got, _, err := f.a.current("statelessapp")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != setupStopped || states(got) != "address:done publish:failed sign-in:waiting" || !strings.Contains(got.Reason, "interrupted by restart") {
		t.Fatalf("setup = %s %s %q", got.Status, states(got), got.Reason)
	}
	if checks := f.a.doctorChecks(); len(checks) != 0 {
		// statelessapp is not installed in this store: no check.
		t.Fatalf("doctor checks for an uninstalled app = %+v", checks)
	}
}

func TestAppSetupPrepareKeepsApprovedStepsOnly(t *testing.T) {
	f := newSetupFixture(t)
	m := statelessManifest(t, "statelessapp")
	if _, err := f.a.prepare(m, catalog.InstallPlan{}, setupApproval{SignIn: &setupDigest{Digest: "x"}}, "owner"); err == nil {
		t.Fatal("sign-in approved for an app without sign-in")
	}
	rec, err := f.a.prepare(m, catalog.InstallPlan{}, setupApproval{Public: &setupPublicApproval{Route: "web.home.arpa", Digest: "p"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if states(rec) != "publish:waiting" || rec.Public.Digest != "p" {
		t.Fatalf("prepared = %s %+v", states(rec), rec.Public)
	}
}

func TestAppSetupRejectsPeopleWithoutSignIn(t *testing.T) {
	f := newSetupFixture(t)
	m := statelessManifest(t, "statelessapp")
	if _, err := f.a.prepare(m, catalog.InstallPlan{}, setupApproval{People: []int64{1}}, "owner"); err == nil {
		t.Fatal("people must not be silently discarded without sign-in")
	}
}

func TestAppSetupValidatesAllPeopleBeforeGrants(t *testing.T) {
	f := newSetupFixture(t)
	f.srv.people = &people.Service{DB: f.st.DB()}
	for _, row := range []struct {
		id    int
		state string
	}{{1, "active"}, {2, "disabled"}} {
		_, err := f.st.DB().Exec(`INSERT INTO people (id, display_name, username, authentik_pk, state, created_by, created_at) VALUES (?, ?, ?, ?, ?, 'owner', '2026-01-01T00:00:00Z')`, row.id, "person", fmt.Sprint(row.id), row.id, row.state)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, ids := range [][]int64{{1, 2}, {1, 99}, {1, 1}, {0}} {
		if err := f.a.validatePeople(ids); err == nil {
			t.Fatalf("accepted %v", ids)
		}
	}
	if err := f.a.validatePeople([]int64{1}); err != nil {
		t.Fatal(err)
	}
	rec := setupRecord{App: "statelessapp", People: []int64{1, 2}}
	if _, err := f.a.startStep(context.Background(), rec, setupPeople, peopleDigest(rec.App, rec.People), ""); err == nil {
		t.Fatal("disabled person must stop the step before the first grant")
	}
}

func TestAppSetupSignInWithoutAKeptTokenIsSkippedWithAReason(t *testing.T) {
	f := newSetupFixture(t)
	m := statelessManifest(t, "statelessapp")
	m.SSO.OIDC = &catalog.OIDCAdapter{}
	rec, err := f.a.prepare(m, catalog.InstallPlan{}, setupApproval{SignIn: &setupDigest{Digest: "s"}, People: []int64{3, 1}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if states(rec) != "sign-in:skipped people:skipped" {
		t.Fatalf("prepared = %s", states(rec))
	}
	if r := rec.step(setupSignIn).Reason; !strings.Contains(r, "keeps no Authentik API token") {
		t.Fatalf("sign-in reason = %q", r)
	}
}
