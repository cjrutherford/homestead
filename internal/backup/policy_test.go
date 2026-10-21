package backup

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"homestead/internal/store"
)

func TestPolicyValidate(t *testing.T) {
	if err := ProposedPolicy().Validate(); err != nil {
		t.Fatalf("proposed: %v", err)
	}
	for _, bad := range []Policy{
		{TimeOfDay: "25:00", Retain: 7},
		{TimeOfDay: "2am", Retain: 7},
		{TimeOfDay: "02:00", Retain: 0},
		{TimeOfDay: "02:00", Retain: MaxPolicyRetain + 1},
	} {
		if err := bad.Validate(); !errors.Is(err, ErrInvalidPolicy) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if got := (Policy{TimeOfDay: "3:05"}).Normalize().TimeOfDay; got != "03:05" {
		t.Fatalf("normalize: %q", got)
	}
}

func TestPoliciesSaveKeepsTheAnchorOnRetentionEdits(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	policies := NewPolicies(st)
	if _, ok, err := policies.Get(); ok || err != nil {
		t.Fatalf("empty store: %v %v", ok, err)
	}
	first, err := policies.Save(ProposedPolicy(), "alice")
	if err != nil || first.Actor != "alice" || !first.Enabled || first.TimeOfDay != "02:00" {
		t.Fatalf("save: %+v %v", first, err)
	}
	time.Sleep(2 * time.Millisecond)
	kept, err := policies.Save(Policy{TimeOfDay: "02:00", Retain: 14, Enabled: true}, "alice")
	if err != nil || kept.Retain != 14 || !kept.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("retention edit moved the anchor: %+v %v", kept, err)
	}
	time.Sleep(2 * time.Millisecond)
	moved, err := policies.Save(Policy{TimeOfDay: "03:30", Retain: 14, Enabled: true}, "alice")
	if err != nil || !moved.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("time change kept the anchor: %+v %v", moved, err)
	}
	if _, err := policies.Save(Policy{TimeOfDay: "x", Retain: 7}, "alice"); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("invalid saved: %v", err)
	}
}
