package docker

import (
	"strings"
	"testing"
)

func TestIsPrivateName(t *testing.T) {
	private := []string{
		"homestead", "app.lan", "app.local", "app.localhost",
		"app.internal", "app.home.arpa", "app.test", "app.example",
		"app.invalid", "192.168.1.10", "::1",
	}
	for _, host := range private {
		if !IsPrivateName(Hostname(host)) {
			t.Fatalf("%q not private", host)
		}
	}
	public := []string{
		"example.com", "app.example.com", "homestead.example.com",
		"example.net", "my-app.io",
	}
	for _, host := range public {
		if IsPrivateName(Hostname(host)) {
			t.Fatalf("%q private", host)
		}
	}
}

func TestDirectChallengesValid(t *testing.T) {
	if !DirectChallengesValid("app.example.com", ExposurePublic) {
		t.Fatal("public route invalid")
	}
	cases := []struct {
		host     Hostname
		exposure Exposure
	}{
		{"app.lan", ExposurePublic},
		{"app.example.com", ExposureLocal},
		{"app.example.com", ExposureOverlay},
		{"homestead", ExposurePublic},
		{"192.168.1.10", ExposurePublic},
	}
	for _, tc := range cases {
		if DirectChallengesValid(tc.host, tc.exposure) {
			t.Fatalf("valid: %q %q", tc.host, tc.exposure)
		}
	}
}

func TestRecommendedTLS(t *testing.T) {
	if got := RecommendedTLS("app.example.com", ExposurePublic); got != TLSAuto {
		t.Fatalf("public = %q", got)
	}
	for _, tc := range []struct {
		host     Hostname
		exposure Exposure
	}{
		{"app.lan", ExposureLocal},
		{"app.lan", ExposurePublic},
		{"app.example.com", ExposureLocal},
		{"homestead", ExposureLocal},
	} {
		if got := RecommendedTLS(tc.host, tc.exposure); got != TLSInternal {
			t.Fatalf("got %q for %q %q", got, tc.host, tc.exposure)
		}
	}
}

func TestRenderRefusesAutoOnPrivate(t *testing.T) {
	r := Route{
		Host:     "app.lan",
		Target:   Target{Host: "app", Port: 8080},
		Exposure: ExposureLocal,
		TLS:      TLSAuto,
	}
	if _, err := RenderRoute(r); err == nil ||
		!strings.Contains(err.Error(), "automatic TLS needs a public name") {
		t.Fatalf("err = %v", err)
	}
	r.Host = "app.example.com"
	r.Exposure = ExposurePublic
	if _, err := RenderRoute(r); err != nil {
		t.Fatalf("public auto: %v", err)
	}
	// Explicit internal on private names still renders.
	r.Host = "app.lan"
	r.TLS = TLSInternal
	if _, err := RenderRoute(r); err != nil {
		t.Fatalf("private internal: %v", err)
	}
}
