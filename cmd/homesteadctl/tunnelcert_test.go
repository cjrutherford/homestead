package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"homestead/internal/cert"
)

const testMovesJSON = `{"apps":[{"app":"immich","title":"Immich","from":"https://homestead.lan/apps/auth/application/o/homestead-immich/",
	"to":"https://homestead.example.com/apps/auth/application/o/homestead-immich/","host_entry":"homestead.example.com:host-gateway",
	"replaced_host_entry":"homestead.lan:host-gateway","digest":"m1"}],"token_kept":false,"note":"Homestead did not keep the Authentik API token."}`

// ADR-048 §5-§6: the tunnel plan prints the public certificate and the
// apps moving with it; setup passes the terms and production approvals.
func TestTunnelPlanCertificateAndMoves(t *testing.T) {
	var setup map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/tunnel/plan":
			_, _ = w.Write([]byte(`{"name":"homestead","dashboard_hostname":"homestead.example.com","zone":"example.com",
				"ingress":[],"dns":[],"omitted":[],"paths":[],"warnings":[],"digest":"c1g",
				"certificate":{"name":"homestead.example.com","issue":true,"certificate":"public-dashboard","adds_to_strategy":true,
				"directory":"https://acme-v02.api.letsencrypt.org/directory","directory_class":"production","challenge":"dns-01",
				"provider":"cloudflare","zone":"example.com","renewal":"auto","terms_url":"https://letsencrypt.org/repository/","note":"n","digest":"x"},
				"issuer_moves":` + testMovesJSON + `}`))
		case "/api/v1/tunnel/setup":
			_ = json.NewDecoder(r.Body).Decode(&setup)
			_, _ = w.Write([]byte(`{"id":31}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	code, stdout, stderr := invoke(t, []string{"--server", ts.URL, "tunnel", "plan", "--name", "homestead"})
	if code != exitOK {
		t.Fatalf("exit = %d stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"certificate: homestead.example.com as public-dashboard from https://acme-v02.api.letsencrypt.org/directory (production)",
		"challenge: dns-01 via cloudflare, zone example.com; renewal auto", "adds public-dashboard to TLS",
		"terms: https://letsencrypt.org/repository/ (approve with --agree-terms)",
		"sign-in moves: Homestead did not keep", "Immich: issuer https://homestead.lan/apps/auth/application/o/homestead-immich/ -> https://homestead.example.com/apps/auth/application/o/homestead-immich/",
		"host entry homestead.example.com:host-gateway (replaces homestead.lan:host-gateway)",
		"re-plan: homesteadctl apps sign-in plan immich",
		"tunnel setup --name homestead --agree-terms --confirm-production --digest c1g"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	code, _, stderr = invoke(t, []string{"--server", ts.URL, "tunnel", "setup", "--name", "homestead", "--digest", "c1g",
		"--agree-terms", "--confirm-production"})
	if code != exitOK || setup["agree_terms"] != true || setup["confirm_production"] != true {
		t.Fatalf("exit = %d body = %v stderr = %q", code, setup, stderr)
	}
}

// ADR-048 §6 from TLS: the issuance plan prints the move list.
func TestIssuancePlanPrintsMoves(t *testing.T) {
	var moves cert.MovePlan
	if err := json.Unmarshal([]byte(testMovesJSON), &moves); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	printIssuancePlan(&b, cert.IssuancePlan{Certificate: "dash", IssuerMoves: &moves})
	if !strings.Contains(b.String(), "  sign-in moves:") || !strings.Contains(b.String(), "    Immich: issuer") {
		t.Fatalf("printout:\n%s", b.String())
	}
}
