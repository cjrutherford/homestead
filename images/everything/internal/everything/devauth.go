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
	created       time.Time
}

// DevIdentity is an in-memory session table for tests.
type DevIdentity struct {
	app      string
	mu       sync.Mutex
	sessions map[string]devSession
	now      func() time.Time

	base, static string // optional: serve the prepared app for a real browser
}

// Static makes Routes also serve the prepared application under base, with
// the same index/CSRF behaviour as production, and a GET sign-in page.
func (d *DevIdentity) Static(base, dir string) { d.base, d.static = base, dir }

// Session implements SessionSource.
func (d *DevIdentity) Session(r *http.Request) (string, string, bool) {
	s, ok := d.lookup(r)
	return s.subject, s.csrf, ok
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
	d.sessions[cookie] = devSession{subject, csrf, d.now().Add(ttl), d.now()}
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

// SessionAge reports the session's age for the sharing recheck.
func (d *DevIdentity) SessionAge(r *http.Request) (time.Duration, bool) {
	s, ok := d.lookup(r)
	if !ok {
		return 0, false
	}
	return d.now().Sub(s.created), true
}

func (d *DevIdentity) CheckCSRF(r *http.Request) bool {
	s, ok := d.lookup(r)
	got := r.Header.Get(DevCSRFHeader)
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.csrf)) == 1
}

// Routes adds /_dev/login and /_dev/revoke for the test harness.
func (d *DevIdentity) Routes(mux *http.ServeMux) {
	if d.static != "" {
		registerApp(mux, d.base, d.static, d)
		// Browser sign-in for the real-browser test: any subject, no password.
		mux.HandleFunc(d.base+LoginPath, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			subject := r.URL.Query().Get("subject")
			if subject == "" {
				subject = "dev-user"
			}
			if !validText(subject, 512) {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			c, _ := d.Login(subject, time.Hour)
			http.SetCookie(w, &http.Cookie{Name: DevCookie, Value: c, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
			http.Redirect(w, r, d.base+"/", http.StatusFound)
		})
	}
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

// DisplayName gives the development identity a name per subject, since it
// has no sign-in claims; production names come from the people registry.
func (d *DevIdentity) DisplayName(s Scope) string { return s.Subject }
