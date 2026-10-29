package sso

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func (f *fakeAuthentik) client() *http.Client { return http.DefaultClient }

func linkOwner(t *testing.T, h *harness, ak *fakeAuthentik) {
	t.Helper()
	_, err := h.st.DB().Exec(`INSERT INTO sso_link (id, issuer, sub, identity_id, linked_at) VALUES (1, ?, 'user-1', 7, ?)`,
		IssuerFor(ak.srv.URL, AppSlug), stamp(h.now))
	if err != nil {
		t.Fatal(err)
	}
}

func applyDashboard(t *testing.T, h *harness) Plan {
	t.Helper()
	plan, err := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := runDef(t, h); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestDashboardPlanGroupsMembershipBindingAndDigest(t *testing.T) {
	h, ak := planHarness(t)
	linkOwner(t, h, ak)
	plan := applyDashboard(t, h)
	a := plan.Spec.Access
	if a == nil || a.OwnerSub != "user-1" || len(a.Bindings) != 2 || a.Bindings[0] != OwnerGroup || a.Bindings[1] != PeopleGroup || !hasGroup(a.Groups, PeopleGroup) {
		t.Fatalf("access in plan = %+v", a)
	}
	var kinds []string
	for _, o := range plan.Objects {
		kinds = append(kinds, o.Kind+" "+o.Name+" "+o.Action)
	}
	for _, want := range []string{"Group homestead-owner create", "Group homestead-people create", "Group member owner in homestead-owner create", "Binding application to homestead-owner create", "Binding application to homestead-people create"} {
		if !strings.Contains(strings.Join(kinds, "|"), want) {
			t.Errorf("plan lacks %q: %v", want, kinds)
		}
	}
	g := ak.groups[OwnerGroup]
	if g == nil || len(g["users"].([]any)) != 1 || g["users"].([]any)[0] != float64(7) {
		t.Fatalf("owner group = %v", g)
	}
	if ak.groups[PeopleGroup] == nil {
		t.Fatalf("the people group is created: %v", ak.groups)
	}
	if len(ak.bindings) != 2 || ak.bindings[0]["group"] != "g-"+OwnerGroup || ak.bindings[1]["group"] != "g-"+PeopleGroup {
		t.Fatalf("bindings = %v", ak.bindings)
	}
	// A different link changes the digest.
	other, _ := h.m.Plan(ctxb(), PlanRequest{}, "owner")
	if other.Digest != plan.Digest {
		t.Fatal("the same inputs must give the same digest")
	}
	if _, err := h.st.DB().Exec(`UPDATE sso_link SET sub = 'user-2'`); err != nil {
		t.Fatal(err)
	}
	if moved, _ := h.m.Plan(ctxb(), PlanRequest{}, "owner"); moved.Digest == plan.Digest {
		t.Fatal("the owner's subject is part of the digest")
	}
}

func TestDashboardApplyIsIdempotent(t *testing.T) {
	h, ak := planHarness(t)
	linkOwner(t, h, ak)
	applyDashboard(t, h)
	posts, patches := ak.posts, ak.patches
	applyDashboard(t, h)
	if ak.posts != posts || ak.patches != patches || len(ak.bindings) != 2 {
		t.Fatalf("second apply changed things: posts %d->%d patches %d->%d bindings %d", posts, ak.posts, patches, ak.patches, len(ak.bindings))
	}
}

func TestDashboardWithoutALinkDoesNotBindTheEmptyOwnerGroup(t *testing.T) {
	h, ak := planHarness(t)
	plan := applyDashboard(t, h)
	if plan.Spec.Access.OwnerSub != "" {
		t.Fatal("no link, no owner subject")
	}
	if _, ok := ak.groups[OwnerGroup]; !ok {
		t.Fatal("the owner group is created")
	}
	// The people group is bound even when empty: nobody outside the two
	// groups can sign in. The empty owner group is not, so the owner is
	// not locked out.
	if len(ak.bindings) != 1 || ak.bindings[0]["group"] != "g-"+PeopleGroup {
		t.Fatalf("only the people group is bound: %v", ak.bindings)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "|"), "No owner is linked yet") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}

