//go:build everythingdev

package everything

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDevIdentitySessions(t *testing.T) {
	d := NewDevIdentity("tasks")
	c, x := d.Login("alice", time.Hour)
	r := httptest.NewRequest("POST", "/", nil)
	r.AddCookie(&http.Cookie{Name: DevCookie, Value: c})
	r.Header.Set(DevCSRFHeader, x)
	if s, err := d.Authenticate(r); err != nil || s.Subject != "alice" || s.App != "tasks" || !d.CheckCSRF(r) {
		t.Fatal("valid session rejected")
	}
	r.Header.Set(DevCSRFHeader, "wrong")
	if d.CheckCSRF(r) {
		t.Fatal("bad csrf accepted")
	}
	d.Revoke(c)
	if _, err := d.Authenticate(r); err == nil {
		t.Fatal("revoked session accepted")
	}
	c2, _ := d.Login("bob", time.Hour)
	d.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	r2 := httptest.NewRequest("POST", "/", nil)
	r2.AddCookie(&http.Cookie{Name: DevCookie, Value: c2})
	if _, err := d.Authenticate(r2); err == nil {
		t.Fatal("expired session accepted")
	}
}
