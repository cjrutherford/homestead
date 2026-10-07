package storage

import (
	"strings"
	"testing"
)

func TestClasses(t *testing.T) {
	ordered := Classes()
	if len(ordered) != 5 || ordered[0] != ClassApplication || ordered[4] != ClassCache {
		t.Fatalf("classes = %v", ordered)
	}
	for _, class := range ordered {
		if !class.Valid() {
			t.Fatalf("%q not valid", class)
		}
		profile, err := ProfileFor(class)
		if err != nil {
			t.Fatalf("ProfileFor %q: %v", class, err)
		}
		if profile.Class != class || profile.Label == "" || profile.Description == "" {
			t.Fatalf("profile = %+v", profile)
		}
	}
	if (Class("archive")).Valid() {
		t.Fatal("archive valid")
	}
	if _, err := ProfileFor("archive"); err == nil ||
		!strings.Contains(err.Error(), "unknown class") {
		t.Fatalf("err = %v", err)
	}
}

func TestProfiles(t *testing.T) {
	expect := map[Class]Profile{
		ClassApplication: {LocalOnly: false, Ephemeral: false, BackupDefault: true},
		ClassDatabase:    {LocalOnly: true, Ephemeral: false, BackupDefault: true},
		ClassBulk:        {LocalOnly: false, Ephemeral: false, BackupDefault: false},
		ClassBackup:      {LocalOnly: false, Ephemeral: false, BackupDefault: false},
		ClassCache:       {LocalOnly: false, Ephemeral: true, BackupDefault: false},
	}
	for class, want := range expect {
		got, err := ProfileFor(class)
		if err != nil {
			t.Fatalf("ProfileFor %q: %v", class, err)
		}
		if got.LocalOnly != want.LocalOnly || got.Ephemeral != want.Ephemeral || got.BackupDefault != want.BackupDefault {
			t.Fatalf("%s = %+v", class, got)
		}
	}
	// Exactly one class demands local mounts and one evicts: the
	// contract matrix stays reviewable as a whole.
	var localOnly, ephemeral int
	for _, class := range Classes() {
		profile, _ := ProfileFor(class)
		if profile.LocalOnly {
			localOnly++
		}
		if profile.Ephemeral {
			ephemeral++
		}
	}
	if localOnly != 1 || ephemeral != 1 {
		t.Fatalf("localOnly=%d ephemeral=%d", localOnly, ephemeral)
	}
}
