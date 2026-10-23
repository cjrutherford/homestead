package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAuthentik is a minimal Authentik API: flows, applications and
// OAuth2 providers, recording every request so tests can assert the
// payload shape rather than only the outcome.
type fakeAuthentik struct {
	t        *testing.T
	app      bool // an application with the slug already exists
	flows    bool
	failApp  bool
	requests []string
	payloads []map[string]any
	provider int
}

func newFakeAuthentik(t *testing.T) (*fakeAuthentik, *httptest.Server) {
	t.Helper()
	f := &fakeAuthentik{t: t, flows: true, provider: 7}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/root/config/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		writeJSON(w, map[string]any{"version_current": "2024.10.1"})
	})
	mux.HandleFunc("/api/v3/flows/instances/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if !f.flows {
			writeJSON(w, map[string]any{"results": []any{}})
			return
		}
		slug := r.URL.Query().Get("slug")
		writeJSON(w, map[string]any{"results": []map[string]any{{"pk": "flow-" + slug}}})
	})
	mux.HandleFunc("/api/v3/core/applications/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if r.Method == http.MethodPost {
			if f.failApp {
				http.Error(w, "boom", http.StatusBadRequest)
				return
			}
			f.app = true
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]any{"slug": "paperless"})
			return
		}
		if r.Method == http.MethodDelete {
			f.app = false
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !f.app {
			writeJSON(w, map[string]any{"results": []any{}})
			return
		}
		writeJSON(w, map[string]any{"results": []map[string]any{{"pk": 3, "provider": f.provider}}})
	})
	mux.HandleFunc("/api/v3/providers/oauth2/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]any{"pk": f.provider, "client_id": "client-abc", "client_secret": "secret-xyz"})
		case http.MethodPatch, http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			writeJSON(w, map[string]any{"pk": f.provider, "client_id": "client-abc"})
		}
	})
	return f, httptest.NewServer(mux)
}

func (f *fakeAuthentik) record(r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPatch) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			f.payloads = append(f.payloads, payload)
		}
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func spec() ClientSpec {
	return ClientSpec{
		Package:      "paperless-ngx",
		Slug:         "paperless",
		Name:         "Paperless",
		RedirectURIs: []string{"https://archive.home.example/accounts/oidc/paperless/login/callback/"},
	}
}

func TestAuthentikRegistersAsProvider(t *testing.T) {
	if err := ValidateProvider(NewAuthentik()); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	if err := reg.Register(NewAuthentik()); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Get("authentik"); err != nil {
		t.Fatal(err)
	}
}

func TestAuthentikCheckConnection(t *testing.T) {
	_, srv := newFakeAuthentik(t)
	defer srv.Close()
	p := NewAuthentik()
	if err := p.CheckConnection(context.Background(), srv.URL, Credentials{"token": "t"}); err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}
	if err := p.CheckConnection(context.Background(), srv.URL, Credentials{}); err == nil {
		t.Fatal("missing token must fail")
	}
}

func TestAuthentikEnsureClientCreatesProviderAndApplication(t *testing.T) {
	fake, srv := newFakeAuthentik(t)
	defer srv.Close()

	client, err := NewAuthentik().EnsureClient(context.Background(), srv.URL, Credentials{"token": "t"}, spec())
	if err != nil {
		t.Fatalf("EnsureClient: %v", err)
	}
	if !client.Created || client.ClientID != "client-abc" || client.ClientSecret != "secret-xyz" || client.ProviderPK != 7 {
		t.Fatalf("client = %+v", client)
	}

	var providerPayload, appPayload map[string]any
	for _, p := range fake.payloads {
		if _, ok := p["client_type"]; ok {
			providerPayload = p
		}
		if _, ok := p["provider"]; ok {
			appPayload = p
		}
	}
	if providerPayload == nil || appPayload == nil {
		t.Fatalf("payloads = %+v", fake.payloads)
	}
	if providerPayload["client_type"] != "confidential" {
		t.Fatalf("client_type = %v", providerPayload["client_type"])
	}
	if providerPayload["authorization_flow"] != "flow-default-provider-authorization-explicit-consent" {
		t.Fatalf("authorization_flow = %v", providerPayload["authorization_flow"])
	}
	redirects, ok := providerPayload["redirect_uris"].([]any)
	if !ok || len(redirects) != 1 {
		t.Fatalf("redirect_uris = %v", providerPayload["redirect_uris"])
	}
	first, _ := redirects[0].(map[string]any)
	if first["url"] != spec().RedirectURIs[0] || first["matching_mode"] != "strict" {
		t.Fatalf("redirect entry = %v", redirects[0])
	}
	if appPayload["slug"] != "paperless" {
		t.Fatalf("app payload = %v", appPayload)
	}
}

