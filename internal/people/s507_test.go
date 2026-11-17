package people

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type drop struct{ app, user, email string }

func dropRig(t *testing.T, fail error) (*rig, *[]drop) {
	r := newRig(t)
	var calls []drop
	r.svc.DropAppAdmin = func(_ context.Context, app, username, email string) error {
		calls = append(calls, drop{app, username, email})
		return fail
	}
	return r, &calls
}

func TestS507RevokeRemoveDisableDropNextcloudAdmin(t *testing.T) {
	r, calls := dropRig(t, nil)
	p, _ := r.add("ada", Grant{"nextcloud", RoleAdmin}, Grant{"immich", RoleUser})
	if _, err := r.svc.Revoke(bg, "owner", p.ID, "immich"); err != nil || len(*calls) != 0 {
		t.Fatalf("revoking another app must not drop: %v %v", err, *calls)
	}
	if _, err := r.svc.Revoke(bg, "owner", p.ID, "nextcloud"); err != nil || len(*calls) != 1 || (*calls)[0].app != "nextcloud" || (*calls)[0].user != "ada" {
		t.Fatalf("%v %v", err, *calls)
	}
	q, _ := r.add("bob", Grant{"nextcloud", RoleAdmin})
	if _, err := r.svc.Grant(bg, "owner", q.ID, Grant{"nextcloud", RoleUser}); err != nil || len(*calls) != 2 {
		t.Fatalf("demotion to user must drop: %v %v", err, *calls)
	}
	if err := r.svc.SetActive(bg, "owner", q.ID, false); err != nil || len(*calls) != 3 {
		t.Fatalf("disable must drop: %v %v", err, *calls)
	}
	if err := r.svc.Remove(bg, "owner", q.ID); err != nil || len(*calls) != 4 {
		t.Fatalf("remove must drop: %v %v", err, *calls)
	}
}

func TestS507DropFailureDoesNotBlockAndIsReported(t *testing.T) {
	r, calls := dropRig(t, errors.New("occ failed"))
	p, _ := r.add("ada", Grant{"nextcloud", RoleAdmin})
	var w Warnings
	if _, err := r.svc.Revoke(WithWarnings(bg, &w), "owner", p.ID, "nextcloud"); err != nil {
		t.Fatalf("must not block: %v", err)
	}
	if len(*calls) != 1 || len(w.List()) != 1 || !strings.Contains(w.List()[0], "next sign-in") {
		t.Fatalf("%v %v", *calls, w.List())
	}
	if !strings.Contains(strings.Join(r.logs, "\n"), "people.app-admin-drop|error") {
		t.Fatalf("%q", r.logs)
	}
	if v, _ := r.svc.person(p.ID); len(v.Grants) != 0 {
		t.Fatalf("the revoke itself must stand: %+v", v.Grants)
	}
}
