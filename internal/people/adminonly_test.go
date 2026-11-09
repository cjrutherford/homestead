package people

import (
	"errors"
	"strings"
	"testing"
)

func TestAdminOnlyAppRefusesCanOpen(t *testing.T) {
	s := &Service{AdminOnly: func(app string) bool { return app == "uptime-kuma" }}
	apps := []string{"navidrome", "uptime-kuma"}
	err := s.checkGrant(Grant{App: "uptime-kuma", Role: RoleUser}, apps)
	if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "administrators only") {
		t.Fatalf("want a clear refusal, got %v", err)
	}
	if err := s.checkGrant(Grant{App: "uptime-kuma", Role: RoleAdmin}, apps); err != nil {
		t.Fatal(err)
	}
	if err := s.checkGrant(Grant{App: "navidrome", Role: RoleUser}, apps); err != nil {
		t.Fatal(err)
	}
}
