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

	"homestead/internal/operation"
	"homestead/internal/platformapply"
	"homestead/internal/release"
)

type routeSystemd struct{ called chan string }

func (r routeSystemd) Restart(_ context.Context, unit string) error { r.called <- unit; return nil }

func applyEnv(t *testing.T) (*platformEnv, routeSystemd, string) {
	t.Helper()
	e := newPlatformEnv(t)
	bin := filepath.Join(e.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "homestead-server"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	sd := routeSystemd{called: make(chan string, 2)}
	e.h.marks = platformapply.NewStore(e.st)
	e.h.applier = &platformapply.Applier{
		Releases: e.h.rel, Markers: e.h.marks, Runner: operation.NewRunner(e.st), Ops: operation.New(e.st), Audit: e.h.audit,
		Systemd: sd, BinDir: bin, StagingDir: filepath.Join(e.dir, "staging"), Unit: "homestead-server.service",
		Running: "0.0.1", RestartWait: time.Hour,
	}
	return e, sd, bin
}

func TestPlatformApplyRoute(t *testing.T) {
	e, sd, bin := applyEnv(t)
	body := e.release("0.1.0", 2)
	apply := func(allowOlder bool) (int, string) {
		raw, _ := json.Marshal(platformApplyBody{Component: "homestead-server", Artifact: body.Artifact, AllowOlder: allowOlder})
		rec := opRequest(http.MethodPost, string(raw), e.h.apply, "")
		return rec.Code, rec.Body.String()
	}
	if code, _ := opRequestBad(e); code != http.StatusBadRequest {
		t.Fatalf("bad body = %d", code)
	}
	if code, msg := apply(false); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "not verified") {
		t.Fatalf("unverified apply = %d %s", code, msg)
	}
	e.trust()
	if rec, code := e.verify(body); code != http.StatusOK || rec.State != release.StateVerified {
		t.Fatalf("verify = %d %+v", code, rec)
	}
	code, msg := apply(false)
	if code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, msg)
	}
	var out struct{ ID int64 }
	_ = json.Unmarshal([]byte(msg), &out)
	if out.ID == 0 {
		t.Fatalf("no operation id: %s", msg)
	}
	select {
	case unit := <-sd.called:
		if unit != "homestead-server.service" {
			t.Fatalf("unit = %s", unit)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no restart")
	}
	if b, _ := os.ReadFile(filepath.Join(bin, "homestead-server")); string(b) != "daemon 0.1.0" {
		t.Fatalf("binary = %q", b)
	}
	if code, msg := apply(false); code != http.StatusConflict {
		t.Fatalf("concurrent apply = %d %s", code, msg)
	}
	view := e.planView()
	if view.Pending == nil || view.Pending.ToVersion != "0.1.0" || view.Previous["homestead-server"].Version != "0.0.1" {
		t.Fatalf("plan view pending/previous = %+v %+v", view.Pending, view.Previous)
	}
}

func opRequestBad(e *platformEnv) (int, string) {
	rec := opRequest(http.MethodPost, `{"component":"nope","artifact":"/x"}`, e.h.apply, "")
	return rec.Code, rec.Body.String()
}
