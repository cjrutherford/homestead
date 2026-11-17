package sso

import "testing"

func TestPublicLinkMovesOnlyTheOrigin(t *testing.T) {
	link := "https://homestead.lan/apps/auth/if/flow/homestead-recovery/?flow_token=abc"
	pub := "https://home.example.com/apps/auth"
	if got, want := PublicLink(link, pub), "https://home.example.com/apps/auth/if/flow/homestead-recovery/?flow_token=abc"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, c := range [][2]string{{"", pub}, {link, ""}, {link, "  "}, {"/relative/only", pub}, {link, "/relative"}, {link, "https://"}, {link, "javascript://example"}} {
		if got := PublicLink(c[0], c[1]); got != "" {
			t.Fatalf("PublicLink(%q, %q) = %q, want empty", c[0], c[1], got)
		}
	}
}
