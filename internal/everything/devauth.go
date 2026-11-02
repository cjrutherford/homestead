//go:build everythingdev

package everything

// DEVELOPMENT ONLY. This file is compiled only with `-tags everythingdev`.
// It is not authentication: anyone who can reach the dev routes can mint a
// session for any subject. Production binaries are built without the tag and
// therefore cannot contain it, whatever their configuration says.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

const DevCookie = "hs_dev_session"
const DevCSRFHeader = "X-Homestead-CSRF"

type devSession struct {
	subject, csrf string
	expires       time.Time
}

// DevIdentity is an in-memory session table for tests.
type DevIdentity struct {
	app      string
	mu       sync.Mutex
	sessions map[string]devSession
	now      func() time.Time
}

func NewDevIdentity(app string) *DevIdentity {
	return &DevIdentity{app: app, sessions: map[string]devSession{}, now: time.Now}
}

func randHex() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Login mints a session for subject valid for ttl.
func (d *DevIdentity) Login(subject string, ttl time.Duration) (cookie, csrf string) {
	cookie, csrf = randHex(), randHex()
	d.mu.Lock()
	d.sessions[cookie] = devSession{subject, csrf, d.now().Add(ttl)}
	d.mu.Unlock()
	return
}

func (d *DevIdentity) Revoke(cookie string) {
	d.mu.Lock()
	delete(d.sessions, cookie)
	d.mu.Unlock()
}

func (d *DevIdentity) lookup(r *http.Request) (devSession, bool) {
	c, err := r.Cookie(DevCookie)
	if err != nil {
		return devSession{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.sessions[c.Value]
	if !ok || !d.now().Before(s.expires) {
		delete(d.sessions, c.Value)
		return devSession{}, false
	}
	return s, true
}

func (d *DevIdentity) Authenticate(r *http.Request) (Scope, error) {
	s, ok := d.lookup(r)
	if !ok {
		return Scope{}, ErrInvalid
	}
	return Scope{App: d.app, Subject: s.subject}, nil
}

func (d *DevIdentity) CheckCSRF(r *http.Request) bool {
	s, ok := d.lookup(r)
	got := r.Header.Get(DevCSRFHeader)
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.csrf)) == 1
}

// Routes adds /_dev/login and /_dev/revoke for the test harness.
func (d *DevIdentity) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/_dev/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Subject string `json:"subject"`
			TTLMs   int64  `json:"ttl_ms"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !validText(in.Subject, 512) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		ttl := time.Duration(in.TTLMs) * time.Millisecond
		if ttl <= 0 {
			ttl = time.Hour
		}
		c, x := d.Login(in.Subject, ttl)
		_ = json.NewEncoder(w).Encode(map[string]string{"cookie": c, "csrf": x})
	})
	mux.HandleFunc("/_dev/revoke", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Cookie string `json:"cookie"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		d.Revoke(in.Cookie)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}
