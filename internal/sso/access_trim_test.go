package sso

import "testing"

func TestClaimScopeDefaultsToGroups(t *testing.T) {
	if got := (Access{}).claimScope(); got != GroupsScope {
		t.Fatalf("default scope = %q", got)
	}
	if got := (Access{ClaimScope: "profile"}).claimScope(); got != "profile" {
		t.Fatalf("scope = %q", got)
	}
}

func TestBoundScopesDropsProfileForTrimmedApps(t *testing.T) {
	sp := Spec{Scopes: Scopes}
	if len(sp.boundScopes()) != 3 {
		t.Fatalf("scopes = %v", sp.boundScopes())
	}
	sp.Access = &Access{ClaimScope: "profile"}
	for _, s := range sp.boundScopes() {
		if s == "profile" {
			t.Fatalf("profile still bound: %v", sp.boundScopes())
		}
	}
	if len(sp.boundScopes()) != 2 {
		t.Fatalf("scopes = %v", sp.boundScopes())
	}
}
