package sso

import "testing"

// ADR-044: Authentik may sit under a path; its issuer and API join to it.
func TestValidateAuthentikURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://authentik.lan":            "https://authentik.lan",
		"https://homestead.lan/apps/auth/": "https://homestead.lan/apps/auth",
		"https://homestead.lan/apps/auth":  "https://homestead.lan/apps/auth",
	} {
		got, err := ValidateAuthentikURL(raw)
		if err != nil || got != want {
			t.Fatalf("%s = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"homestead.lan/apps/auth", "https://homestead.lan/apps/auth?x=1", "https://homestead.lan/../auth",
		"https://homestead.lan/apps/Auth", "https://homestead.lan/apps/auth#f"} {
		if got, err := ValidateAuthentikURL(raw); err == nil {
			t.Fatalf("%s accepted as %q", raw, got)
		}
	}
	if got := IssuerFor("https://homestead.lan/apps/auth", "homestead"); got != "https://homestead.lan/apps/auth/application/o/homestead/" {
		t.Fatalf("issuer = %s", got)
	}
}
