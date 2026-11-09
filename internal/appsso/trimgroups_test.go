package appsso

import (
	"strings"
	"testing"

	"homestead/internal/catalog"
)

// S496: FreshRSS's Apache module stores the whole token in a 16400-byte
// cache entry, so the owner's every-app group list must not reach it.
func TestFreshRSSTrimsGroupsToItsOwn(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("freshrss")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	g := groupNames("freshrss")
	rm := a.Roles(g)
	if !rm.TrimGroups {
		t.Fatal("FreshRSS must trim the groups claim")
	}
	expr := claimExpression(g, rm)
	if strings.Contains(expr, `startswith("homestead-")`) || !strings.Contains(expr, `"homestead-freshrss-users"`) {
		t.Fatalf("expression is not trimmed:\n%s", expr)
	}
	if !strings.Contains(expr, `"preferred_username": request.user.username`) {
		t.Fatalf("the trimmed mapping must carry the profile claims:\n%s", expr)
	}
	x := buildAccess("freshrss", m.Title, "", rm)
	if x.ClaimScope != "profile" {
		t.Fatalf("claim scope = %q, want profile", x.ClaimScope)
	}
	// An app that asks for groups keeps the wide claim and the groups scope.
	wide := buildAccess("nextcloud", "Nextcloud", "", nextcloud{}.Roles(groupNames("nextcloud")))
	if wide.ClaimScope != "" || !strings.Contains(wide.ClaimExpression, "startswith") {
		t.Fatalf("a non-trimming app changed: %+v", wide)
	}
}
