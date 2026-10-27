package craftfiles

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Craft-host constants shared with the edge renderer (internal/docker).
const (
	// SessionPath is the control path under the forwarded prefix.
	SessionPath = "/_homestead/files/session"
	// CookieName is the craft-scoped session cookie.
	CookieName = "homestead_craft"
	// CookiePath scopes the cookie to the forwarded prefix, under the
	// app's path on a path route (ADR-045).
	CookiePath = "/_homestead/files/"

	appHeader    = "X-Homestead-Craft-App"
	hostHeader   = "X-Homestead-Craft-Host"
	prefixHeader = "X-Homestead-Craft-Prefix"
)

var (
	appName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)
	// appPrefix is a path route's prefix as the edge sends it, without
	// its trailing slash (ADR-045).
	appPrefix = regexp.MustCompile(`^/apps/[a-z][a-z0-9-]{0,40}$`)
)

// SessionInfo is what the wrapper may learn about the link: no credential.
type SessionInfo struct {
	Linked    bool   `json:"linked"`
	LocalOnly bool   `json:"local_only"`
	Root      string `json:"root"`
	Mode      string `json:"mode"`
	MaxUpload int64  `json:"max_upload"`
}

// Handler is the craft listener's HTTP surface.
type Handler struct {
	Sessions *Sessions
	Proxy    *Proxy
	// Info reports the link state for an app; errors fail closed.
	Info func(app string) (SessionInfo, error)
	Log  *slog.Logger
	// Audit, when set, records requests denied before the proxy ran.
	Audit *Auditor
}

// deny audits a request refused before Serve. Only the method, the
// normalised path and the status are recorded: never a launch code or
// cookie value.
func (h *Handler) deny(r *http.Request, app, path string, status int) {
	h.Audit.Record(Event{Operator: "unauthenticated", App: app, Method: r.Method, Path: path, Status: status, Count: 1})
}

// Route is one pattern and its handler, so the server registers them on
// a mux that records patterns for the classification test (ADR-020).
type Route struct {
	Pattern string
	Handler http.HandlerFunc
}

// Routes lists every craft listener route: the wrapper page (ADR-045),
// the session control routes and one route per proxied WebDAV method.
// Nothing else is ever served.
func (h *Handler) Routes() []Route {
	routes := []Route{
		{"GET " + WrapperPrefix + "open.html", h.wrapper},
		{"GET " + WrapperPrefix + "open.js", h.wrapper},
		{"GET " + WrapperPrefix + "open.css", h.wrapper},
		{"POST " + SessionPath, h.exchange},
		{"GET " + SessionPath, h.status},
		{"DELETE " + SessionPath, h.end},
	}
	for _, m := range Methods {
		routes = append(routes, Route{m + " " + FilesPrefix + "/{path...}", h.file})
	}
	return routes
}

type edgeKey struct{}

// edgeIdentity is what the edge says about a request: the craft app, the
// name it came on, its scheme and, on a path route, the app's path.
type edgeIdentity struct{ app, host, scheme, prefix string }

// cookiePath is the craft cookie's path for the request's route.
func (id edgeIdentity) cookiePath() string { return id.prefix + CookiePath }

func edgeFrom(ctx context.Context) edgeIdentity {
	v, _ := ctx.Value(edgeKey{}).(edgeIdentity)
	return v
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(strings.Trim(hostport, "[]"))
}

// Guard wraps the craft mux. The listener is reachable only through the
// edge (socket permissions, ADR-032), which sets the craft app and host
// headers from the route itself; requests without them are refused. The
// request Host must equal that craft host, any Origin must be exactly the
// craft host's own origin, and traversal, preflights and cross-site
// fetches are refused before routing. No CORS header is ever set.
func (h *Handler) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Cache-Control", "no-store")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		app := r.Header.Get(appHeader)
		host := strings.ToLower(r.Header.Get(hostHeader))
		if !appName.MatchString(app) || host == "" {
			http.Error(w, "not served through a craft route", http.StatusBadRequest)
			return
		}
		prefix := r.Header.Get(prefixHeader)
		if prefix != "" && !appPrefix.MatchString(prefix) {
			http.Error(w, "not served through a craft route", http.StatusBadRequest)
			return
		}
		if hostOnly(r.Host) != host {
			http.Error(w, "host does not match the craft host", http.StatusMisdirectedRequest)
			return
		}
		// The edge sets X-Forwarded-Proto from the route's own TLS mode
		// (header_up replaces anything the client sent); anything else
		// means the request did not come through a craft route.
		scheme := strings.ToLower(r.Header.Get("X-Forwarded-Proto"))
		if scheme != "http" && scheme != "https" {
			http.Error(w, "not served through a craft route", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodOptions {
			// Preflights are never answered; the design is same-origin.
			http.Error(w, "cross-origin requests are not accepted", http.StatusForbidden)
			return
		}
		if hasDotSegment(r.URL.Path) {
			http.Error(w, "path refused", http.StatusBadRequest)
			return
		}
		// The wrapper's own files may be opened directly, as the image
		// serves them; everything else is same-origin only.
		if !(isWrapper(r) && r.Header.Get("Origin") == "") && !sameOrigin(r, scheme, host) {
			http.Error(w, "cross-origin requests are not accepted", http.StatusForbidden)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), edgeKey{}, edgeIdentity{app, host, scheme, prefix}))
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request, scheme, host string) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != scheme || strings.ToLower(u.Hostname()) != host {
			return false
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	if !IsRead(r.Method) && r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Site") == "" {
		return false // state-changing requests must say where they come from
	}
	return true
}