func TestGroupsWithoutThePrefixAreNeverTouched(t *testing.T) {
	_, ak := planHarness(t)
	api := &API{Base: ak.srv.URL, Token: apiToken, Client: ak.client}
	for _, x := range []Access{{Groups: []string{"outsider"}}, {Bindings: []string{"authentik Admins"}}, {OwnerOf: []string{"outsider"}}} {
		if err := api.reconcileAccess(ctxb(), x, "app-1", func(string) {}); err == nil {
			t.Errorf("reconcile %+v must refuse", x)
		}
	}
	if _, err := api.FindGroup(ctxb(), "outsider"); err == nil {
		t.Error("FindGroup must refuse an unprefixed name")
	}
	if err := api.removeAccess(ctxb(), Access{Groups: []string{"outsider", OwnerGroup}}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ak.groups["outsider"]; !ok {
		t.Fatal("outsider was deleted")
	}
	if len(ak.deleted) != 0 {
		t.Fatalf("deleted %v", ak.deleted)
	}
}

func appAccess() Access {
	return Access{
		Groups:   []string{OwnerGroup, "homestead-demo-users", "homestead-demo-admins"},
		OwnerSub: "user-1", OwnerOf: []string{OwnerGroup, "homestead-demo-users", "homestead-demo-admins"},
		Bindings:     []string{"homestead-demo-users", "homestead-demo-admins", OwnerGroup},
		ClaimMapping: "Homestead: Demo groups", ClaimExpression: "return {}",
	}
}

func demoSpec(h *harness, ak *fakeAuthentik) Spec {
	spec, _ := h.m.BuildSpec(PlanRequest{})
	a := appAccess()
	spec.Access = &a
	return spec
}

func TestAppAccessApplyReadBackAndRemove(t *testing.T) {
	h, ak := planHarness(t)
	spec := demoSpec(h, ak)
	api := &API{Base: ak.srv.URL, Token: apiToken, Client: ak.client}
	var steps []string
	if err := api.Reconcile(ctxb(), spec, "s3cret", func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if len(ak.bindings) != 3 {
		t.Fatalf("bindings = %d", len(ak.bindings))
	}
	if mp, _ := ak.provider["property_mappings"].([]any); len(mp) != 4 || mp[3] != "m-Homestead: Demo groups" {
		t.Fatalf("the claim mapping goes last in the provider: %v", ak.provider["property_mappings"])
	}
	rep, err := api.ReadAccess(ctxb(), appAccess(), spec.AppSlug)
	if err != nil || len(rep.Drift) != 0 || rep.Open || rep.ClaimMapping != "ok" {
		t.Fatalf("read-back = %+v err %v", rep, err)
	}
	// Idempotent.
	posts := ak.posts
	if err := api.Reconcile(ctxb(), spec, "s3cret", func(string) {}); err != nil || ak.posts != posts {
		t.Fatalf("second reconcile posts %d->%d err %v", posts, ak.posts, err)
	}
	// Drift: a binding removed in Authentik is reported, not reverted.
	ak.bindings = ak.bindings[:1]
	ak.mappings["Homestead: Demo groups"]["expression"] = "return {'groups': ['everyone']}"
	rep, _ = api.ReadAccess(ctxb(), appAccess(), spec.AppSlug)
	if len(rep.Drift) < 3 || rep.ClaimMapping != "differs" || len(ak.bindings) != 1 {
		t.Fatalf("drift = %+v", rep)
	}
	ak.bindings = nil
	if rep, _ = api.ReadAccess(ctxb(), appAccess(), spec.AppSlug); !rep.Open {
		t.Fatal("no bindings means open to every Authentik user")
	}
	// Remove deletes the app's two groups and the mapping, never the owner group or others.
	if err := api.Remove(ctxb(), spec, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ak.groups["homestead-demo-users"]; ok {
		t.Fatal("users group remains")
	}
	if _, ok := ak.groups["homestead-demo-admins"]; ok {
		t.Fatal("admins group remains")
	}
	if _, ok := ak.groups[OwnerGroup]; !ok {
		t.Fatal("the shared owner group was deleted")
	}
	if _, ok := ak.groups["outsider"]; !ok || len(ak.mappings) != 0 {
		t.Fatalf("groups %v mappings %v", ak.groups, ak.mappings)
	}
}

// S338: applying the plan asks the People service to fill the people group
// once it exists, and a removal never deletes it.
func TestApplySyncsPeopleAfterTheGroupExists(t *testing.T) {
	h, ak := planHarness(t)
	var existed bool
	calls := 0
	h.m.SyncPeople = func(_ context.Context, api *API) error {
		calls++
		g, err := api.FindGroup(ctxb(), PeopleGroup)
		existed = err == nil && g != nil
		return nil
	}
	applyDashboard(t, h)
	if calls != 1 || !existed {
		t.Fatalf("SyncPeople calls=%d, group existed=%v", calls, existed)
	}
	_ = ak
	if got := (Access{Groups: []string{OwnerGroup, PeopleGroup, "homestead-demo-users"}}).RemovableGroups(); len(got) != 1 || got[0] != "homestead-demo-users" {
		t.Fatalf("removable = %v: the owner and people groups are shared", got)
	}
}