func TestAuthentikEnsureClientUpdatesExistingRedirects(t *testing.T) {
	fake, srv := newFakeAuthentik(t)
	fake.app = true
	defer srv.Close()

	client, err := NewAuthentik().EnsureClient(context.Background(), srv.URL, Credentials{"token": "t"}, spec())
	if err != nil {
		t.Fatalf("EnsureClient: %v", err)
	}
	if client.Created || client.ClientID != "client-abc" || client.ClientSecret != "" {
		t.Fatalf("client = %+v, want existing client without a fresh secret", client)
	}
	patched := false
	for _, r := range fake.requests {
		if strings.HasPrefix(r, "PATCH /api/v3/providers/oauth2/") {
			patched = true
		}
		if strings.HasPrefix(r, "POST /api/v3/providers/oauth2/") || strings.HasPrefix(r, "POST /api/v3/core/applications/") {
			t.Fatalf("existing client must not be recreated: %v", fake.requests)
		}
	}
	if !patched {
		t.Fatalf("redirects not updated: %v", fake.requests)
	}
}

func TestAuthentikEnsureClientCleansUpOrphanProvider(t *testing.T) {
	fake, srv := newFakeAuthentik(t)
	fake.failApp = true
	defer srv.Close()

	if _, err := NewAuthentik().EnsureClient(context.Background(), srv.URL, Credentials{"token": "t"}, spec()); err == nil {
		t.Fatal("want an error when the application cannot be created")
	}
	deleted := false
	for _, r := range fake.requests {
		if strings.HasPrefix(r, "DELETE /api/v3/providers/oauth2/") {
			deleted = true
		}
	}
	if !deleted {
		t.Fatalf("orphan provider left behind: %v", fake.requests)
	}
}

func TestAuthentikEnsureClientRejectsBadSpec(t *testing.T) {
	_, srv := newFakeAuthentik(t)
	defer srv.Close()
	bad := spec()
	bad.RedirectURIs = []string{"/relative"}
	if _, err := NewAuthentik().EnsureClient(context.Background(), srv.URL, Credentials{"token": "t"}, bad); err == nil {
		t.Fatal("relative redirect must fail before any call")
	}
}

func TestAuthentikEnsureClientReportsMissingFlow(t *testing.T) {
	fake, srv := newFakeAuthentik(t)
	fake.flows = false
	defer srv.Close()
	_, err := NewAuthentik().EnsureClient(context.Background(), srv.URL, Credentials{"token": "t"}, spec())
	if err == nil || !strings.Contains(err.Error(), "flow") {
		t.Fatalf("err = %v, want a missing-flow error", err)
	}
}

func TestAuthentikDeleteClientRemovesProvider(t *testing.T) {
	fake, srv := newFakeAuthentik(t)
	fake.app = true
	defer srv.Close()

	if err := NewAuthentik().DeleteClient(context.Background(), srv.URL, Credentials{"token": "t"}, "paperless"); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	want := []string{
		"GET /api/v3/core/applications/",
		"DELETE /api/v3/core/applications/paperless/",
		"DELETE /api/v3/providers/oauth2/7/",
	}
	if strings.Join(fake.requests, ",") != strings.Join(want, ",") {
		t.Fatalf("requests = %v, want %v", fake.requests, want)
	}
}

func TestAuthentikDeleteClientIsRepeatable(t *testing.T) {
	_, srv := newFakeAuthentik(t)
	defer srv.Close()
	if err := NewAuthentik().DeleteClient(context.Background(), srv.URL, Credentials{"token": "t"}, "absent"); err != nil {
		t.Fatalf("deleting an absent client must succeed: %v", err)
	}
}

func TestAuthentikSurfacesAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer srv.Close()
	err := NewAuthentik().CheckConnection(context.Background(), srv.URL, Credentials{"token": "t"})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want a status-only error", err)
	}
}
