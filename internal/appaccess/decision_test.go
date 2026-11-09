package appaccess

import "testing"

func TestPlanRequiresExplicitChoiceForEveryPerson(t *testing.T) {
	people := []Person{{ID: 1, Active: true}, {ID: 2, Active: false}}
	valid := []Choice{{PersonID: 1, Role: User}, {PersonID: 2, Role: None}}
	if _, err := Build("example", people, valid, true, "verified-v1"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		choices []Choice
		admin   bool
	}{
		{"missing", valid[:1], true},
		{"duplicate", []Choice{{1, User}, {1, None}, {2, None}}, true},
		{"unknown", []Choice{{1, User}, {2, None}, {3, None}}, true},
		{"disabled grant", []Choice{{1, User}, {2, User}}, true},
		{"invalid role", []Choice{{1, "superuser"}, {2, None}}, true},
		{"unmapped admin", []Choice{{1, Admin}, {2, None}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Build("example", people, tc.choices, tc.admin, "verified-v1"); err == nil {
				t.Fatal("accepted invalid decisions")
			}
		})
	}
}

func TestOwnerOnlyAndInvalidPeople(t *testing.T) {
	if _, err := Build("example", nil, nil, true, "verified-v1"); err != nil {
		t.Fatalf("the owner alone is a valid household: %v", err)
	}
	for _, persons := range [][]Person{{{ID: 0, Active: true}}, {{ID: 1}, {ID: 1}}} {
		if _, err := Build("example", persons, nil, true, "verified-v1"); err == nil {
			t.Fatalf("accepted invalid people: %#v", persons)
		}
	}
}

func TestPlanDigestBindsRolesPeopleAndState(t *testing.T) {
	people := []Person{{ID: 1, Active: true}, {ID: 2, Active: true}}
	a, err := Build("example", people, []Choice{{1, User}, {2, None}}, true, "verified-v1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build("example", []Person{people[1], people[0]}, []Choice{{2, None}, {1, User}}, true, "verified-v1")
	if err != nil || a.Digest != b.Digest {
		t.Fatalf("ordering changed digest: %v", err)
	}
	for _, tc := range []struct {
		app     string
		people  []Person
		choices []Choice
		admin   bool
	}{
		{"example", people, []Choice{{1, Admin}, {2, None}}, true},
		{"other", people, []Choice{{1, User}, {2, None}}, true},
		{"example", []Person{{1, true}, {2, false}}, []Choice{{1, User}, {2, None}}, true},
		{"example", people, []Choice{{1, User}, {2, None}}, false},
	} {
		got, err := Build(tc.app, tc.people, tc.choices, tc.admin, "verified-v1")
		if err != nil || got.Digest == a.Digest {
			t.Fatalf("digest did not bind decision: %v", err)
		}
	}
	if err := Verify(a.Digest, "example", people, []Choice{{1, User}, {2, None}}, true, "verified-v1"); err != nil {
		t.Fatal(err)
	}
	if err := Verify(a.Digest, "example", people, []Choice{{1, User}, {2, None}}, true, "verified-v2"); err == nil {
		t.Fatal("changed integration accepted")
	}
	if err := Verify(a.Digest, "example", people, []Choice{{1, Admin}, {2, None}}, true, "verified-v1"); err == nil {
		t.Fatal("changed role accepted")
	}
	if _, err := Build("example", people, []Choice{{1, User}, {2, None}}, true, ""); err == nil {
		t.Fatal("missing integration accepted")
	}
}
