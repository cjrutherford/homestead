package audit

import (
	"path/filepath"
	"testing"

	"homestead/internal/store"
)

func openAuditStore(t *testing.T) *Service {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s)
}

func TestRecordGetRoundTrip(t *testing.T) {
	svc := openAuditStore(t)
	entry, err := svc.Record(Entry{
		Actor:   "owner",
		Action:  "admin.login",
		Outcome: OutcomeOK,
		Detail:  "session minted",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if entry.ID == 0 || entry.CreatedAt.IsZero() {
		t.Fatalf("entry = %+v", entry)
	}
	got, err := svc.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != entry {
		t.Fatalf("got = %+v, want %+v", got, entry)
	}
	if _, err := svc.Get(9999); err != ErrNotFound {
		t.Fatalf("missing = %v, want ErrNotFound", err)
	}
}

func TestRecordValidation(t *testing.T) {
	svc := openAuditStore(t)
	base := Entry{Actor: "owner", Action: "test.run", Outcome: OutcomeOK}
	for _, tc := range []struct {
		name   string
		mutate func(*Entry)
	}{
		{"empty actor", func(e *Entry) { e.Actor = "" }},
		{"empty action", func(e *Entry) { e.Action = "" }},
		{"bad outcome", func(e *Entry) { e.Outcome = "maybe" }},
		{"bad plan", func(e *Entry) { e.Plan = "{oops" }},
		{"reasonless override", func(e *Entry) { e.Override = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := base
			tc.mutate(&entry)
			if _, err := svc.Record(entry); err == nil {
				t.Fatal("accepted, want error")
			}
		})
	}
	override, err := svc.Record(Entry{
		Actor: "owner", Action: "route.expose", Outcome: OutcomeOK,
		Plan: `{"route": "media"}`, Override: true, OverrideReason: "demo override",
	})
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	got, err := svc.Get(override.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Override || got.OverrideReason != "demo override" || got.Plan != `{"route": "media"}` {
		t.Fatalf("got = %+v", got)
	}
}

func TestListNewestFirstAndClamped(t *testing.T) {
	svc := openAuditStore(t)
	for _, action := range []string{"a.one", "b.two", "c.three"} {
		if _, err := svc.Record(Entry{Actor: "owner", Action: action, Outcome: OutcomeOK}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	entries, err := svc.List(2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 || entries[0].Action != "c.three" || entries[1].Action != "b.two" {
		t.Fatalf("list = %+v", entries)
	}
	entries, err = svc.List(0)
	if err != nil || len(entries) != 3 {
		t.Fatalf("default list = %v, %v", entries, err)
	}
	entries, err = svc.List(5000)
	if err != nil || len(entries) != 3 {
		t.Fatalf("clamped list = %v, %v", entries, err)
	}
}
