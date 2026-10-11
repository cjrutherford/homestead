package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func cookieByName(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func loginRec(t *testing.T, srv *Server, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions",
		strings.NewReader(`{"username":"owner","password":"long-enough-password"}`))
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

func setupOwner(t *testing.T) *Server {
	t.Helper()
	srv := claimedAuthServer(t)
	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/admins/setup", `{"username":"owner","password":"long-enough-password"}`, ""); rec.Code != http.StatusCreated {
		t.Fatalf("setup = %d", rec.Code)
	}
	return srv
}

func TestLoginSetsHardenedSessionCookie(t *testing.T) {
	srv := setupOwner(t)
	rec := loginRec(t, srv, nil)
	sess := cookieByName(rec, SessionCookieName)
	if sess == nil {
		t.Fatal("no session cookie")
	}
	if !sess.HttpOnly || sess.SameSite != http.SameSiteStrictMode || sess.Path != "/" || sess.Secure {
		t.Fatalf("plain-HTTP session cookie attributes wrong: %+v", sess)
	}
	if sess.Expires.IsZero() {
		t.Fatal("session cookie has no expiry")
	}
	csrf := cookieByName(rec, CSRFCookieName)
	if csrf == nil || csrf.HttpOnly || csrf.SameSite != http.SameSiteStrictMode {
		t.Fatalf("csrf cookie must be script-readable and SameSite=Strict: %+v", csrf)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"token":"`+sess.Value+`"`) {
		t.Fatalf("body must still carry the bearer token matching the cookie: %s", body)
	}
	if !strings.Contains(body, `"csrf_token":"`+csrf.Value+`"`) {
		t.Fatalf("body lacks csrf token: %s", body)
	}
	if sess.Value == csrf.Value {
		t.Fatal("csrf token equals session token")
	}
}

func TestSessionCookieSecureWhenTLS(t *testing.T) {
	srv := setupOwner(t)
	rec := loginRec(t, srv, func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") })
	if c := cookieByName(rec, SessionCookieName); c == nil || !c.Secure {
		t.Fatalf("forwarded-https cookie not Secure: %+v", c)
	}
	rec = loginRec(t, srv, func(r *http.Request) { r.TLS = &tlsState })
	for _, n := range []string{SessionCookieName, CSRFCookieName} {
		if c := cookieByName(rec, n); c == nil || !c.Secure {
			t.Fatalf("TLS cookie %s not Secure: %+v", n, c)
		}
	}
}

func TestCookieAndBearerResolveToOneSession(t *testing.T) {
	srv := setupOwner(t)
	rec := loginRec(t, srv, nil)
	cookie := cookieByName(rec, SessionCookieName)
	// Cookie authenticates a session-only endpoint.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admins/lockout", nil)
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	srv.Handler().ServeHTTP(out, req)
	if out.Code != http.StatusOK {
		t.Fatalf("cookie lockout = %d", out.Code)
	}
	// Bearer logout revokes the same session the cookie names.
	if r := doJSON(t, srv, http.MethodDelete, "/api/v1/sessions", "", cookie.Value); r.Code != http.StatusNoContent {
		t.Fatalf("bearer logout = %d", r.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admins/lockout", nil)
	req.AddCookie(cookie)
	out = httptest.NewRecorder()
	srv.Handler().ServeHTTP(out, req)
	if out.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie lockout = %d, want 401", out.Code)
	}
}

func TestCookieLogoutClearsCookies(t *testing.T) {
	srv := setupOwner(t)
	login := loginRec(t, srv, nil)
	cookie := cookieByName(login, SessionCookieName)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions", nil)
	req.AddCookie(cookie)
	req.Header.Set(CSRFHeader, cookieByName(login, CSRFCookieName).Value)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cookie logout = %d", rec.Code)
	}
	for _, n := range []string{SessionCookieName, CSRFCookieName} {
		c := cookieByName(rec, n)
		if c == nil || c.MaxAge >= 0 || c.Value != "" {
			t.Fatalf("cookie %s not cleared: %+v", n, c)
		}
	}
	// Server-side revoked too.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admins/lockout", nil)
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	srv.Handler().ServeHTTP(out, req)
	if out.Code != http.StatusUnauthorized {
		t.Fatalf("after logout = %d, want 401", out.Code)
	}
}

func TestBearerTakesPrecedenceOverCookie(t *testing.T) {
	srv := setupOwner(t)
	good := loginToken(t, srv, "long-enough-password")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admins/lockout", nil)
	req.Header.Set("Authorization", "Bearer "+good)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "garbage"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer+bad cookie = %d, want 200", rec.Code)
	}
}

var tlsState = tls.ConnectionState{HandshakeComplete: true}
