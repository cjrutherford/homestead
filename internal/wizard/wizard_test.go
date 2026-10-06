package wizard

import (
	"path/filepath"
	"testing"

	"homestead/internal/store"
)

func openWizardService(t *testing.T) *Service {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s)
}

func TestGetMissingIsNotFound(t *testing.T) {
	svc := openWizardService(t)
	if _, err := svc.Get("claim"); err != ErrNotFound {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	svc := openWizardService(t)
	saved, err := svc.Put("claim", "admin", map[string]any{"attempts": 1})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if saved.Wizard != "claim" || saved.Step != "admin" {
		t.Fatalf("Put = %+v, want claim/admin", saved)
	}
	if saved.Payload["attempts"] != float64(1) {
		t.Fatalf("Payload = %v, want attempts", saved.Payload)
	}
	if saved.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt zero")
	}
	got, err := svc.Get("claim")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Step != "admin" || got.Payload["attempts"] != float64(1) {
		t.Fatalf("Get = %+v", got)
	}
}

func TestPutOverwrites(t *testing.T) {
	svc := openWizardService(t)
	if _, err := svc.Put("claim", "token", nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := svc.Put("claim", "done", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got.Step != "done" || got.Payload["ok"] != true {
		t.Fatalf("Put = %+v", got)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	svc := openWizardService(t)
	if _, err := svc.Put("claim", "admin", nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := svc.Delete("claim"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get("claim"); err != ErrNotFound {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := svc.Delete("claim"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestValidation(t *testing.T) {
	svc := openWizardService(t)
	if _, err := svc.Get("Bad!"); err != ErrBadID {
		t.Fatalf("Get = %v, want ErrBadID", err)
	}
	if _, err := svc.Put("Bad!", "admin", nil); err != ErrBadID {
		t.Fatalf("Put = %v, want ErrBadID", err)
	}
	if err := svc.Delete("Bad!"); err != ErrBadID {
		t.Fatalf("Delete = %v, want ErrBadID", err)
	}
	if _, err := svc.Put("claim", "", nil); err != ErrBadStep {
		t.Fatalf("Put = %v, want ErrBadStep", err)
	}
}
