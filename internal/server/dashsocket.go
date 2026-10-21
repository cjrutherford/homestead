package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/mdns"
	"homestead/internal/store"
)

// Dashboard socket (S289, ADR-035). The edge reaches the dashboard and API
// over a dedicated unix socket that serves the same mux behind the same
// ADR-020 boundary as the TCP listener. A thin front wrapper, on this
// listener only, takes the client address from X-Forwarded-For and checks
// Host and Origin; it never grants anything the boundary refuses.

// DashboardSocketGroup is the group that may use the dashboard socket in
// addition to root, created by scripts/install-systemd.sh.
const DashboardSocketGroup = "homestead-dashboard"

// ValidateDashboardSocketPath pins --dashboard-socket to the one path the
// edge mounts: RunDashboardSocket removes a stale socket and changes the
// directory's mode and group, which must never touch an arbitrary path.
func ValidateDashboardSocketPath(p string) error {
	if filepath.Clean(p) != docker.DashboardSocketPath {
		return fmt.Errorf("dashboard socket must be %s (the path the edge container mounts), got %q", docker.DashboardSocketPath, p)
	}
	return nil
}

type edgeClientKey struct{}

// edgeClientFrom returns the client address the dashboard front wrapper
// took from X-Forwarded-For. It is only ever set on the dashboard socket.
func edgeClientFrom(ctx context.Context) (string, bool) {
	a, ok := ctx.Value(edgeClientKey{}).(string)
	return a, ok
}

// viaEdge describes a request that came through the edge, for audit.
func viaEdge(r *http.Request, detail string) string {
	addr, ok := edgeClientFrom(r.Context())
	if !ok {
		return detail
	}
	suffix := "via edge from " + addr
	if detail == "" {
		return suffix
	}
	return detail + " (" + suffix + ")"
}

// SetDashboardNames supplies the names the dashboard route answers on
// (the Host and Origin rules). Without it the socket answers 421.
func (s *Server) SetDashboardNames(f func() []string) { s.dashNames = f }

// SetDashboardPublicNames supplies the dashboard's public names, delivered
// by the tunnel (ADR-037). On those names the front wrapper refuses every
// way to sign in except Authentik.
func (s *Server) SetDashboardPublicNames(f func() []string) { s.dashPublicNames = f }

type publicHostKey struct{}

// onPublicHost reports whether the request arrived on a public dashboard
// name (set by the dashboard front wrapper only).
func onPublicHost(r *http.Request) bool {
	v, _ := r.Context().Value(publicHostKey{}).(bool)
	return v
}

// publicHostRefused lists the routes the public dashboard names refuse
// (ADR-037 §4): every session-creating or password-taking route except
// sign-in with Authentik. Claim, setup and claim-window wizards, password
// login and change, recovery, and linking an Authentik account stay on the
// local or overlay addresses. Any request carrying the step-up password is
// refused too, so the local password never crosses the tunnel.
var publicHostRefused = []string{
	"POST /api/v1/claim",
	"POST /api/v1/admins/setup",
	"POST /api/v1/admins/password",
	"POST /api/v1/admins/recover",
	"POST /api/v1/sessions",
	"GET /api/v1/wizards/{id}",
	"PUT /api/v1/wizards/{id}",
	"DELETE /api/v1/wizards/{id}",
	"POST /api/v1/sso/link",
	"DELETE /api/v1/sso/link",
}

// publicRefusals matches publicHostRefused with the same pattern rules as
// the mux, so a path variant cannot slip past.
var publicRefusals = func() *http.ServeMux {
	m := http.NewServeMux()
	for _, p := range publicHostRefused {
		m.HandleFunc(p, func(http.ResponseWriter, *http.Request) {})
	}
	return m
}()

// refusedOnPublicHost names why a public-name request is refused, or "".
func refusedOnPublicHost(r *http.Request) string {
	if r.Header.Get(ReauthHeader) != "" {
		return "The local password is never accepted on the public address. Use the local or overlay address for this action."
	}
	if _, pattern := publicRefusals.Handler(r); pattern != "" && pattern != "/" {
		return "This address signs in with Authentik only. Claim, password sign-in, recovery and linking work on the local or overlay address."
	}
	return ""
}

// forwardedClient parses the single X-Forwarded-For value Caddy sets.
// Exactly one valid, non-loopback IP is accepted.
func forwardedClient(r *http.Request) (string, bool) {
	vals := r.Header.Values("X-Forwarded-For")
	if len(vals) != 1 {
		return "", false
	}
	raw := strings.TrimSpace(vals[0])
	if raw == "" || strings.ContainsAny(raw, ", \t") {
		return "", false
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return "", false
	}
	addr = addr.Unmap().WithZone("")
	if addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() {
		return "", false
	}
	return addr.String(), true
}

// hostMatches compares a Host header to the allowed names, ignoring case
// and an explicit :443.
func hostMatches(host string, names []string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		if h[i+1:] != "443" {
			return false
		}
		h = h[:i]
	}
	for _, n := range names {
		if h == strings.ToLower(n) {
			return true
		}
	}
	return false
}

