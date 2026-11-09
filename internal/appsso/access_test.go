package appsso

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/sso"
	"homestead/internal/store"
)

func TestPlanListsGroupsMembershipBindingsAndClaimMapping(t *testing.T) {
	in := inputs(t, "nextcloud")
	in.OwnerSub = "user-1"
	p, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	a := p.Access
	if a == nil || a.OwnerSub != "user-1" {
		t.Fatalf("access = %+v", a)
	}
	for _, want := range []string{"homestead-nextcloud-users", "homestead-nextcloud-admins", "homestead-owner"} {
		if !contains(a.Groups, want) || !contains(a.Bindings, want) || !contains(a.OwnerOf, want) {
			t.Errorf("%s is not a group, a binding and an owner membership: %+v", want, a)
		}
	}
	if a.ClaimMapping != "Homestead: Nextcloud groups" || !strings.Contains(a.ClaimExpression, `in ("homestead-nextcloud-users"`) {
		t.Fatalf("claim mapping: %+v", a)
	}
	text := strings.Join(p.Changes, "\n")
	for _, want := range []string{"Group homestead-nextcloud-users", "Group homestead-nextcloud-admins", "Bind the application to group homestead-owner", "groups claim", "People page (coming next)"} {
		if !strings.Contains(text, want) {
			t.Errorf("changes lack %q:\n%s", want, text)
		}
	}
	if p.Spec(dashboard).Access != p.Access {
		t.Fatal("the Authentik spec must carry the access setup")
	}
	// The owner's link is part of the digest; the same inputs repeat it.
	again, _ := BuildPlan(in, true)
	in.OwnerSub = ""
	other, _ := BuildPlan(in, true)
	if again.Digest != p.Digest || other.Digest == p.Digest {
		t.Fatal("digest must bind the access setup")
	}
	if !strings.Contains(strings.Join(other.Changes, "\n"), "No owner is linked yet") {
		t.Fatal("a plan without a linked owner says so")
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func TestExistingSignInWithoutBindingsIsWarnedAndNotChanged(t *testing.T) {
	in := inputs(t, "immich")
	p, _ := BuildPlan(in, true)
	in.Applied = &Record{Package: "immich", Issuer: p.Issuer, RedirectURIs: p.RedirectURIs} // from before S314
	st := BuildStatus(in, true)
	if !st.OpenToAll || !st.Replan || !st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	re, _ := BuildPlan(in, true)
	if !strings.Contains(strings.Join(re.Changes, "\n"), "open to every Authentik user") {
		t.Fatalf("the re-plan says what changes: %v", re.Changes)
	}
	// Reading status and planning change nothing: only the record decides.
	if got, _ := BuildPlan(in, true); got.Digest != re.Digest {
		t.Fatal("planning is stable")
	}
	in.Applied.Access = p.Access
	if st := BuildStatus(in, true); st.OpenToAll || st.Replan {
		t.Fatalf("after apply: %+v", st)
	}
}

func TestAdaptersMapTheAdminGroup(t *testing.T) {
	g := groupNames("x")
	for _, name := range []string{"nextcloud", "jellyfin", "immich"} {
		m := manifest(t, name)
		a, err := AdapterFor(*m.SSO.OIDC)
		if err != nil {
			t.Fatal(err)
		}
		rm := a.Roles(groupNames(name))
		if !rm.Mapped || !strings.Contains(rm.Note, "homestead-"+name+"-admins") {
			t.Errorf("%s role mapping: %+v", name, rm)
		}
	}
	nc := nextcloud{}.Roles(g)
	if nc.AdminAlias != "admin" {
		t.Fatalf("nextcloud: %+v", nc)
	}
	expr := claimExpression(g, nc)
	if !strings.Contains(expr, `"homestead-x-admins" in names`) || !strings.Contains(expr, `append("admin")`) || !strings.Contains(expr, `in ("homestead-x-users"`) {
		t.Fatalf("expression:\n%s", expr)
	}
	if im := claimExpression(g, immich{}.Roles(g)); !strings.Contains(im, `claims["immich_role"] = "admin" if admin else "user"`) {
		t.Fatalf("immich expression:\n%s", im)
	}
	xml, _ := jellyfinConfig(&Settings{Issuer: "https://a/", ClientID: "c", ClientSecret: "s", Groups: g})
	xml = strings.Join(strings.Fields(xml), "")
	for _, want := range []string{"<AdminRoles><string>homestead-x-admins</string></AdminRoles>", "<RoleClaim>groups</RoleClaim>", "<EnableAuthorization>true", "<string>homestead-x-users</string>", "<string>groups</string>"} {
		if !strings.Contains(xml, want) {
			t.Errorf("jellyfin config lacks %q:\n%s", want, xml)
		}
	}
}

func TestAdapterWiresGroupsInTheApps(t *testing.T) {
	f := stubDocker(t)
	m := manifest(t, "immich")
	a, _ := AdapterFor(*m.SSO.OIDC)
	if err := a.Enable(context.Background(), Target{Manifest: m, Container: "immich-server"}, Settings{Issuer: "https://a/", ClientID: "c", ClientSecret: clientSecret}); err != nil {
		t.Fatal(err)
	}
	if s := f.calls[0].stdin; !strings.Contains(s, `"roleClaim":"immich_role"`) || !strings.Contains(s, "openid email profile groups") {
		t.Fatalf("immich: %s", s)
	}
}

func TestAnAppWhoseAdminCannotBeMappedSaysSo(t *testing.T) {
	p := Plan{Title: "Demo"}
	noteRoles(&p, RoleMapping{Note: "Demo has no role claim."})
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "admins stay local") {
		t.Fatalf("warnings = %v", p.Warnings)
	}
}

func TestOffPlanListsAndRemovesTheAppsGroups(t *testing.T) {
	stubDocker(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, catalog.OverrideFileName), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := Runtime{Records: Records{DB: st.DB()}, Secrets: &memSecrets{m: map[string]string{}}, StackDir: func(string) string { return dir },
		RootPEM: func() ([]byte, error) { return []byte("pem"), nil }}
	in := inputs(t, "immich")
	on, _ := BuildPlan(in, true)
	ak := &fakeAuthentik{}
	def, _ := Definition(rt, on, in.Manifest, dashboard, ak, "owner")
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", step.Name, err)
		}
	}
	if ak.specs[0].Access == nil || ak.specs[0].Access.ClaimMapping == "" {
		t.Fatalf("spec access = %+v", ak.specs[0].Access)
	}
	rec, ok, _ := rt.Records.Get("immich")
	if !ok || rec.Access == nil || !sameAccess(rec.Access, on.Access) {
		t.Fatalf("the record keeps the access that was applied: %+v", rec)
	}
	in.Applied = &rec
	off, _ := BuildPlan(in, false)
	text := strings.Join(off.Changes, "\n")
	if !strings.Contains(text, "homestead-immich-users") || !strings.Contains(text, "homestead-immich-admins") || !strings.Contains(text, "homestead-owner stays") {
		t.Fatalf("off plan: %v", off.Changes)
	}
	def, _ = Definition(rt, off, in.Manifest, dashboard, ak, "owner")
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", step.Name, err)
		}
	}
	got := ak.removedSpec.Access.RemovableGroups()
	if len(got) != 2 || got[0] != "homestead-immich-users" || got[1] != "homestead-immich-admins" {
		t.Fatalf("removed groups = %v", got)
	}
	var _ = sso.OwnerGroup
}
