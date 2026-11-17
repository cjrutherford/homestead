package appsso

import (
	"strings"
	"testing"

	"homestead/internal/catalog"
)

// S499: every OIDC app's claim lists only its own groups and answers to the
// profile scope, replacing the standard profile mapping (S496 did this for
// FreshRSS alone). The owner holds every app group, so a wide claim can
// overflow an app's session store.
func TestEveryOIDCAppTrimsGroupsToItsOwn(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, name := range entries.Names() {
		m, err := entries.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		if m.SSO.OIDC == nil {
			continue
		}
		a, err := AdapterFor(*m.SSO.OIDC)
		if err != nil {
			continue
		}
		g := groupNames(m.Name)
		rm := a.Roles(g)
		x := buildAccess(m.Name, m.Title, "", rm)
		seen++
		if strings.Contains(x.ClaimExpression, `startswith("homestead-")`) || !strings.Contains(x.ClaimExpression, `"`+g.Users+`"`) {
			t.Errorf("%s: expression is not trimmed:\n%s", m.Name, x.ClaimExpression)
		}
		if !strings.Contains(x.ClaimExpression, `"preferred_username": request.user.username`) {
			t.Errorf("%s: the trimmed mapping must carry the profile claims", m.Name)
		}
		if x.ClaimScope != "profile" {
			t.Errorf("%s: claim scope = %q, want profile", m.Name, x.ClaimScope)
		}
		if rm.Mapped && rm.AdminAlias == "" && !strings.Contains(x.ClaimExpression, g.Admins) {
			t.Errorf("%s: the admin group is missing", m.Name)
		}
	}
	if seen < 10 {
		t.Fatalf("only %d OIDC apps checked", seen)
	}
}

func TestNextcloudKeepsOwnGroupsOnly(t *testing.T) {
	g := groupNames("nextcloud")
	x := buildAccess("nextcloud", "Nextcloud", "", nextcloud{}.Roles(g))
	if !strings.Contains(x.ClaimExpression, `in ("homestead-nextcloud-users", "homestead-nextcloud-admins", "homestead-owner")`) {
		t.Fatalf("expression:\n%s", x.ClaimExpression)
	}
}