func (h *Handler) log(msg string, args ...any) {
	if h.Log != nil {
		h.Log.Warn("craft files: "+msg, args...)
	}
}

// authorize resolves the craft cookie; it answers 401 itself on failure.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	id := edgeFrom(r.Context())
	c, err := r.Cookie(CookieName)
	if err != nil {
		h.denyFile(r, id.app, http.StatusUnauthorized)
		http.Error(w, "craft session required", http.StatusUnauthorized)
		return nil, false
	}
	sess, err := h.Sessions.Lookup(c.Value, id.app, id.host)
	switch {
	case err == nil:
		return sess, true
	case errors.Is(err, ErrOperatorUnavailable):
		h.denyFile(r, id.app, http.StatusServiceUnavailable)
		http.Error(w, "session check unavailable", http.StatusServiceUnavailable)
	default:
		h.denyFile(r, id.app, http.StatusUnauthorized)
		clearCookie(w, id)
		http.Error(w, "craft session required", http.StatusUnauthorized)
	}
	return nil, false
}

// denyFile audits a refused request by its normalised path, or a fixed
// placeholder when the path does not normalise.
func (h *Handler) denyFile(r *http.Request, app string, status int) {
	path := SessionPath
	if v, ok := strings.CutPrefix(r.URL.Path, FilesPrefix); ok {
		if rel, err := CleanRel(strings.TrimPrefix(v, "/")); err == nil {
			path = "/" + rel
		} else {
			path = "/?"
		}
	}
	h.deny(r, app, path, status)
}

func clearCookie(w http.ResponseWriter, id edgeIdentity) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: id.cookiePath(), MaxAge: -1,
		Expires: time.Unix(0, 0), HttpOnly: true, Secure: id.scheme == "https", SameSite: http.SameSiteStrictMode})
}

func (h *Handler) exchange(w http.ResponseWriter, r *http.Request) {
	id := edgeFrom(r.Context())
	// The listener has no server-wide ReadTimeout (transfers stream), so
	// the small control body gets its own deadline.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Second))
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.Code == "" {
		http.Error(w, "launch code required", http.StatusBadRequest)
		return
	}
	info, err := h.Info(id.app)
	if err != nil || !info.Linked || info.LocalOnly {
		// Burn the code anyway: a launch for an unlinked app is stale.
		_, _, _ = h.Sessions.Exchange(body.Code, id.app, id.host)
		http.Error(w, "Nextcloud file storage is not enabled for this app", http.StatusConflict)
		return
	}
	token, sess, err := h.Sessions.Exchange(body.Code, id.app, id.host)
	switch {
	case err == nil:
	case errors.Is(err, ErrBusy):
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	case errors.Is(err, ErrOperatorUnavailable):
		http.Error(w, "session check unavailable", http.StatusServiceUnavailable)
		return
	default:
		h.deny(r, id.app, SessionPath, http.StatusUnauthorized)
		http.Error(w, "invalid or expired launch code", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: id.cookiePath(), Expires: sess.Expires(),
		MaxAge:   int(time.Until(sess.Expires()).Seconds()) + 1,
		HttpOnly: true, Secure: id.scheme == "https", SameSite: http.SameSiteStrictMode,
	})
	h.writeStatus(w, id.app, sess)
}

func (h *Handler) writeStatus(w http.ResponseWriter, app string, sess *Session) {
	info, err := h.Info(app)
	if err != nil {
		http.Error(w, "link state unavailable", http.StatusServiceUnavailable)
		return
	}
	a, _ := HookedApp(app)
	info.Mode = a.Mode
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"app": app, "linked": info.Linked, "local_only": info.LocalOnly,
		"root": info.Root, "mode": info.Mode, "max_upload": info.MaxUpload,
		"expires_at": sess.Expires().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.authorize(w, r)
	if !ok {
		return
	}
	h.writeStatus(w, sess.App, sess)
}

func (h *Handler) end(w http.ResponseWriter, r *http.Request) {
	id := edgeFrom(r.Context())
	if c, err := r.Cookie(CookieName); err == nil {
		h.Sessions.End(c.Value)
	}
	clearCookie(w, id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) file(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.authorize(w, r)
	if !ok {
		return
	}
	rel, err := CleanRel(r.PathValue("path"))
	if err != nil {
		http.Error(w, "path refused", http.StatusBadRequest)
		return
	}
	info, err := h.Info(sess.App)
	if err != nil || !info.Linked || info.LocalOnly {
		http.Error(w, "Nextcloud file storage is not enabled for this app", http.StatusConflict)
		return
	}
	h.Proxy.Serve(w, r, sess, rel)
}
