package exposure

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// refuseProbe answers nothing: every DNS layer fails fast without
// touching the network.
func refuseProbe(_ context.Context, _ string, _ dns.RecordType) ([]string, error) {
	return nil, errors.New("no such host")
}

func publicTarget() Target {
	return Target{
		Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"),
		Path: "/healthz", WantStatus: 200, Addr: "127.0.0.1",
	}
}

func approvePublic(t *testing.T, st *store.Store) {
	t.Helper()
	plan, err := PlanExposure(planRequest())
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	approval, err := Approve(plan, "owner", []string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := RecordApproval(st, approval); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func TestValidatePublicGates(t *testing.T) {
	ctx := context.Background()
	st := openApplyStore(t)
	target := publicTarget()
	if _, err := ValidatePublic(ctx, nil, "app.example.com", target, refuseProbe, nil); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := ValidatePublic(ctx, st, "", target, refuseProbe, nil); err == nil {
		t.Fatal("empty host accepted")
	}
	// Unapproved hosts never validate, however ready they look.
	if _, err := ValidatePublic(ctx, st, "app.example.com", target, refuseProbe, nil); err != ErrNoApproval {
		t.Fatalf("err = %v", err)
	}
	approvePublic(t, st)
	if _, err := ValidatePublic(ctx, st, "other.example.com", target, refuseProbe, nil); err == nil {
		t.Fatal("unapproved host accepted")
	}
	mismatched := target
	mismatched.Host = "other.example.com"
	if _, err := ValidatePublic(ctx, st, "app.example.com", mismatched, refuseProbe, nil); err == nil {
		t.Fatal("mismatched target accepted")
	}
	// Non-public approvals cannot validate public.
	local, err := PlanExposure(Request{
		Route: docker.Route{
			Host: "inner.example.com", Target: docker.Target{Host: "inner", Port: 8080},
			Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
		},
		To: docker.ExposureOverlay, Zone: "example.com",
		PublicIP: netip.MustParseAddr("203.0.113.7"),
	})
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	overlayApproval, err := Approve(local, "owner", []string{"100.64.0.0/10"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := RecordApproval(st, overlayApproval); err != nil {
		t.Fatalf("Record: %v", err)
	}
	overlayTarget := publicTarget()
	overlayTarget.Host = "inner.example.com"
	if _, err := ValidatePublic(ctx, st, "inner.example.com", overlayTarget, refuseProbe, nil); err == nil ||
		!strings.Contains(err.Error(), "not public") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidatePublicFailureVerdict(t *testing.T) {
	ctx := context.Background()
	st := openApplyStore(t)
	approvePublic(t, st)
	verdict, err := ValidatePublic(ctx, st, "app.example.com", publicTarget(), refuseProbe, nil)
	if err != nil {
		t.Fatalf("ValidatePublic: %v", err)
	}
	if verdict.Passed || !verdict.Approved || !verdict.ManagementClean {
		t.Fatalf("verdict = %+v", verdict)
	}
	if len(verdict.Failed) != 5 {
		t.Fatalf("failed = %v", verdict.Failed)
	}
	for _, check := range verdict.Layers.Checks {
		if check.OK {
			t.Fatalf("check = %+v", check)
		}
	}
	record, err := CurrentValidation(st, "app.example.com")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if record.Passed || len(record.Failed) != 5 || record.Host != "app.example.com" {
		t.Fatalf("record = %+v", record)
	}
}

func TestValidatePublicManagementTrip(t *testing.T) {
	ctx := context.Background()
	st := openApplyStore(t)
	approvePublic(t, st)
	probe := func(_ context.Context, name string, _ dns.RecordType) ([]string, error) {
		if name == "dockge.example.com" {
			return []string{"203.0.113.7"}, nil
		}
		return nil, errors.New("no such host")
	}
	verdict, err := ValidatePublic(ctx, st, "app.example.com", publicTarget(), probe, []string{"dockge.example.com", "", "app.example.com"})
	if err != nil {
		t.Fatalf("ValidatePublic: %v", err)
	}
	if verdict.ManagementClean {
		t.Fatalf("verdict = %+v", verdict)
	}
	found := false
	for _, name := range verdict.Failed {
		if name == "management:dockge.example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed = %v", verdict.Failed)
	}
	// The subject itself never trips its own guard.
	for _, name := range verdict.Failed {
		if name == "management:app.example.com" {
			t.Fatalf("failed = %v", verdict.Failed)
		}
	}
}

func TestCurrentValidationEmpty(t *testing.T) {
	st := openApplyStore(t)
	if _, err := CurrentValidation(st, "app.example.com"); err != ErrNoValidation {
		t.Fatalf("Current = %v", err)
	}
}

func TestCurrentValidationPassing(t *testing.T) {
	st := openApplyStore(t)
	if _, err := st.DB().Exec(
		`INSERT INTO public_validation (host, passed, failed, updated_at) VALUES (?, 1, '[]', '2026-10-09T12:00:00Z')`,
		"app.example.com",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	record, err := CurrentValidation(st, "app.example.com")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if !record.Passed || len(record.Failed) != 0 {
		t.Fatalf("record = %+v", record)
	}
}
