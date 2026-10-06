package firewall

import (
	"testing"
)

func examplePolicy() ServicePolicy {
	return ServicePolicy{
		Service: Service{Name: "dashboard", Protocol: ProtocolTCP, Port: 8080},
		Sources: []Source{
			{Zone: ZoneLocal},
			{Zone: ZoneOverlay, CIDR: "100.64.0.0/10"},
		},
	}
}

func TestExamplePolicyValid(t *testing.T) {
	if err := examplePolicy().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestZoneValidity(t *testing.T) {
	for _, zone := range []Zone{ZoneLocal, ZoneOverlay, ZonePublic} {
		if !zone.Valid() {
			t.Fatalf("%q invalid", zone)
		}
	}
	if Zone("dmz").Valid() {
		t.Fatal("dmz valid, want invalid")
	}
}

func TestServiceFailures(t *testing.T) {
	base := Service{Name: "web", Protocol: ProtocolTCP, Port: 80}
	for _, tc := range []struct {
		name   string
		mutate func(*Service)
	}{
		{"bad name", func(s *Service) { s.Name = "Bad!" }},
		{"bad protocol", func(s *Service) { s.Protocol = "sctp" }},
		{"port zero", func(s *Service) { s.Port = 0 }},
		{"port high", func(s *Service) { s.Port = 70000 }},
		{"end below start", func(s *Service) { s.Port, s.EndPort = 8080, 80 }},
		{"end high", func(s *Service) { s.EndPort = 70000 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.mutate(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("accepted, want error")
			}
		})
	}
	ranged := base
	ranged.Port, ranged.EndPort = 8000, 8100
	if err := ranged.Validate(); err != nil {
		t.Fatalf("ranged: %v", err)
	}
}

func TestSourceFailures(t *testing.T) {
	if err := (Source{Zone: "dmz"}).Validate(); err == nil {
		t.Fatal("bad zone accepted")
	}
	if err := (Source{Zone: ZoneLocal, CIDR: "192.168.1.5"}).Validate(); err == nil {
		t.Fatal("bare IP accepted, want CIDR notation")
	}
	if err := (Source{Zone: ZoneLocal, CIDR: "10.0.0.0/8"}).Validate(); err != nil {
		t.Fatalf("cidr: %v", err)
	}
}

func TestRuleFailures(t *testing.T) {
	base := Rule{
		Name:     "allow-lan",
		Source:   Source{Zone: ZoneLocal},
		Service:  Service{Name: "web", Protocol: ProtocolTCP, Port: 80},
		Action:   ActionAccept,
		Priority: 100,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("base: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Rule)
	}{
		{"bad name", func(r *Rule) { r.Name = "" }},
		{"bad iface", func(r *Rule) { r.Interfaces = []string{"eth0", ".."} }},
		{"bad source", func(r *Rule) { r.Source.Zone = "dmz" }},
		{"bad service", func(r *Rule) { r.Service.Port = 0 }},
		{"bad action", func(r *Rule) { r.Action = "reject" }},
		{"bad priority", func(r *Rule) { r.Priority = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.mutate(&r)
			if err := r.Validate(); err == nil {
				t.Fatal("accepted, want error")
			}
		})
	}
	iface := base
	iface.Interfaces = []string{"enp8s0", "tailscale0"}
	if err := iface.Validate(); err != nil {
		t.Fatalf("interfaces: %v", err)
	}
}

func TestRuleSetUniqueAndSorted(t *testing.T) {
	mk := func(name string, priority int) Rule {
		return Rule{Name: name, Source: Source{Zone: ZoneLocal},
			Service: Service{Name: "web", Protocol: ProtocolTCP, Port: 80},
			Action:  ActionAccept, Priority: priority}
	}
	dupes := RuleSet{Rules: []Rule{mk("a", 1), mk("a", 2)}}
	if err := dupes.Validate(); err == nil {
		t.Fatal("duplicates accepted")
	}
	set := RuleSet{Rules: []Rule{mk("b", 200), mk("a", 100), mk("c", 100)}}
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	sorted := set.Sorted()
	if sorted[0].Name != "a" || sorted[1].Name != "c" || sorted[2].Name != "b" {
		t.Fatalf("order = %v", []string{sorted[0].Name, sorted[1].Name, sorted[2].Name})
	}
	if len(set.Rules) != 3 || set.Rules[0].Name != "b" {
		t.Fatal("Sorted mutated the original")
	}
}

func TestPolicyDuplicateZones(t *testing.T) {
	policy := examplePolicy()
	policy.Sources = append(policy.Sources, Source{Zone: ZoneLocal})
	if err := policy.Validate(); err == nil {
		t.Fatal("duplicate zones accepted")
	}
	empty := ServicePolicy{Service: Service{Name: "web", Protocol: ProtocolTCP, Port: 80}}
	if err := empty.Validate(); err != nil {
		t.Fatalf("empty sources (blocked): %v", err)
	}
}
