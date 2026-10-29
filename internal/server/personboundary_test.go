package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"homestead/internal/auth"
)

// S338 (ADR-050 §2): a person session is deny-by-default.

func (a *enforcedApp) addPerson(t *testing.T, id int64, state string) {
	t.Helper()
	if _, err := a.st.DB().Exec(`INSERT INTO people (id, display_name, username, email, authentik_pk, authentik_uid, state, origin, created_by, created_at)
		VALUES (?, 'Sam Person', 'sam', '', 9, 'uid-9', ?, 'created', 'o', '2026-01-01T00:00:00Z')`, id, state); err != nil {
		t.Fatal(err)
	}
}

func TestPersonSessionReachesOnlyItsAllowlist(t *testing.T) {
	a := newEnforcedApp(t)
	a.setup(t)
	a.addPerson(t, 7, "active")
	sess, err := a.app.Auth.MintPersonSession(7)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"GET /api/v1/me": true, "GET /api/v1/me/apps": true, "DELETE /api/v1/sessions": true}
	hdr := func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+sess.Token)
	}
	for _, pattern := range a.srv.Routes() {
		if c, _ := Classify(pattern); c != ClassSession || allowed[pattern] {
			continue
		}
		method, path := routeRequest(pattern)
		rec := a.do(method, path, "{}", hdr)
		if rec.Code != http.StatusForbidden || rec.Header().Get("X-Homestead-Error") != ErrorCodeOwnerOnly {
			t.Errorf("%s: person got %d (%s), want 403 owner-only", pattern, rec.Code, rec.Header().Get("X-Homestead-Error"))
		}
	}
	// The allowlist answers, as the person.
	rec := a.do("GET", "/api/v1/me", "", hdr)
	var me map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if rec.Code != 200 || me["role"] != "person" || me["display_name"] != "Sam Person" {
		t.Fatalf("me = %d %s", rec.Code, rec.Body)
	}
	if rec := a.do("GET", "/api/v1/me/apps", "", hdr); rec.Code == 401 || rec.Code == 403 {
		t.Fatalf("me/apps = %d %s", rec.Code, rec.Body)
	}
	if rec := a.do("DELETE", "/api/v1/sessions", "", hdr); rec.Code >= 300 {
		t.Fatalf("sign-out = %d %s", rec.Code, rec.Body)
	}
	if rec := a.do("GET", "/api/v1/me", "", hdr); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session survived sign-out: %d", rec.Code)
	}
}

func TestPersonSessionEndsWhenDisabledOrRemoved(t *testing.T) {
	a := newEnforcedApp(t)
	a.setup(t)
	a.addPerson(t, 7, "active")
	sess, _ := a.app.Auth.MintPersonSession(7)
	hdr := bearer(sess.Token)
	if rec := a.do("GET", "/api/v1/me", "", hdr); rec.Code != 200 {
		t.Fatalf("me = %d", rec.Code)
	}
	if _, err := a.st.DB().Exec(`UPDATE people SET state = 'disabled' WHERE id = 7`); err != nil {
		t.Fatal(err)
	}
	if rec := a.do("GET", "/api/v1/me", "", hdr); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled person kept a session: %d", rec.Code)
	}
	if _, err := a.app.Auth.MintPersonSession(7); err == nil {
		t.Fatal("minted a session for a disabled person")
	}
}

func TestPersonSessionIsNeverTheOwner(t *testing.T) {
	a := newEnforcedApp(t)
	a.setup(t)
	a.addPerson(t, 7, "active")
	sess, _ := a.app.Auth.MintPersonSession(7)
	v, err := a.app.Auth.Verify(sess.Token)
	if err != nil || v.Role != auth.RolePerson || v.PersonID != 7 || v.Username == "owner" {
		t.Fatalf("verified = %+v, %v", v, err)
	}
	// Even a matching username would not make it the owner: the sessions
	// table carries no identity for it.
	var identity *int64
	_ = a.st.DB().QueryRow(`SELECT identity_id FROM sessions WHERE person_id = 7`).Scan(&identity)
	if identity != nil {
		t.Fatalf("person session has identity %d", *identity)
	}
}
