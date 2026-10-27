package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/cert"
	"homestead/internal/craftfiles"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// Craft file storage (S254, ADR-029, ADR-032). The management routes
// (status, plan, approve, local-only, launch) live on the TCP mux as
// ordinary session routes. The file proxy itself is served only on a
// dedicated unix socket that the Caddy stack mounts; its routes are
// classified craft-session (ADR-020) and the full API is never on it.

// CraftSocketGroup is the group that may use the craft socket in addition
// to root, created by scripts/install-systemd.sh.
const CraftSocketGroup = "homestead-craft"

// CraftFiles bundles what the craft file listener needs.
type CraftFiles struct {
	Manager  *craftfiles.Manager
	Sessions *craftfiles.Sessions
	Handler  *craftfiles.Handler
	Auditor  *craftfiles.Auditor
}

// authOperator adapts the operator session store (ADR-020) for craft
// sessions: logout, password change and expiry end them at the next use.
type authOperator struct{ svc *auth.Service }

func (a authOperator) Check(token string) (time.Time, error) {
	v, err := a.svc.Verify(token)
	if errors.Is(err, auth.ErrInvalidSession) {
		return time.Time{}, craftfiles.ErrOperatorInvalid
	}
	if err != nil {
		return time.Time{}, craftfiles.ErrOperatorUnavailable
	}
	exp := v.ExpiresAt
	if !v.IdleExpiresAt.IsZero() && v.IdleExpiresAt.Before(exp) {
		exp = v.IdleExpiresAt
	}
	return exp, nil
}

// CraftFilesConfig wires MountCraftFiles; zero Limits take the defaults.
type CraftFilesConfig struct {
	Store    *store.Store
	Secrets  *secret.Store
	Auth     *auth.Service
	Local    *cert.LocalTLS // optional: trust the internal CA for Nextcloud
	YAMLPath string
	Limits   craftfiles.Limits
	// Settings supplies the desired-configuration limits per request.
	Settings func() map[string]any
	Now      func() time.Time
}

