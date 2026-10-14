package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/platformapply"
	"homestead/internal/release"
)

type routeSystemd struct {
	started chan string
	active  bool
}

func (r *routeSystemd) Start(_ context.Context, unit string) error { r.started <- unit; return nil }
func (r *routeSystemd) Restart(context.Context, string) error      { return nil }
func (r *routeSystemd) MainPID(context.Context, string) (int, error) {
	return 1, nil
}
func (r *routeSystemd) Active(context.Context, string) (bool, error) { return r.active, nil }

func applyEnv(t *testing.T) (*platformEnv, *routeSystemd, string) {
	t.Helper()
	e := newPlatformEnv(t)
	bin := filepath.Join(e.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "homestead-server"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	sd := &routeSystemd{started: make(chan string, 2)}
	e.h.marks = platformapply.NewStore(e.st)
	e.h.applier = &platformapply.Applier{
		Releases: e.h.rel, Markers: e.h.marks, Runner: operation.NewRunner(e.st), Ops: operation.New(e.st), Audit: e.h.audit,
		Systemd: sd, BinDir: bin, StagingDir: filepath.Join(e.dir, "staging"), Unit: "homestead-server.service",
		ApplyUnit: "homestead-apply.service", Running: "0.0.1", AwaitPoll: 5 * time.Millisecond,
	}
	return e, sd, bin
}

func TestPlatformApplyRouteAndClear(t *testing.T) {
	e, sd, bin := applyEnv(t)
	body := e.release("0.1.0", 2)
	apply := func() (int, string) {
		raw, _ := json.Marshal(platformApplyBody{Component: "homestead-server", Artifact: body.Artifact,
			Checksums: body.Checksums, Signature: body.Signature})
		rec := opRequest(http.MethodPost, string(raw), e.h.apply, "")
		return rec.Code, rec.Body.String()
	}
	if rec := opRequest(http.MethodPost, `{"component":"nope","artifact":"/x","checksums":"/y","signature":"/z"}`, e.h.apply, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body = %d", rec.Code)
	}
	if code, msg := apply(); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "not verified") {
		t.Fatalf("unverified apply = %d %s", code, msg)
	}
	e.trust()
	if rec, code := e.verify(body); code != http.StatusOK || rec.State != release.StateVerified {
		t.Fatalf("verify = %d %+v", code, rec)
	}
	code, msg := apply()
	if code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, msg)
	}
	var out struct{ ID int64 }
	_ = json.Unmarshal([]byte(msg), &out)
	select {
	case unit := <-sd.started:
		if unit != "homestead-apply.service" {
			t.Fatalf("unit = %s", unit)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("apply unit not started")
	}
	if b, _ := os.ReadFile(filepath.Join(bin, "homestead-server")); string(b) != "old" {
		t.Fatalf("the daemon must not replace binaries, got %q", b)
	}
	if code, _ := apply(); code != http.StatusConflict {
		t.Fatalf("concurrent apply = %d", code)
	}
	view := e.planView()
	if view.Pending == nil || view.Pending.ToVersion != "0.1.0" {
		t.Fatalf("pending = %+v", view.Pending)
	}
	// Doctor: fresh marker is fine; an old one warns.
	now := time.Now()
	if checks := platformApplyDoctorCheck(e.st, func() time.Time { return now })(); checks[0].Status != doctor.StatusOK {
		t.Fatalf("fresh marker doctor = %+v", checks)
	}
	later := now.Add(ApplyStaleAfter + time.Minute)
	if checks := platformApplyDoctorCheck(e.st, func() time.Time { return later })(); checks[0].Status != doctor.StatusWarn || !strings.Contains(checks[0].Detail, "clear-apply") {
		t.Fatalf("stale marker doctor = %+v", checks)
	}
	// clear-apply: refused while the unit runs, then clears and the
	// failed apply fails doctor.
	sd.active = true
	if rec := opRequest(http.MethodPost, `{}`, e.h.clearApply, ""); rec.Code != http.StatusConflict {
		t.Fatalf("clear while active = %d", rec.Code)
	}
	sd.active = false
	if rec := opRequest(http.MethodPost, `{}`, e.h.clearApply, ""); rec.Code != http.StatusOK {
		t.Fatalf("clear = %d %s", rec.Code, rec.Body.String())
	}
	if rec := opRequest(http.MethodPost, `{}`, e.h.clearApply, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second clear = %d", rec.Code)
	}
	checks := platformApplyDoctorCheck(e.st, time.Now)()
	if checks[0].Status != doctor.StatusFail || !strings.Contains(checks[0].Detail, "failed") {
		t.Fatalf("failed apply doctor = %+v", checks)
	}
}
