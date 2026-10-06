package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/claim"
	"homestead/internal/store"
)

func claimedServer(t *testing.T) (*Server, *claim.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := claim.New(st)
	srv := New("127.0.0.1:0")
	srv.MountClaim(svc)
	return srv, svc
}

func TestIdentityUnclaimed(t *testing.T) {
	srv, _ := claimedServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/identity", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	var body struct {
		State   string `json:"state"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.State != "UNCLAIMED" || body.Version == "" {
		t.Fatalf("identity = %+v, want UNCLAIMED with version", body)
	}
}

func postClaim(t *testing.T, srv *Server, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/claim", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestClaimRoundTrip(t *testing.T) {
	srv, svc := claimedServer(t)
	token, err := svc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	rec := postClaim(t, srv, `{"token":`+quote(token)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "CLAIMED") {
		t.Fatalf("body = %s, want CLAIMED", rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/identity", nil)
	idRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(idRec, req)
	if !strings.Contains(idRec.Body.String(), "CLAIMED") {
		t.Fatalf("identity = %s, want CLAIMED", idRec.Body.String())
	}
}

func TestClaimFailures(t *testing.T) {
	srv, svc := claimedServer(t)
	token, err := svc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"malformed", "{oops", http.StatusBadRequest},
		{"empty token", `{"token":""}`, http.StatusBadRequest},
		{"unknown token", `{"token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := postClaim(t, srv, tc.body); rec.Code != tc.status {
				t.Fatalf("POST status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
	if rec := postClaim(t, srv, `{"token":`+quote(token)+`}`); rec.Code != http.StatusOK {
		t.Fatalf("claim status = %d, want 200", rec.Code)
	}
	if rec := postClaim(t, srv, `{"token":`+quote(token)+`}`); rec.Code != http.StatusConflict {
		t.Fatalf("second claim status = %d, want 409", rec.Code)
	}
	if rec := postClaim(t, srv, `{"token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`); rec.Code != http.StatusConflict {
		t.Fatalf("claim when claimed status = %d, want 409", rec.Code)
	}
}

func quote(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}
