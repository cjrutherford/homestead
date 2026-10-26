package server

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"homestead/internal/docker"
)

// S313: each address answers, or says why not and what to do.
func TestReachResults(t *testing.T) {
	refused := &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	for _, c := range []struct {
		got  reachAddress
		ok   bool
		want string
	}{
		{localResult("Nextcloud", "nextcloud.lan", true, true, 200, nil), true, ""},
		{localResult("Nextcloud", "nextcloud.lan", true, false, 302, nil), true, "turn on the resolver"},
		{localResult("Nextcloud", "nextcloud.local", true, false, 200, nil), true, ""},
		{localResult("Nextcloud", "nextcloud.lan", true, true, 502, nil), false, "may still be starting"},
		{localResult("Nextcloud", "nextcloud.lan", true, true, 0, refused), false, "Edge page"},
		{localResult("Nextcloud", "nextcloud.lan", true, true, 0, x509.UnknownAuthorityError{}), false, "internal CA"},
		{publicResult("Nextcloud", "nextcloud.homestead.example.com", "example.com", 302, nil), true, ""},
		{publicResult("Nextcloud", "nextcloud.homestead.example.com", "example.com", 0, &net.DNSError{Err: "no such host", Name: "x"}), false, "does not resolve yet"},
		{publicResult("Nextcloud", "nextcloud.homestead.example.com", "example.com", 0, fmt.Errorf("remote error: tls: handshake failure")), false, "no certificate for nextcloud.homestead.example.com"},
		{publicResult("Nextcloud", "nextcloud.example.com", "example.com", 530, nil), false, "tunnel"},
		{publicResult("Nextcloud", "nextcloud.example.com", "example.com", 0, errors.New("network is unreachable")), false, "another device"},
	} {
		text := c.got.Reason + " " + c.got.Fix
		if c.got.Answers != c.ok || (c.want == "" && c.got.Reason != "") || !strings.Contains(strings.ToLower(text), strings.ToLower(c.want)) {
			t.Errorf("%+v: want answers=%v %q", c.got, c.ok, c.want)
		}
	}
}

func TestReachRoute(t *testing.T) {
	srv, st, _, _ := appPublicRig(t)
	srv.MountAppReach(st, NewLocalNames(st), nil, "../../catalog")
	set, _ := loadEdgeRoutes(st)
	for i := range set.Routes {
		if set.Routes[i].Host == "immich.lan" {
			set.Routes[i].Tunnel = []docker.Hostname{"immich.homestead.example.com"}
		}
	}
	if err := saveEdgeRoutes(st, set); err != nil {
		t.Fatal(err)
	}
	var asked []string
	oldLocal, oldPublic := reachLocal, reachPublic
	reachLocal = func(_ context.Context, host string, https bool, _ *x509.CertPool) (int, error) {
		asked = append(asked, fmt.Sprintf("local %s %v", host, https))
		return 200, nil
	}
	reachPublic = func(_ context.Context, host string, _ bool, _ *x509.CertPool) (int, error) {
		asked = append(asked, "public "+host)
		return 0, errors.New("remote error: tls: handshake failure")
	}
	t.Cleanup(func() { reachLocal, reachPublic = oldLocal, oldPublic })
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/immich/reach", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("reach: %d %s", recorder.Code, recorder.Body.String())
	}
	var out reachStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(asked, "|") != "local immich.lan true|local immich.local true|public immich.homestead.example.com" {
		t.Fatalf("asked = %v", asked)
	}
	if out.Resolver || len(out.Addresses) != 3 || !out.Addresses[0].Answers || !strings.Contains(out.Addresses[0].Fix, "resolver") ||
		out.Addresses[2].Answers || out.Addresses[2].Kind != "public" || !strings.Contains(out.Addresses[2].Reason, "Advanced Certificate Manager") {
		t.Fatalf("reach = %+v", out)
	}
	if recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/ghost/reach", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("ghost: %d", recorder.Code)
	}
}
