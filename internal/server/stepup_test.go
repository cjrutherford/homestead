package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (a *ssoApp) auditCount(t *testing.T, action, outcome string) int {
	t.Helper()
	var n int
	if err := a.st.DB().QueryRow(`SELECT COUNT(*) FROM audit_entries WHERE action = ? AND outcome = ?`, action, outcome).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// oidcOwner returns a configured, linked app and an oidc session's cookie
// helper and row id.
func oidcOwner(t *testing.T) (*ssoApp, func(*http.Request), int64) {
	t.Helper()
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	token, csrf := a.ssoSession(t)
	v, err := a.app.Auth.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	return a, cookieAuth(token, csrf), v.ID
}

func stepUpCode(rec *httptest.ResponseRecorder) string { return rec.Header().Get(ErrorCodeHeader) }

func TestPublicNameStepUpMatrix(t *testing.T) {
	a, cookie, sid := oidcOwner(t)
	peopleRoutes := []string{}
	for p := range publicStepUpRoutes {
		peopleRoutes = append(peopleRoutes, p)
	}
	// ADR-054 adds the app-admin installation action to the five People routes and resume-admin.
	if len(peopleRoutes) != 7 {
		t.Fatalf("public step-up routes = %d, want 7", len(peopleRoutes))
	}
	for _, p := range peopleRoutes {
		if stepUpRoutes[p] != stepUpBoundary || (!strings.Contains(p, "/api/v1/people/") && p != "POST /api/v1/catalog/{name}/install-admin" && p != "POST /api/v1/catalog/{name}/setup/resume-admin") {
			t.Errorf("%s is not an approved step-up boundary route", p)
		}
	}
	do := func(pattern string) *httptest.ResponseRecorder {
		m, path := patternRequest(pattern)
		path = strings.ReplaceAll(path, "{app}", "immich")
		path = strings.ReplaceAll(path, "{name}", "immich")
		return a.publicDo(dashPublic, m, path, cookie)
	}

	// Not stepped up: stepup-required, never the password prompt.
	for _, p := range peopleRoutes {
		if rec := do(p); rec.Code != 403 || stepUpCode(rec) != ErrorCodeStepUpRequired {
			t.Errorf("%s not stepped up: %d %q", p, rec.Code, stepUpCode(rec))
		}
	}
	v := mustIdentity(t, a, sid)
	if _, err := a.app.Auth.MarkSteppedUp(sid, v); err != nil {
		t.Fatal(err)
	}
	// Stepped up: the approved routes pass the boundary.
	before := a.auditCount(t, "session.step-up.authorised", "ok")
	for _, p := range peopleRoutes {
		rec := do(p)
		if c := stepUpCode(rec); c == ErrorCodeStepUpRequired || c == ErrorCodeReauthRequired || c == ErrorCodePublicHost {
			t.Errorf("%s stepped up was refused: %d %q", p, rec.Code, c)
		}
	}
	if got := a.auditCount(t, "session.step-up.authorised", "ok"); got != before+7 {
		t.Errorf("authorised audit entries grew by %d, want 7", got-before)
	}
	// Every other step-up route is local-only on the public name (the link
	// routes are refused earlier as public-host); the name never takes the
	// password.
	for p := range stepUpRoutes {
		if publicStepUpRoutes[p] {
			continue
		}
		rec := do(p)
		if c := stepUpCode(rec); rec.Code != 403 || (c != ErrorCodeLocalOnly && c != ErrorCodePublicHost) {
			t.Errorf("%s stepped up on the public name: %d %q", p, rec.Code, c)
		}
		m, path := patternRequest(p)
		withPw := a.publicDo(dashPublic, m, path, func(r *http.Request) { cookie(r); r.Header.Set(ReauthHeader, ownerPassword) })
		if stepUpCode(withPw) != ErrorCodePublicHost {
			t.Errorf("%s with the password on the public name: %d %q", p, withPw.Code, stepUpCode(withPw))
		}
	}
	// A People route with the password header is still refused publicly.
	m, path := patternRequest("POST /api/v1/people/{id}/remove")
	if rec := a.publicDo(dashPublic, m, path, func(r *http.Request) { cookie(r); r.Header.Set(ReauthHeader, ownerPassword) }); stepUpCode(rec) != ErrorCodePublicHost {
		t.Errorf("password on a People route publicly: %q", stepUpCode(rec))
	}

	// Expired: back to stepup-required.
	if _, err := a.st.DB().Exec(`UPDATE sessions SET stepped_up_until = ?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for _, p := range peopleRoutes {
		if rec := do(p); rec.Code != 403 || stepUpCode(rec) != ErrorCodeStepUpRequired {
			t.Errorf("%s expired: %d %q", p, rec.Code, stepUpCode(rec))
		}
	}

	// Local and overlay names are unchanged: the password, step-up or not.
	if _, err := a.app.Auth.MarkSteppedUp(sid, v); err != nil {
		t.Fatal(err)
	}
	for _, p := range peopleRoutes {
		mm, pp := patternRequest(p)
		rec := a.req(mm, pp, "{}", "", cookie)
		if rec.Code != 403 || stepUpCode(rec) != ErrorCodeReauthRequired {
			t.Errorf("%s on the local name stepped up: %d %q", p, rec.Code, stepUpCode(rec))
		}
	}
	// A non-People step-up route still asks for the password locally and
	// says local-only (not the password prompt) on the public name.
	mm, pp := patternRequest("POST /api/v1/sso/plan")
	if rec := a.req(mm, pp, "{}", "", cookie); rec.Code != 403 || stepUpCode(rec) != ErrorCodeReauthRequired {
		t.Errorf("sso plan on the local name: %d %q", rec.Code, stepUpCode(rec))
	}
	if rec := a.publicDo(dashPublic, mm, pp, cookie); rec.Code != 403 || stepUpCode(rec) != ErrorCodeLocalOnly {
		t.Errorf("sso plan on the public name: %d %q", rec.Code, stepUpCode(rec))
	}
}

func mustIdentity(t *testing.T, a *ssoApp, sid int64) int64 {
	t.Helper()
	var id int64
	if err := a.st.DB().QueryRow(`SELECT identity_id FROM sessions WHERE id = ?`, sid).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStepUpCeremonyMarksTheSessionAndMintsNothing(t *testing.T) {
	a, cookie, sid := oidcOwner(t)
	sessions := func() int {
		var n int
		_ = a.st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
		return n
	}
	until := func() string {
		var s string
		_ = a.st.DB().QueryRow(`SELECT stepped_up_until FROM sessions WHERE id = ?`, sid).Scan(&s)
		return s
	}
	start := func(returnTo string) (*httptest.ResponseRecorder, string, string) {
		rec := a.req("POST", "/api/v1/sso/step-up", `{"return_to":`+jsonString(returnTo)+`}`, "", cookie)
		var out struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		state := ""
		for _, c := range rec.Result().Cookies() {
			if c.Name == "homestead_sso" {
				state = c.Value
			}
		}
		return rec, out.URL, state
	}
	for _, bad := range []string{"", "people", "//evil.test", "https://evil.test/"} {
		if rec, _, _ := start(bad); rec.Code != 400 {
			t.Errorf("return_to %q = %d, want 400", bad, rec.Code)
		}
	}
	n := sessions()
	rec, authURL, state := start("/people")
	if rec.Code != 200 || authURL == "" || state == "" || !strings.Contains(authURL, "max_age=30") || strings.Contains(authURL, "prompt=login") {
		t.Fatalf("step-up start = %d %s", rec.Code, rec.Body)
	}
	cb := a.followCallback(a.w.authorize(authURL), state)
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/people" {
		t.Fatalf("callback = %d %s", cb.Code, cb.Header().Get("Location"))
	}
	if c := cookiesOf(cb); c["homestead_session"] != nil || c["homestead_csrf"] != nil {
		t.Fatal("a step-up must not set session cookies")
	}
	if sessions() != n || until() == "" {
		t.Fatalf("sessions %d -> %d, stepped_up_until %q", n, sessions(), until())
	}
	if a.auditCount(t, "sso.step-up", "ok") != 1 {
		t.Fatal("step-up not audited")
	}

	// A stale Authentik sign-in marks nothing.
	a.st.DB().Exec(`UPDATE sessions SET stepped_up_until = ''`)
	a.w.authAge = 10 * time.Minute
	_, authURL, state = start("/people")
	cb = a.followCallback(a.w.authorize(authURL), state)
	if loc := cb.Header().Get("Location"); cb.Code != http.StatusFound || !strings.Contains(loc, "sso_error=stale") || until() != "" {
		t.Fatalf("stale callback = %d %s, until %q", cb.Code, loc, until())
	}
	a.w.authAge = 0

	// The ended session cannot be stepped up: the callback refuses.
	_, authURL, state = start("/people")
	a.st.DB().Exec(`DELETE FROM sessions WHERE id = ?`, sid)
	cb = a.followCallback(a.w.authorize(authURL), state)
	if loc := cb.Header().Get("Location"); !strings.Contains(loc, "sso_error=session") {
		t.Fatalf("ended-session callback = %s", loc)
	}
}

func TestStepUpIsOnlyForOIDCSessionsAndReachableOnPublicNames(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	if rec := a.req("POST", "/api/v1/sso/step-up", `{"return_to":"/people"}`, a.owner.token); rec.Code != http.StatusConflict {
		t.Fatalf("password session step-up = %d, want 409", rec.Code)
	}
	if rec := a.do("POST", "/api/v1/sso/step-up", `{}`, func(r *http.Request) { r.Host = ssoLocal }); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d, want 401", rec.Code)
	}
	if c, _ := Classify("POST /api/v1/sso/step-up"); c != ClassSession {
		t.Fatalf("class = %s", c)
	}
	rec := a.publicDo(dashPublic, "POST", "/api/v1/sso/step-up", nil)
	if stepUpCode(rec) == ErrorCodePublicHost {
		t.Fatal("step-up is refused on the public name")
	}
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }
