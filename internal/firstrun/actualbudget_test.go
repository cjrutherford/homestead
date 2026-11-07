package firstrun

import (
	"context"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func actualManifest() catalog.Manifest {
	return catalog.Manifest{Name: "actual-budget", Title: "Actual Budget", FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunActualBudget, Service: "actual-budget"}}
}

func stubActual(t *testing.T, outs []string, calls *[]string) {
	t.Helper()
	old, tries, pause := execIn, Tries, Pause
	t.Cleanup(func() { execIn, Tries, Pause = old, tries, pause })
	Tries, Pause = 3, time.Millisecond
	execIn = func(_ context.Context, container, _ string, args ...string) (string, error) {
		if container != "actual-budget-actual-budget" {
			t.Fatalf("container %s", container)
		}
		*calls = append(*calls, strings.Join(args, " "))
		o := outs[0]
		if len(outs) > 1 {
			outs = outs[1:]
		}
		return o, nil
	}
}

func TestActualBudgetSeedsTheOwnerAfterStartup(t *testing.T) {
	var calls []string
	stubActual(t, []string{"starting\n", "seeded\n"}, &calls)
	res, err := RunAs(context.Background(), actualManifest(), memSecrets{}, "akadmin")
	if err != nil || res != Created || len(calls) != 2 || !strings.HasSuffix(calls[1], " akadmin") {
		t.Fatalf("res=%q err=%v calls=%v", res, err, calls)
	}
}

func TestActualBudgetLeavesANamedUserAlone(t *testing.T) {
	var calls []string
	stubActual(t, []string{"present\n"}, &calls)
	if res, err := RunAs(context.Background(), actualManifest(), memSecrets{}, "akadmin"); err != nil || res != Ready {
		t.Fatalf("res=%q err=%v", res, err)
	}
}

func TestActualBudgetWithoutAKnownOwnerDoesNothing(t *testing.T) {
	var calls []string
	stubActual(t, []string{"seeded"}, &calls)
	if res, err := RunAs(context.Background(), actualManifest(), memSecrets{}, ""); err != nil || res != Ready || len(calls) != 0 {
		t.Fatalf("res=%q err=%v calls=%v", res, err, calls)
	}
}

// TestActualBudgetRoutinePinnedToVerifiedVersion: the script writes
// Actual's own account database; re-verify it before a version bump.
func TestActualBudgetRoutinePinnedToVerifiedVersion(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("actual-budget")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != ActualBudgetVerified || !strings.HasSuffix(m.Services[0].Image, ":"+ActualBudgetVerified) {
		t.Fatalf("actual-budget is %s but its owner routine was verified against %s. Before bumping: re-check the users and files tables (account-db, multiuser migration) and loginWithOpenIdFinalize in the image, then update ActualBudgetVerified (S447).", m.Version, ActualBudgetVerified)
	}
	if m.FirstRun == nil || m.FirstRun.Routine != catalog.FirstRunActualBudget {
		t.Fatal("actual-budget declares no owner first run")
	}
	if !strings.Contains(actualBudgetScript, "const VERIFIED = '"+ActualBudgetVerified+"'") {
		t.Fatal("script version constant is stale")
	}
}