// MountCraftFiles registers the management routes on the TCP mux and
// builds the craft-socket handler.
func (s *Server) MountCraftFiles(c CraftFilesConfig) *CraftFiles {
	auditSvc := audit.New(c.Store)
	record := func(actor, action, outcome, detail string) {
		if _, err := auditSvc.Record(audit.Entry{Actor: actor, Action: action, Outcome: outcome, Detail: detail}); err != nil {
			s.log.Warn("audit record failed", "action", action, "error", err)
		}
	}
	up := &craftfiles.Upstream{EdgeHosts: func() map[string]bool {
		set, err := loadEdgeRoutes(c.Store)
		if err != nil {
			return nil
		}
		hosts := map[string]bool{}
		for _, r := range set.Routes {
			hosts[string(r.Host)] = true
		}
		return hosts
	}}
	if c.Local != nil {
		up.Roots = func() []byte {
			pem, err := c.Local.CA.RootPEM()
			if err != nil {
				return nil
			}
			return pem
		}
	}
	mgr := &craftfiles.Manager{
		DB: c.Store.DB(), Secrets: c.Secrets, Client: up.Client, YAMLPath: c.YAMLPath, Now: c.Now,
		Audit: record,
		Routes: func() ([]craftfiles.CraftRoute, error) {
			set, err := loadEdgeRoutes(c.Store)
			if err != nil {
				return nil, err
			}
			var out []craftfiles.CraftRoute
			// A path route answers on the dashboard's names (ADR-045).
			for _, r := range docker.ServedRoutes(set.Routes, set.Routes) {
				if r.CraftApp == "" {
					continue
				}
				cr := craftfiles.CraftRoute{App: r.CraftApp, Host: string(r.Host), TLS: r.TLS != docker.TLSOff, Path: r.Path}
				if r.IsPath() {
					for _, n := range append(append([]docker.Hostname{r.Host}, r.Aliases...), r.Tunnel...) {
						cr.Names = append(cr.Names, string(n))
					}
				}
				out = append(out, cr)
			}
			return out, nil
		},
	}
	_ = mgr.SyncYAML()
	sessions := craftfiles.NewSessions(authOperator{c.Auth})
	if c.Now != nil {
		sessions.Now = c.Now
	}
	limits := c.Limits
	auditor := &craftfiles.Auditor{Now: c.Now, Sink: func(ev craftfiles.Event) {
		detail := fmt.Sprintf("craft=%s session=%s %s %s status=%d bytes=%d", ev.App, ev.Session, ev.Method, ev.Path, ev.Status, ev.Bytes)
		if ev.Count > 1 {
			detail += fmt.Sprintf(" x%d", ev.Count)
		}
		outcome := audit.OutcomeOK
		if ev.Status >= 400 {
			outcome = audit.OutcomeError
		}
		record(ev.Operator, "craftfiles.file", outcome, detail)
	}}
	proxy := &craftfiles.Proxy{
		Link: mgr.CurrentLink, Password: mgr.Password, Client: up.Client,
		Limits: limits, Audit: auditor, Log: s.log, Sessions: sessions,
	}
	if c.Settings != nil {
		proxy.LimitsFn = limitsCache(c.Settings, limits)
	}
	info := func(app string) (craftfiles.SessionInfo, error) {
		i, err := mgr.Info(app)
		lim := proxy.Limits
		if proxy.LimitsFn != nil {
			lim = proxy.LimitsFn()
		}
		i.MaxUpload = lim.MaxUpload
		if i.MaxUpload <= 0 {
			i.MaxUpload = craftfiles.DefaultMaxUpload
		}
		return i, err
	}
	h := &craftfiles.Handler{Sessions: sessions, Proxy: proxy, Info: info, Log: s.log, Audit: auditor}
	cf := &CraftFiles{Manager: mgr, Sessions: sessions, Handler: h, Auditor: auditor}
	s.files = cf

	s.craftMux = newRecordingMux()
	for _, r := range h.Routes() {
		s.craftMux.HandleFunc(r.Pattern, r.Handler)
	}
	s.craftHTTP.Handler = h.Guard(s.craftClassified())

	s.mux.HandleFunc("GET /api/v1/craft/files", func(w http.ResponseWriter, r *http.Request) {
		st, err := mgr.Status()
		if err != nil {
			http.Error(w, "failed to read craft file storage", http.StatusInternalServerError)
			return
		}
		writeJSON(w, st)
	})
	s.mux.HandleFunc("POST /api/v1/craft/files/plan", func(w http.ResponseWriter, r *http.Request) {
		var req craftfiles.PlanRequest
		if err := decodeBody(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		plan, err := mgr.Plan(req, operatorActor(r))
		if err != nil {
			writeCraftFilesError(w, err)
			return
		}
		writeJSON(w, plan)
	})
	s.mux.HandleFunc("POST /api/v1/craft/files/approve", func(w http.ResponseWriter, r *http.Request) {
		var req craftfiles.ApproveRequest
		if err := decodeBody(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		st, err := mgr.Approve(r.Context(), req, operatorActor(r))
		if err != nil {
			writeCraftFilesError(w, err)
			return
		}
		writeJSON(w, st)
	})
	s.mux.HandleFunc("POST /api/v1/craft/files/local-only", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			App       string `json:"app"`
			LocalOnly bool   `json:"local_only"`
		}
		if err := decodeBody(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := mgr.SetLocalOnly(req.App, req.LocalOnly, operatorActor(r)); err != nil {
			writeCraftFilesError(w, err)
			return
		}
		st, err := mgr.Status()
		if err != nil {
			http.Error(w, "failed to read craft file storage", http.StatusInternalServerError)
			return
		}
		writeJSON(w, st)
	})
	s.mux.HandleFunc("POST /api/v1/craft/files/launch", func(w http.ResponseWriter, r *http.Request) {
		op, ok := requireOperator(w, r)
		if !ok {
			return
		}
		var req struct {
			App string `json:"app"`
		}
		if err := decodeBody(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		res, err := mgr.Launch(sessions, op.Token, op.Session.Username, op.Session.ExpiresAt, req.App, host)
		if err != nil {
			writeCraftFilesError(w, err)
			return
		}
		writeJSON(w, res)
	})
	return cf
}

func writeCraftFilesError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, craftfiles.ErrInput), errors.Is(err, craftfiles.ErrNoHook):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, craftfiles.ErrNotLinked), errors.Is(err, craftfiles.ErrAlreadyLinked),
		errors.Is(err, craftfiles.ErrLocalOnly), errors.Is(err, craftfiles.ErrNoRoute):
		status = http.StatusConflict
	case errors.Is(err, craftfiles.ErrNoPlan):
		status = http.StatusNotFound
	case errors.Is(err, craftfiles.ErrUpstream):
		status = http.StatusBadGateway
	case errors.Is(err, craftfiles.ErrBusy):
		status = http.StatusTooManyRequests
	}
	if status == http.StatusInternalServerError {
		http.Error(w, "craft file storage failed", status)
		return
	}
	http.Error(w, err.Error(), status)
}