// dashboardFront is the front wrapper of the dashboard socket.
func (s *Server) dashboardFront(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, ok := forwardedClient(r)
		if !ok {
			http.Error(w, "X-Forwarded-For must be exactly one client address", http.StatusBadRequest)
			return
		}
		var names []string
		if s.dashNames != nil {
			names = s.dashNames()
		}
		if !hostMatches(r.Host, names) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		public := s.dashPublicNames != nil && hostMatches(r.Host, s.dashPublicNames())
		if public {
			if reason := refusedOnPublicHost(r); reason != "" {
				w.Header().Set("X-Homestead-Error", ErrorCodePublicHost)
				http.Error(w, reason, http.StatusForbidden)
				return
			}
		}
		_, hasBearer := bearerToken(r.Header.Get("Authorization"))
		_, cookieErr := r.Cookie(SessionCookieName)
		hasCookie := cookieErr == nil
		if hasBearer && hasCookie {
			http.Error(w, "send either a session cookie or a bearer token, not both", http.StatusBadRequest)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !originMatches(origin, names) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
		} else if hasCookie && isMutating(r.Method) {
			http.Error(w, "Origin header required", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), edgeClientKey{}, client)
		if public {
			ctx = context.WithValue(ctx, publicHostKey{}, true)
		}
		r = r.Clone(ctx)
		r.RemoteAddr = net.JoinHostPort(client, "0")
		r.Header.Set("X-Forwarded-For", client)
		// The route is HTTPS only; the cookie must be Secure.
		r.Header.Set("X-Forwarded-Proto", "https")
		next.ServeHTTP(w, r)
	})
}

// originMatches: the Origin must be exactly https://<one of the names>.
func originMatches(origin string, names []string) bool {
	rest, ok := strings.CutPrefix(origin, "https://")
	if !ok {
		return false
	}
	for _, n := range names {
		if rest == strings.ToLower(n) || strings.EqualFold(rest, n) {
			return true
		}
	}
	return false
}

// RunDashboardSocket serves the TCP handler (mux plus ADR-020 boundary)
// behind the front wrapper on the dedicated socket until ctx ends: the
// directory 0750, the socket 0660, group homestead-dashboard when it
// exists. A stale socket is replaced.
func (s *Server) RunDashboardSocket(ctx context.Context, socketPath string) error {
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("server: dashboard socket dir: %w", err)
	}
	gid := -1
	if g, err := user.LookupGroup(DashboardSocketGroup); err == nil {
		if n, err := strconv.Atoi(g.Gid); err == nil {
			gid = n
		}
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		return fmt.Errorf("server: dashboard socket dir mode: %w", err)
	}
	if gid >= 0 {
		_ = os.Chown(dir, -1, gid)
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("server: remove stale dashboard socket: %w", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("server: listen dashboard socket %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		ln.Close()
		return fmt.Errorf("server: chmod dashboard socket: %w", err)
	}
	if gid >= 0 {
		if err := os.Chown(socketPath, -1, gid); err != nil {
			s.log.Warn("dashboard socket group not applied", "group", DashboardSocketGroup, "error", err)
		}
	}
	hs := &http.Server{
		Handler: s.dashboardFront(s.http.Handler), ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10,
	}
	return s.serve(ctx, ln, hs, "dashboard")
}

// dashboardSocketDoctorCheck reports edge drift for the dashboard route
// (ADR-035): the running Caddy lacks the socket mount, or the socket is
// missing. mounted and socketPath are seams for tests.
func dashboardSocketDoctorCheck(st *store.Store, mounted func(context.Context) (bool, error), socketPath string) func() []doctor.Check {
	return func() []doctor.Check {
		const name = "edge-dashboard-socket"
		set, err := loadEdgeRoutes(st)
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot read the edge route table: " + err.Error()}}
		}
		if _, ok := set.DashboardRoute(); !ok {
			if held, conflict := defaultNameHolder(set); conflict {
				return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
					Detail: fmt.Sprintf("the default dashboard route cannot be created because another edge route already uses %q: rename or remove that route, or choose another dashboard name with homesteadctl dashboard address set NAME", held)}}
			}
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: "no dashboard route is recorded, so the dashboard has no local network address: set one with homesteadctl dashboard address set NAME"}}
		}
		if _, err := os.Stat(socketPath); err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: fmt.Sprintf("the dashboard socket %s is missing: the local network address does not work until the daemon creates it (restart homestead-server)", socketPath)}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ok, err := mounted(ctx)
		switch {
		case err != nil:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot inspect the edge container: " + err.Error()}}
		case !ok:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: docker.ErrDashboardMountMissing.Error()}}
		}
		return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "the edge mounts the dashboard socket directory"}}
	}
}

// mdnsDoctorCheck says when the appliance's .local name is unavailable
// because the mDNS responder could not bind (ADR-035 amendment, S221).
func mdnsDoctorCheck(state func() mdns.State) func() []doctor.Check {
	return func() []doctor.Check {
		const name = "dashboard-mdns-name"
		st := state()
		switch {
		case !st.Known:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: "the mDNS responder has not started, so " + mdns.DefaultHostname + ".local is not answering yet"}}
		case st.Err != "":
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: mdns.DefaultHostname + ".local is unavailable: the mDNS responder could not start (" + st.Err +
					"). Another program (Avahi, systemd-resolved) likely holds UDP 5353; stop it or use the local dashboard name"}}
		case !st.Running:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: mdns.DefaultHostname + ".local is not answering: the mDNS responder stopped"}}
		}
		checks := []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: mdns.DefaultHostname + ".local is answered by the mDNS responder"}}
		if len(st.Conflicts) > 0 {
			// Homestead does not fight for a name (ADR-039): the owner
			// renames the app or the other device.
			checks = append(checks, doctor.Check{Name: "mdns-name-conflict", Status: doctor.StatusWarn,
				Detail: "another device also answers " + strings.Join(st.Conflicts, ", ") +
					" over mDNS; devices may reach either. Rename the app's local name or the other device"})
		}
		return checks
	}
}
