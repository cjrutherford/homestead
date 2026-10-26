package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/router"
	"homestead/internal/store"
)

// The reachability check (S313): after an install, the wizard's last step
// and the CLI ask the appliance whether each of the app's addresses
// answers. Local names are asked of the edge directly; public names go
// out the front door, DNS and Cloudflare's TLS handshake included. The
// check is advisory: it never fails an install and changes nothing.

// reachAddress is one address and whether it answers, with the reason
// and the fix in the owner's words. Reason may be set on an answering
// address: a .lan name answers at the edge but devices cannot find it
// while the resolver is off.
type reachAddress struct {
	Address string `json:"address"`
	Host    string `json:"host"`
	Kind    string `json:"kind"`
	Answers bool   `json:"answers"`
	Reason  string `json:"reason,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

type reachStatus struct {
	App       string         `json:"app"`
	Resolver  bool           `json:"resolver"`
	Addresses []reachAddress `json:"addresses"`
}

// reachProbe asks one address and answers with the HTTP status. Tests
// replace these.
type reachProbe func(ctx context.Context, host string, https bool, roots *x509.CertPool) (int, error)

var (
	reachLocal  reachProbe = probeEdge
	reachPublic reachProbe = probePublic
)

const reachTimeout = 8 * time.Second

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// probeEdge asks the edge on this appliance for host, by name, trusting
// only the Homestead internal CA.
func probeEdge(ctx context.Context, host string, https bool, roots *x509.CertPool) (int, error) {
	port, scheme := "80", "http"
	if https {
		port, scheme = "443", "https"
	}
	dialer := &net.Dialer{Timeout: reachTimeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		},
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12},
	}
	defer transport.CloseIdleConnections()
	return get(ctx, &http.Client{Transport: transport, Timeout: reachTimeout, CheckRedirect: noRedirect}, scheme+"://"+host+"/")
}

// probePublic asks the public name the way a browser would: public DNS,
// Cloudflare's certificate, the tunnel, the edge, the app.
func probePublic(ctx context.Context, host string, _ bool, _ *x509.CertPool) (int, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	return get(ctx, &http.Client{Transport: transport, Timeout: reachTimeout, CheckRedirect: noRedirect}, "https://"+host+"/")
}

func get(ctx context.Context, client *http.Client, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

func isDNSError(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

func isTLSError(err error) bool {
	var (
		verify   *tls.CertificateVerificationError
		alert    tls.AlertError
		record   tls.RecordHeaderError
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
	)
	return errors.As(err, &verify) || errors.As(err, &alert) || errors.As(err, &record) ||
		errors.As(err, &unknown) || errors.As(err, &hostname) ||
		strings.Contains(err.Error(), "tls:") || strings.Contains(err.Error(), "x509:")
}

// localResult explains one local name. The appliance can only see the
// edge; whether a device finds the name is the resolver's and the
// router's business, so a .lan name says what it needs when the resolver
// is off.
func localResult(title, host string, https, resolver bool, status int, err error) reachAddress {
	scheme := "http"
	if https {
		scheme = "https"
	}
	out := reachAddress{Address: scheme + "://" + host, Host: host, Kind: "local"}
	switch {
	case err != nil && errors.Is(err, syscall.ECONNREFUSED):
		out.Reason = "The edge is not answering on this appliance."
		out.Fix = "Set up the edge on the Edge page; it serves every app by name."
	case err != nil && isTLSError(err):
		out.Reason = "The edge has no certificate from your Homestead internal CA for " + host + " yet."
		out.Fix = "Check the route on the Edge page, then check again."
	case err != nil:
		out.Reason = "The edge did not answer for " + host + ": " + err.Error()
		out.Fix = "Check the Edge page, then check again."
	case status >= 500:
		out.Reason = fmt.Sprintf("The edge answers, but %s does not yet (%d): it may still be starting.", title, status)
		out.Fix = "Wait a minute and check again. If it stays, open its work order or its containers on the Docker page."
	default:
		out.Answers = true
		if strings.HasSuffix(host, ".lan") && !resolver {
			out.Reason = "Your devices cannot resolve " + host + " while the appliance resolver is off."
			out.Fix = "Turn on the resolver on the Names page, or add a router DNS entry for " + host + "."
		}
	}
	return out
}

// publicResult explains one public name, from outside in: DNS, then
// Cloudflare's certificate (the S312 message), then the tunnel.
func publicResult(title, host, zone string, status int, err error) reachAddress {
	out := reachAddress{Address: "https://" + host, Host: host, Kind: "public"}
	switch {
	case err != nil && isDNSError(err):
		out.Reason = host + " does not resolve yet."
		out.Fix = "A new DNS record can take a few minutes to appear. Check again shortly; if it stays, check the name on the Tunnel page."
	case err != nil && isTLSError(err):
		out.Reason = router.UncoveredText(host, zone)
		out.Fix = "Add a certificate for it in Cloudflare (Advanced Certificate Manager), or publish a name one level below " + zone + "."
	case err != nil:
		out.Reason = "The appliance could not reach " + host + ": " + err.Error()
		out.Fix = "The appliance may not reach the internet. Try the address from another device."
	case status == 530 || status == 502 || status == 503 || status == 504:
		out.Reason = fmt.Sprintf("Cloudflare answers, but the tunnel does not reach %s (%d).", title, status)
		out.Fix = "Check the tunnel connector on the Tunnel page, then check again."
	default:
		out.Answers = true
	}
	return out
}

// zoneGuess is the tunnel's stored zone, else the name's last two labels.
func zoneGuess(st *store.Store, host string) string {
	if id, _, err := router.RecordedTunnel(st); err == nil && id != "" {
		if zone, err := router.TunnelZoneName(st, id); err == nil && zone != "" {
			return zone
		}
	}
	labels := strings.Split(host, ".")
	if len(labels) > 2 {
		labels = labels[len(labels)-2:]
	}
	return strings.Join(labels, ".")
}

// MountAppReach registers the reachability check. Session class.
func (s *Server) MountAppReach(st *store.Store, names *LocalNames, local *cert.LocalTLS, catalogDir string) {
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/reach", func(w http.ResponseWriter, r *http.Request) {
		entries, err := catalog.Load(catalogDir)
		if err != nil {
			http.Error(w, "catalog unavailable", http.StatusInternalServerError)
			return
		}
		manifest, err := entries.Get(r.PathValue("name"))
		if err != nil {
			http.Error(w, "unknown app", http.StatusNotFound)
			return
		}
		set, err := loadEdgeRoutes(st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := reachStatus{App: manifest.Name, Addresses: []reachAddress{}}
		if names != nil {
			out.Resolver, _ = names.Enabled()
		}
		var roots *x509.CertPool
		if local != nil {
			if pem, err := local.CA.RootPEM(); err == nil {
				roots = x509.NewCertPool()
				roots.AppendCertsFromPEM(pem)
			}
		}
		ctx := r.Context()
		for _, route := range appsso.AppRoutes(manifest, set.Routes) {
			https := route.TLS != docker.TLSOff
			for _, n := range route.Names() {
				status, err := reachLocal(ctx, string(n), https, roots)
				out.Addresses = append(out.Addresses, localResult(manifest.Title, string(n), https, out.Resolver, status, err))
			}
			for _, n := range route.Tunnel {
				status, err := reachPublic(ctx, string(n), true, nil)
				out.Addresses = append(out.Addresses, publicResult(manifest.Title, string(n), zoneGuess(st, string(n)), status, err))
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, out)
	})
}