// CraftRoutes lists every pattern on the craft socket mux (classification
// test).
func (s *Server) CraftRoutes() []string {
	if s.craftMux == nil {
		return nil
	}
	return append([]string(nil), s.craftMux.patterns...)
}

// CraftHandler exposes the craft listener handler for tests.
func (s *Server) CraftHandler() http.Handler { return s.craftHTTP.Handler }

// craftClassified refuses any craft-mux route that is not classified
// craft-session before it runs (ADR-020): deny by default, like the TCP
// boundary.
func (s *Server) craftClassified() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := s.craftMux.Handler(r)
		if pattern != "" {
			if class, ok := Classify(pattern); !ok || class != ClassCraftSession {
				http.Error(w, "route not classified", http.StatusForbidden)
				return
			}
		}
		s.craftMux.ServeHTTP(w, r)
	})
}

// ValidateCraftSocketPath pins --craft-socket to the one path the edge
// mounts: RunCraftSocket removes a stale socket and changes the directory's
// mode and group, which must never touch an arbitrary path.
func ValidateCraftSocketPath(p string) error {
	if filepath.Clean(p) != docker.CraftSocketPath {
		return fmt.Errorf("craft socket must be %s (the path the edge container mounts), got %q", docker.CraftSocketPath, p)
	}
	return nil
}

// RevokeCraftSessions ends an operator's craft sessions and pending
// launch codes (a password change keeps the operator session, so it must
// end them explicitly).
func (s *Server) RevokeCraftSessions(username string) {
	if s.files != nil {
		s.files.Sessions.RevokeOperator(username)
	}
}

// RunCraftSocket serves the craft file handlers on the dedicated socket
// until ctx ends (ADR-032): directory 0750, socket 0660, group
// homestead-craft when it exists. A stale socket is replaced; the
// directory is created if the unit did not.
func (s *Server) RunCraftSocket(ctx context.Context, socketPath string) error {
	if s.files == nil {
		return nil
	}
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("server: craft socket dir: %w", err)
	}
	gid := -1
	if g, err := user.LookupGroup(CraftSocketGroup); err == nil {
		if n, err := strconv.Atoi(g.Gid); err == nil {
			gid = n
		}
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		return fmt.Errorf("server: craft socket dir mode: %w", err)
	}
	if gid >= 0 {
		_ = os.Chown(dir, -1, gid)
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("server: remove stale craft socket: %w", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("server: listen craft socket %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		ln.Close()
		return fmt.Errorf("server: chmod craft socket: %w", err)
	}
	if gid >= 0 {
		if err := os.Chown(socketPath, -1, gid); err != nil {
			s.log.Warn("craft socket group not applied", "group", CraftSocketGroup, "error", err)
		}
	}
	done := make(chan struct{})
	go func() { defer close(done); s.files.Auditor.Run(ctx) }()
	// No ReadTimeout/WriteTimeout: uploads and downloads stream for as long
	// as the proxy's per-request timeout allows (ADR-029); that timeout
	// and the handler's per-request read deadlines bound each request.
	hs := &http.Server{
		Handler: s.craftHTTP.Handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10,
	}
	err = s.serve(ctx, ln, hs, "craft")
	<-done
	return err
}

// limitsCache reads the configured limits at most every few seconds.
func limitsCache(read func() map[string]any, fallback craftfiles.Limits) func() craftfiles.Limits {
	var mu sync.Mutex
	var at time.Time
	cur := fallback
	return func() craftfiles.Limits {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) > 5*time.Second {
			cur = craftfiles.LimitsOf(read())
			at = time.Now()
		}
		return cur
	}
}

