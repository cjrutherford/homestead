package docker

import (
	"testing"
)

func TestStackValidation(t *testing.T) {
	good := Stack{Name: "jellyfin", Dir: DirFor("jellyfin"), Services: []string{"app", "db"}, Enabled: true}
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Stack)
	}{
		{"bad name", func(s *Stack) { s.Name = "Bad!" }},
		{"wrong dir", func(s *Stack) { s.Dir = "/tmp/other" }},
		{"empty dir", func(s *Stack) { s.Dir = "" }},
		{"bad service", func(s *Stack) { s.Services = []string{"ok", "no spaces"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := good
			tc.mutate(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("accepted, want error")
			}
		})
	}
	// DirFor always validates against itself.
	if err := (Stack{Name: "x", Dir: DirFor("x")}).Validate(); err != nil {
		t.Fatalf("DirFor: %v", err)
	}
}

func labeledContainer(project, service, state string, oneOff bool) Container {
	labels := map[string]string{LabelProject: project, LabelService: service}
	if oneOff {
		labels[LabelOneOff] = "True"
	}
	return Container{ID: project + service + state, State: state, Labels: labels}
}

func TestObserveStacks(t *testing.T) {
	containers := []Container{
		labeledContainer("jellyfin", "app", "running", false),
		labeledContainer("jellyfin", "app", "exited", false),
		labeledContainer("jellyfin", "db", "running", false),
		labeledContainer("nextcloud", "app", "running", false),
		labeledContainer("jellyfin", "app", "running", true),
		{ID: "plain", State: "running"},
	}
	stacks := ObserveStacks(containers, []string{"app"})
	if len(stacks) != 2 || stacks[0].Name != "jellyfin" || stacks[1].Name != "nextcloud" {
		t.Fatalf("stacks = %+v", stacks)
	}
	jellyfin := stacks[0]
	if !jellyfin.Running || len(jellyfin.Services) != 2 {
		t.Fatalf("jellyfin = %+v", jellyfin)
	}
	for _, service := range jellyfin.Services {
		switch service.Name {
		case "app":
			if service.Running != 1 || service.Total != 2 || !service.Wanted {
				t.Fatalf("app = %+v", service)
			}
			if len(service.Containers) != 2 {
				t.Fatalf("app containers = %d, want 2 (one-off excluded)", len(service.Containers))
			}
		case "db":
			if service.Running != 1 || service.Wanted {
				t.Fatalf("db = %+v, want running but unwanted", service)
			}
		default:
			t.Fatalf("unexpected service %q", service.Name)
		}
	}
}

func TestObserveWantedSemantics(t *testing.T) {
	containers := []Container{labeledContainer("x", "a", "running", false)}
	all := ObserveStacks(containers, nil)
	if len(all) != 1 || !all[0].Services[0].Wanted {
		t.Fatalf("empty desired should want everything: %+v", all)
	}
	none := ObserveStacks(containers, []string{"b"})
	if len(none) != 1 || none[0].Services[0].Wanted {
		t.Fatalf("unlisted should be unwanted: %+v", none)
	}
	if got := ObserveStacks(nil, nil); len(got) != 0 {
		t.Fatalf("empty in = %v, want none", got)
	}
}
