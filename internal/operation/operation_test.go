package operation

import (
	"errors"
	"path/filepath"
	"testing"

	"homestead/internal/store"
)

func openOperationStore(t *testing.T) *Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s)
}

func TestStateMachine(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		ok       bool
	}{
		{StatusPending, StatusRunning, true},
		{StatusPending, StatusCancelled, true},
		{StatusPending, StatusSucceeded, false},
		{StatusPending, StatusPending, false},
		{StatusRunning, StatusSucceeded, true},
		{StatusRunning, StatusFailed, true},
		{StatusRunning, StatusCancelled, true},
		{StatusRunning, StatusPending, false},
		{StatusSucceeded, StatusRunning, false},
		{StatusFailed, StatusRunning, false},
		{StatusCancelled, StatusPending, false},
		{"bogus", StatusRunning, false},
	} {
		if got := CanTransition(tc.from, tc.to); got != tc.ok {
			t.Fatalf("%s -> %s = %v, want %v", tc.from, tc.to, got, tc.ok)
		}
	}
	for _, terminal := range []string{StatusSucceeded, StatusFailed, StatusCancelled} {
		if !Terminal(terminal) {
			t.Fatalf("%s not terminal", terminal)
		}
	}
	if Terminal(StatusRunning) || Terminal(StatusPending) {
		t.Fatal("open states terminal")
	}
}

func TestValidateInputs(t *testing.T) {
	for _, bad := range []string{"", "Bad", "has space", "UPPER"} {
		if err := ValidateType(bad); err == nil {
			t.Fatalf("type %q accepted", bad)
		}
	}
	for _, good := range []string{"firewall.apply", "catalog.install", "x"} {
		if err := ValidateType(good); err != nil {
			t.Fatalf("type %q: %v", good, err)
		}
	}
	if err := ValidatePayload(`{"a": 1}`); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if err := ValidatePayload(""); err != nil {
		t.Fatalf("empty payload: %v", err)
	}
	if err := ValidatePayload("{oops"); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestCreateGetLifecycle(t *testing.T) {
	svc := openOperationStore(t)
	created, err := svc.Create("firewall.apply", `{"port": 8080}`)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != StatusPending || created.Type != "firewall.apply" {
		t.Fatalf("created = %+v", created)
	}
	if _, err := svc.Create("Bad Type!", ""); err == nil {
		t.Fatal("bad type accepted")
	}
	if _, err := svc.Create("x.y", "{oops"); err == nil {
		t.Fatal("bad payload accepted")
	}
	if _, err := svc.Get(9999); err != ErrNotFound {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	if err := svc.Transition(created.ID, StatusRunning, "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.Transition(created.ID, StatusRunning, "", ""); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("double start = %v, want ErrBadTransition", err)
	}
	if err := svc.Transition(created.ID, StatusSucceeded, `{"ok": true}`, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	got, err := svc.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusSucceeded || got.Result != `{"ok": true}` {
		t.Fatalf("got = %+v", got)
	}
	if err := svc.Transition(created.ID, StatusFailed, "", "x"); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("terminal move = %v, want ErrBadTransition", err)
	}
	if err := svc.Transition(9999, StatusRunning, "", ""); err != ErrNotFound {
		t.Fatalf("missing transition = %v, want ErrNotFound", err)
	}
}

func TestStepsLifecycle(t *testing.T) {
	svc := openOperationStore(t)
	created, err := svc.Create("catalog.install", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := svc.AddStep(created.ID, "download")
	if err != nil {
		t.Fatalf("AddStep: %v", err)
	}
	second, err := svc.AddStep(created.ID, "install")
	if err != nil {
		t.Fatalf("AddStep: %v", err)
	}
	if first.Position != 0 || second.Position != 1 {
		t.Fatalf("positions %d, %d", first.Position, second.Position)
	}
	if _, err := svc.AddStep(created.ID, ""); err == nil {
		t.Fatal("empty step name accepted")
	}
	if _, err := svc.AddStep(9999, "x"); err != ErrNotFound {
		t.Fatalf("missing op step = %v, want ErrNotFound", err)
	}
	if err := svc.TransitionStep(created.ID, first.ID, StatusRunning, ""); err != nil {
		t.Fatalf("start step: %v", err)
	}
	if err := svc.TransitionStep(created.ID, first.ID, StatusSucceeded, ""); err != nil {
		t.Fatalf("finish step: %v", err)
	}
	if err := svc.TransitionStep(created.ID, first.ID, StatusFailed, ""); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("terminal step = %v, want ErrBadTransition", err)
	}
	if err := svc.TransitionStep(created.ID, 9999, StatusRunning, ""); err != ErrNotFound {
		t.Fatalf("missing step = %v, want ErrNotFound", err)
	}
	got, err := svc.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Steps) != 2 || got.Steps[0].Name != "download" || got.Steps[0].Status != StatusSucceeded {
		t.Fatalf("steps = %+v", got.Steps)
	}
	if got.Steps[0].StartedAt.IsZero() || got.Steps[0].FinishedAt.IsZero() {
		t.Fatal("step timestamps missing")
	}
}

func TestListNewestFirst(t *testing.T) {
	svc := openOperationStore(t)
	for _, typ := range []string{"a.one", "b.two", "c.three"} {
		if _, err := svc.Create(typ, ""); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	ops, err := svc.List(2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ops) != 2 || ops[0].Type != "c.three" || ops[1].Type != "b.two" {
		t.Fatalf("list = %+v", ops)
	}
	ops, err = svc.List(0)
	if err != nil || len(ops) != 3 {
		t.Fatalf("default list = %v, %v", ops, err)
	}
}
