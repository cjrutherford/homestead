package appsso

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// S313: a public name added later joins Nextcloud's trusted domains at
// the next free index, a listed name is not added twice, and a removed
// name is deleted by its index, gaps and all.
func TestTrustHostsNextcloudAddsAndRemovesByIndex(t *testing.T) {
	f := stubDocker(t)
	m := manifest(t, "nextcloud")
	f.fail = func(c call) (string, error) {
		if strings.Contains(strings.Join(c.args, " "), "config:system:get trusted_domains") {
			return `{"0":"nextcloud.lan","1":"nextcloud.local","3":"old.example.com"}`, nil
		}
		return "", nil
	}
	err := TrustHosts(context.Background(), m, "https://nextcloud.lan",
		[]string{"nextcloud.lan", "nextcloud.local", "nextcloud.homestead.example.com"}, []string{"old.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	var occ []string
	for _, c := range f.calls {
		if c.container != "nextcloud-nextcloud" || c.user != "www-data" {
			t.Fatalf("call %+v", c)
		}
		joined := strings.Join(c.args, " ")
		if strings.Contains(joined, "config:system:set") || strings.Contains(joined, "config:system:delete") {
			occ = append(occ, strings.TrimPrefix(joined, "php occ --no-interaction "))
		}
	}
	want := "config:system:delete trusted_domains 3|config:system:set trusted_domains 2 --value=nextcloud.homestead.example.com"
	if strings.Join(occ, "|") != want {
		t.Fatalf("occ = %v", occ)
	}
}

func TestTrustHostsLeavesOtherAppsAlone(t *testing.T) {
	f := stubDocker(t)
	for _, app := range []string{"immich", "jellyfin", "authentik"} {
		if err := TrustHosts(context.Background(), manifest(t, app), "", []string{"x.example.com"}, nil); err != nil {
			t.Fatal(err)
		}
		if HostSettings(manifest(t, app), []string{"x.example.com"}, nil) != nil {
			t.Fatalf("%s has host settings", app)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls = %+v", f.calls)
	}
	got := HostSettings(manifest(t, "nextcloud"), []string{"a.example.com"}, []string{"b.example.com"})
	if len(got) != 2 || !strings.Contains(got[0], "trusted_domains gets a.example.com") || !strings.Contains(got[1], "b.example.com is taken out") {
		t.Fatalf("settings = %v", got)
	}
}

// A Nextcloud whose owner must finish setup is refused with the guided
// error, before any change.
func TestTrustHostsNextcloudNotSetUp(t *testing.T) {
	f := stubDocker(t)
	f.fail = func(c call) (string, error) {
		if strings.Contains(strings.Join(c.args, " "), "status --output=json") {
			return `{"installed":false}`, nil
		}
		return "", nil
	}
	err := TrustHosts(context.Background(), manifest(t, "nextcloud"), "https://nextcloud.lan", []string{"n.example.com"}, nil)
	if !errors.Is(err, ErrNextcloudNotSetUp) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c.args, " "), "config:system") {
			t.Fatalf("changed: %+v", c)
		}
	}
}
