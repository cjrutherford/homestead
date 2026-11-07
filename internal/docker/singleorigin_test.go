package docker

import (
	"strings"
	"testing"
)

func TestSingleOriginRedirectsLocalNamesWhilePublished(t *testing.T) {
	r := Route{Host: "gitea.lan", Target: Target{Host: "gitea", Port: 3000}, Exposure: ExposureLocal, TLS: TLSInternal, SingleOrigin: true}
	out, err := RenderRoute(r)
	if err != nil || strings.Contains(out, "redir") || !strings.Contains(out, "reverse_proxy gitea:3000") {
		t.Fatalf("unpublished must serve the app: %v\n%s", err, out)
	}
	r.Tunnel = []Hostname{"gitea.example.com"}
	out, err = RenderRoute(r)
	if err != nil {
		t.Fatal(err)
	}
	main, entry, ok := strings.Cut(out, "# Tunnel entry")
	if !ok || !strings.Contains(main, "gitea.lan, gitea.local {") || !strings.Contains(main, "redir https://gitea.example.com{uri} 307") || strings.Contains(main, "reverse_proxy") {
		t.Fatalf("local names must redirect and not serve:\n%s", out)
	}
	if !strings.Contains(entry, "http://gitea.example.com:") || !strings.Contains(entry, "reverse_proxy gitea:3000") || strings.Contains(entry, "redir") {
		t.Fatalf("the public name must keep serving the app:\n%s", entry)
	}
	if to, from := r.LANRedirect(); to != "https://gitea.example.com" || len(from) != 2 {
		t.Fatalf("LANRedirect = %q %v", to, from)
	}
	r.Tunnel = nil
	out, _ = RenderRoute(r)
	if strings.Contains(out, "redir") {
		t.Fatalf("unpublishing removes the redirect:\n%s", out)
	}
	r.Tunnel = []Hostname{"gitea.example.com"}
	r.SingleOrigin = false
	if out, _ = RenderRoute(r); strings.Contains(strings.SplitN(out, "# Tunnel entry", 2)[0], "redir") {
		t.Fatal("an app without the marker keeps both names")
	}
}
