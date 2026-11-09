package doctor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRunLiveHealthyKeepsProbeOrderAndMarksLive(t *testing.T) {
	probes := []Probe{
		{Name: "a", Run: func(context.Context) []Check { return []Check{{Name: "a", Status: StatusOK}} }},
		{Name: "b", Run: func(context.Context) []Check {
			return []Check{{Name: "b1", Status: StatusWarn}, {Name: "b2", Status: StatusOK}}
		}},
	}
	got := RunLive(context.Background(), time.Second, probes)
	names := []string{}
	for _, c := range got {
		names = append(names, c.Name)
		if !c.Live {
			t.Errorf("%s not marked live", c.Name)
		}
	}
	if strings.Join(names, ",") != "a,b1,b2" {
		t.Fatalf("order = %v", names)
	}
}

func TestRunLiveSlowProbeIsUnknownNotAHang(t *testing.T) {
	// A probe that ignores its context still cannot hold the report.
	block := make(chan struct{})
	defer close(block)
	probes := []Probe{
		{Name: "slow", Group: GroupTunnel, Timeout: 50 * time.Millisecond, Run: func(context.Context) []Check {
			<-block
			return nil
		}},
		{Name: "fast", Run: func(context.Context) []Check { return []Check{{Name: "fast", Status: StatusOK}} }},
	}
	start := time.Now()
	got := RunLive(context.Background(), time.Second, probes)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("took %s", time.Since(start))
	}
	if len(got) != 2 || got[0].Name != "slow" || got[0].Status != StatusUnknown || got[0].Group != GroupTunnel || got[1].Status != StatusOK {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(got[0].Detail, "did not answer") {
		t.Fatalf("detail = %q", got[0].Detail)
	}
}

func TestRunLiveRespectsTheTotalBudget(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	var probes []Probe
	for _, n := range []string{"p1", "p2", "p3"} {
		probes = append(probes, Probe{Name: n, Timeout: time.Minute, Run: func(context.Context) []Check {
			<-block
			return nil
		}})
	}
	start := time.Now()
	got := RunLive(context.Background(), 80*time.Millisecond, probes)
	if time.Since(start) > time.Second {
		t.Fatalf("budget ignored: %s", time.Since(start))
	}
	for _, c := range got {
		if c.Status != StatusUnknown {
			t.Fatalf("%+v", c)
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
}

func TestRunLivePanicIsContained(t *testing.T) {
	got := RunLive(context.Background(), time.Second, []Probe{{Name: "boom", Timeout: 100 * time.Millisecond, Run: func(context.Context) []Check { panic("x") }}})
	if len(got) > 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestUnknownDoesNotChangeOverall(t *testing.T) {
	var r Report
	r.Add(Check{Name: "memory", Status: StatusOK}, Check{Name: "x", Status: StatusUnknown})
	if r.Overall != StatusOK {
		t.Fatalf("overall = %s", r.Overall)
	}
	r.Add(Check{Name: "y", Status: StatusWarn})
	if r.Overall != StatusWarn {
		t.Fatalf("overall = %s", r.Overall)
	}
}

func TestAnnotateGivesEveryKnownNameAGroupTitleWhyAndFix(t *testing.T) {
	for name := range metas {
		checks := []Check{{Name: name, Status: StatusWarn}, {Name: name + ":suffix", Status: StatusWarn}}
		Annotate(checks)
		for _, c := range checks {
			if c.Group == "" || c.Title == "" || c.Why == "" || c.Fix == nil || c.Fix.Text == "" {
				t.Errorf("%s incomplete: %+v", c.Name, c)
			}
		}
	}
}

func TestAnnotateGroupsAndKeepsAnExplicitFix(t *testing.T) {
	checks := []Check{
		{Name: "root-disk"}, {Name: "default-gateway"}, {Name: "local-ca"}, {Name: "tunnel-connector"},
		{Name: "authentik"}, {Name: "app-containers:immich"}, {Name: "nonsense"},
		{Name: "app-access:jellyfin", Fix: &Fix{Text: "mine"}},
	}
	Annotate(checks)
	want := []string{GroupStorage, GroupNetwork, GroupCertificates, GroupTunnel, GroupSignIn, GroupApps, GroupSystem, GroupSignIn}
	for i, c := range checks {
		if c.Group != want[i] {
			t.Errorf("%s group = %s, want %s", c.Name, c.Group, want[i])
		}
	}
	if checks[7].Fix.Text != "mine" || checks[6].Title != "nonsense" || checks[6].Fix.Command == "" {
		t.Fatalf("%+v %+v", checks[7], checks[6])
	}
	if !strings.Contains(checks[5].Title, "immich") {
		t.Fatalf("title %q", checks[5].Title)
	}
}

func TestAnnotateFillsTheAppIntoACommand(t *testing.T) {
	checks := []Check{{Name: "app-access:jellyfin"}}
	Annotate(checks)
	if checks[0].Fix.Command != "homesteadctl apps sign-in plan jellyfin" {
		t.Fatalf("command = %q", checks[0].Fix.Command)
	}
}

func TestByGroupOrdersGroupsAndDropsEmpty(t *testing.T) {
	groups := ByGroup([]Check{{Name: "a", Group: GroupApps}, {Name: "b", Group: GroupSystem}, {Name: "c", Group: GroupApps}})
	if len(groups) != 2 || groups[0].Group != GroupSystem || len(groups[1].Checks) != 2 {
		t.Fatalf("%+v", groups)
	}
}

func TestJSONKeepsTheOldFieldsAndAddsNewOnes(t *testing.T) {
	checks := []Check{{Name: "memory", Status: StatusOK, Detail: "8 GiB"}}
	Annotate(checks)
	raw, _ := json.Marshal(Report{Overall: StatusOK, Checks: checks, Live: true})
	var generic struct {
		Overall string
		Live    bool
		Checks  []map[string]any
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	c := generic.Checks[0]
	for _, key := range []string{"name", "status", "detail", "group", "title", "why", "fix"} {
		if _, ok := c[key]; !ok {
			t.Errorf("missing %s in %s", key, raw)
		}
	}
	if generic.Overall != "ok" || !generic.Live {
		t.Fatalf("%s", raw)
	}
}
