package purge

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/store"
)

type rig struct {
	t       *testing.T
	svc     *Service
	calls   []string
	stopped map[string]bool
	zero    bool // report "purged 0 people" for a subject purge
	logs    []string
	apps    []Target
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &rig{t: t, stopped: map[string]bool{}, apps: []Target{{"tasks", "tasks-tasks"}, {"notes", "notes-notes"}}}
	r.svc = &Service{
		DB:   st.DB(),
		Apps: func(context.Context) ([]Target, error) { return r.apps, nil },
		Exec: func(_ context.Context, c string, argv ...string) (string, error) {
			r.calls = append(r.calls, c+" "+strings.Join(argv, " "))
			if r.stopped[c] {
				return "", errors.New("container " + c + " is not running")
			}
			if r.zero && strings.Contains(strings.Join(argv, " "), "-subject=") {
				return "purged 0 people\n", nil
			}
			return "purged 1 people\n", nil
		},
		Audit: func(actor, action, outcome, detail string) { r.logs = append(r.logs, action+"|"+outcome+"|"+detail) },
	}
	return r
}

func count(db *sql.DB) int {
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM everything_purges`).Scan(&n)
	return n
}

func TestRequestRunsToolInEveryEverythingApp(t *testing.T) {
	r := newRig(t)
	outs := r.svc.Request(context.Background(), "owner", "ada", "sub-1", "")
	if len(outs) != 2 || outs[0].State != StateDone || outs[1].State != StateDone {
		t.Fatalf("%+v", outs)
	}
	want := "tasks-tasks /everything-bridge people purge -db /data/bridge.db -app tasks -subject=sub-1"
	if len(r.calls) != 2 || r.calls[0] != want {
		t.Fatalf("calls: %q", r.calls)
	}
	if count(r.svc.DB) != 0 || r.svc.HasPending() {
		t.Fatal("a done purge must not stay pending")
	}
	if !strings.Contains(strings.Join(r.logs, "\n"), "people.purge|ok|ada in tasks") {
		t.Fatalf("audit: %q", r.logs)
	}
}

func TestRequestOneAppIgnoresOthers(t *testing.T) {
	r := newRig(t)
	if outs := r.svc.Request(context.Background(), "owner", "ada", "", "nextcloud"); len(outs) != 0 || len(r.calls) != 0 {
		t.Fatalf("a non-Everything app must be ignored: %+v %q", outs, r.calls)
	}
	outs := r.svc.Request(context.Background(), "owner", "ada", "", "notes")
	if len(outs) != 1 || outs[0].App != "notes" || !strings.HasSuffix(r.calls[0], "-username=ada") {
		t.Fatalf("%+v %q", outs, r.calls)
	}
}

func TestSubjectMissFallsBackToUsername(t *testing.T) {
	r := newRig(t)
	r.zero = true
	r.svc.Request(context.Background(), "owner", "ada", "sub-1", "tasks")
	if len(r.calls) != 2 || !strings.HasSuffix(r.calls[1], "-username=ada") {
		t.Fatalf("calls: %q", r.calls)
	}
}

func TestStoppedContainerIsPendingThenRetried(t *testing.T) {
	r := newRig(t)
	r.stopped["tasks-tasks"] = true
	outs := r.svc.Request(context.Background(), "owner", "ada", "sub-1", "")
	if outs[0].State != StatePending || outs[1].State != StateDone {
		t.Fatalf("%+v", outs)
	}
	list, _ := r.svc.List()
	if len(list) != 1 || list[0].App != "tasks" || list[0].Username != "ada" || list[0].Attempts != 1 || !strings.Contains(list[0].LastError, "not running") {
		t.Fatalf("%+v", list)
	}
	if !r.svc.HasPending() {
		t.Fatal("pending expected")
	}
	// Still stopped: stays pending, attempts grow.
	r.svc.Retry(context.Background(), "scheduler", "")
	if list, _ = r.svc.List(); len(list) != 1 || list[0].Attempts != 2 {
		t.Fatalf("%+v", list)
	}
	// Another app's retry does not touch it.
	r.calls = nil
	r.svc.Retry(context.Background(), "scheduler", "notes")
	if len(r.calls) != 0 {
		t.Fatalf("calls: %q", r.calls)
	}
	r.stopped["tasks-tasks"] = false
	r.svc.Retry(context.Background(), "scheduler", "tasks")
	if r.svc.HasPending() {
		t.Fatal("retry should have finished it")
	}
	if !strings.Contains(strings.Join(r.logs, "\n"), "people.purge|pending|ada in tasks: pending") {
		t.Fatalf("audit: %q", r.logs)
	}
}

func TestRetryDropsUninstalledApp(t *testing.T) {
	r := newRig(t)
	r.stopped["tasks-tasks"] = true
	r.svc.Request(context.Background(), "owner", "ada", "", "tasks")
	r.apps = r.apps[1:]
	r.svc.Retry(context.Background(), "scheduler", "")
	if r.svc.HasPending() {
		t.Fatal("a purge for an uninstalled app must be dropped")
	}
}

func TestCancelDropsPendingForGrantedAgain(t *testing.T) {
	r := newRig(t)
	r.stopped["tasks-tasks"], r.stopped["notes-notes"] = true, true
	r.svc.Request(context.Background(), "owner", "ada", "", "")
	r.svc.Request(context.Background(), "owner", "bob", "", "")
	r.svc.Cancel("ada", "tasks")
	if list, _ := r.svc.List(); len(list) != 3 {
		t.Fatalf("%+v", list)
	}
	r.svc.Cancel("ada", "")
	if list, _ := r.svc.List(); len(list) != 2 || list[0].Username != "bob" {
		t.Fatalf("%+v", list)
	}
}

func TestNilServiceIsInert(t *testing.T) {
	var s *Service
	if s.Request(context.Background(), "a", "b", "", "") != nil || s.HasPending() {
		t.Fatal("nil must do nothing")
	}
	if l, err := s.List(); err != nil || len(l) != 0 {
		t.Fatal("nil list")
	}
	s.Cancel("x", "")
	s.Retry(context.Background(), "a", "")
}