// craftFilesDoctorCheck reports edge drift for craft file storage
// (ADR-032): a recorded craft route is served by an edge that predates the
// socket mount, or the daemon's craft socket is missing. mounted and
// socketPath are seams for tests.
func craftFilesDoctorCheck(st *store.Store, mounted func(context.Context) (bool, error), socketPath string) func() []doctor.Check {
	return func() []doctor.Check {
		const name = "edge-craft-socket"
		set, err := loadEdgeRoutes(st)
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot read the edge route table: " + err.Error()}}
		}
		if !set.HasCraftRoutes() {
			return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "no craft routes recorded"}}
		}
		if _, err := os.Stat(socketPath); err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: fmt.Sprintf("the craft file socket %s is missing: craft apps stay browser-local until the daemon creates it (restart homestead-server)", socketPath)}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ok, err := mounted(ctx)
		switch {
		case err != nil:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot inspect the edge container: " + err.Error()}}
		case !ok:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: "the edge container does not mount " + docker.CraftSocketDir + ", so craft file storage cannot reach the daemon: redeploy the edge (homesteadctl edge deploy)"}}
		}
		return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "the edge mounts the craft file socket directory"}}
	}
}

// edgeUpstreamDoctorCheck flags a routed service that shares no network
// with the edge proxy (installed before S249 attached routed services to
// homestead-edge): the edge answers 502 for it and a craft file link to
// its address fails. lister and the container name are seams for tests.
func edgeUpstreamDoctorCheck(st *store.Store, lister func(context.Context) ([]docker.Container, error)) func() []doctor.Check {
	return func() []doctor.Check {
		const name = "edge-upstream-networks"
		set, err := loadEdgeRoutes(st)
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot read the edge route table: " + err.Error()}}
		}
		if len(set.Routes) == 0 {
			return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "no routes recorded"}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		all, err := lister(ctx)
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot list containers: " + err.Error()}}
		}
		var edge map[string]bool
		for _, c := range all {
			for _, n := range c.Names {
				if strings.TrimPrefix(n, "/") == docker.CaddyContainerName {
					edge = map[string]bool{}
					for _, nw := range c.Networks {
						edge[nw] = true
					}
				}
			}
		}
		if edge == nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "the edge proxy is not running; nothing to compare"}}
		}
		var bad []string
		for _, r := range set.Routes {
			found, shares := false, false
			for _, c := range all {
				if c.Labels["com.docker.compose.service"] != r.Target.Host {
					continue
				}
				found = true
				for _, nw := range c.Networks {
					if edge[nw] {
						shares = true
					}
				}
			}
			if found && !shares {
				bad = append(bad, fmt.Sprintf("%s (%s)", r.Host, r.Target.Host))
			}
		}
		if len(bad) > 0 {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: fmt.Sprintf("routed services not attached to %s, so the edge cannot reach them: %s; reinstall the package, or attach the container with `docker network connect --alias <service> %s <container>`, the alias being the route target name (S249 installs attach routed services themselves)",
					docker.NetworkEdge, strings.Join(bad, ", "), docker.NetworkEdge)}}
		}
		return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "every routed service shares a network with the edge"}}
	}
}
