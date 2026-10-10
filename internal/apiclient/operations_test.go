package apiclient

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"homestead/internal/operation"
	"homestead/internal/server"
	"homestead/internal/store"
)

func operationClient(t *testing.T) (*Client, *operation.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	opStore := operation.New(st)
	srv := server.New("127.0.0.1:0")
	srv.MountOperations(opStore)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return New(ts.URL), opStore
}

func TestGetOperation(t *testing.T) {
	c, opStore := operationClient(t)
	created, err := opStore.Create("test.op", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := c.GetOperation(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if got.ID != created.ID || got.Type != "test.op" {
		t.Fatalf("op = %+v", got)
	}
	if _, err := c.GetOperation(context.Background(), 9999); err == nil {
		t.Fatal("missing op succeeded, want error")
	} else if statusErr, ok := err.(*StatusError); !ok || statusErr.Status != 404 {
		t.Fatalf("err = %v, want 404 StatusError", err)
	}
}

func TestStreamOperation(t *testing.T) {
	c, opStore := operationClient(t)
	created, err := opStore.Create("test.op", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := opStore.AddStep(created.ID, "only"); err != nil {
		t.Fatalf("AddStep: %v", err)
	}
	go func() {
		time.Sleep(600 * time.Millisecond)
		_ = opStore.Transition(created.ID, operation.StatusRunning, "", "")
		time.Sleep(600 * time.Millisecond)
		_ = opStore.Transition(created.ID, operation.StatusSucceeded, "", "")
	}()
	var statuses []string
	err = c.StreamOperation(context.Background(), created.ID, func(op operation.Operation) {
		statuses = append(statuses, op.Status)
	})
	if err != nil {
		t.Fatalf("StreamOperation: %v", err)
	}
	for _, want := range []string{"pending", "running", "succeeded"} {
		found := false
		for _, status := range statuses {
			if status == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("statuses = %v, want %q", statuses, want)
		}
	}
}

func TestStreamMissing(t *testing.T) {
	c, _ := operationClient(t)
	err := c.StreamOperation(context.Background(), 9999, func(operation.Operation) {})
	if err == nil {
		t.Fatal("missing stream succeeded, want error")
	} else if statusErr, ok := err.(*StatusError); !ok || statusErr.Status != 404 {
		t.Fatalf("err = %v, want 404 StatusError", err)
	}
}

func TestListOperations(t *testing.T) {
	c, opStore := operationClient(t)
	for _, typ := range []string{"a.one", "b.two"} {
		if _, err := opStore.Create(typ, ""); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	ops, err := c.ListOperations(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListOperations: %v", err)
	}
	if len(ops) != 2 || ops[0].Type != "b.two" {
		t.Fatalf("ops = %+v, want newest first", ops)
	}
}

func TestStreamIgnoresCallTimeout(t *testing.T) {
	// Regression: streams once shared the 10s call timeout, killing
	// --follow on any operation quieter than that (image pulls).
	// A 50ms call timeout must not disturb a 300ms-quiet stream.
	c, opStore := operationClient(t)
	short := New(c.base, WithTimeout(50*time.Millisecond))
	created, err := opStore.Create("test.op", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = opStore.Transition(created.ID, operation.StatusRunning, "", "")
		time.Sleep(100 * time.Millisecond)
		_ = opStore.Transition(created.ID, operation.StatusSucceeded, "", "")
	}()
	if err := short.StreamOperation(context.Background(), created.ID, func(operation.Operation) {}); err != nil {
		t.Fatalf("StreamOperation: %v", err)
	}
}
