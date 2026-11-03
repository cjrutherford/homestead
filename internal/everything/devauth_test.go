//go:build everythingdev

package everything

import (
	"context"
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

// S437: with a store attached, a development login registers the person under
// the subject as the name, as a verified OIDC sign-in would, so development
// people can share with each other by name; without a store nothing is recorded.
func TestDevIdentityRecordsPeople(t *testing.T) {
	s, _ := newTestStore(t)
	d := NewDevIdentity("tasks")
	d.Login("early", time.Hour) // no store yet
	d.RecordPeople(s)
	d.Login("Bob", time.Hour)
	ctx := context.Background()
	if sub, err := resolveName(ctx, s.db, "tasks", "bob"); err != nil || sub != "Bob" {
		t.Fatalf("bob not registered: %q %v", sub, err)
	}
	if _, err := resolveName(ctx, s.db, "tasks", "early"); err == nil {
		t.Fatal("login before the store was attached was recorded")
	}
	if _, err := resolveName(ctx, s.db, "notes", "bob"); err == nil {
		t.Fatal("registered in another app")
	}
}
