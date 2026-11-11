package testtools

import "testing"

func TestRequired(t *testing.T) {
	t.Setenv("HOMESTEAD_REQUIRE_AGE", "")
	t.Setenv("HOMESTEAD_REQUIRE_TOOLS", "")
	if Required("age") {
		t.Fatal("nothing set")
	}
	t.Setenv("HOMESTEAD_REQUIRE_TOOLS", "node, nft")
	if !Required("node") || !Required("nft") || Required("age") {
		t.Fatal("list not honoured")
	}
	t.Setenv("HOMESTEAD_REQUIRE_TOOLS", "all")
	if !Required("caddy") {
		t.Fatal("all not honoured")
	}
	t.Setenv("HOMESTEAD_REQUIRE_TOOLS", "")
	t.Setenv("HOMESTEAD_REQUIRE_AGE", "1")
	if !Required("age") || Required("node") {
		t.Fatal("HOMESTEAD_REQUIRE_AGE should mean age only")
	}
}

func TestSkipUnlessToolFindsSh(t *testing.T) {
	if SkipUnlessTool(t, "sh") == "" {
		t.Fatal("no path")
	}
}
