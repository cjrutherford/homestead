package craftfiles

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Limits bound the proxy (ADR-029). Zero values take the defaults.
type Limits struct {
	// MaxUpload is the largest PUT body in bytes (default 512 MiB).
	MaxUpload int64
	// Timeout bounds one upstream request end to end (default 30 min).
	Timeout time.Duration
	// PerSession caps concurrent requests of one craft session (default 8).
	PerSession int
}

// Default limits.
const (
	DefaultMaxUpload  int64 = 512 << 20
	DefaultTimeout          = 30 * time.Minute
	DefaultPerSession       = 8
	maxPropfindBody         = 1 << 20
	maxPropfindReply        = 8 << 20
)

func (l Limits) withDefaults() Limits {
	if l.MaxUpload <= 0 {
		l.MaxUpload = DefaultMaxUpload
	}
	if l.Timeout <= 0 {
		l.Timeout = DefaultTimeout
	}
	if l.PerSession <= 0 {
		l.PerSession = DefaultPerSession
	}
	return l
}

// Link is the non-secret part of the Nextcloud link.
type Link struct {
	BaseURL string `json:"base_url"`
	Account string `json:"account"`
	Root    string `json:"root"`
}

// Proxy forwards the fixed WebDAV method subset to the linked Nextcloud,
// confined to Link.Root. It buffers neither request nor response bodies
// (PROPFIND listings, which are small and need href rewriting, excepted).
type Proxy struct {
	Link     func() (Link, bool, error)
	Password func() (string, error)
	Client   func() *http.Client
	Limits   Limits
	// LimitsFn, when set, supplies the limits per request (desired
	// configuration); Limits is the fallback.
	LimitsFn func() Limits
	Audit    *Auditor
	Log      *slog.Logger
	// Sessions, when set, is re-checked while a request runs so revoking
	// the operator session (logout, password change) cancels in-flight
	// transfers, not just new requests.
	Sessions *Sessions
	// RecheckEvery is that interval (default 30s).
	RecheckEvery time.Duration
	// Tick supplies the re-check ticks; tests inject a manual one.
	Tick func(d time.Duration) (<-chan time.Time, func())
}

const defaultRecheck = 30 * time.Second

// watch cancels a request when its craft session stops being valid. The
// returned stop func ends the watcher; revoked reports whether it fired.
func (p *Proxy) watch(ctx context.Context, cancel context.CancelFunc, sess *Session) (stop func(), revoked *atomic.Bool) {
	revoked = new(atomic.Bool)
	if p.Sessions == nil {
		return func() {}, revoked
	}
	every := p.RecheckEvery
	if every <= 0 {
		every = defaultRecheck
	}
	var ticks <-chan time.Time
	var stopTick func()
	if p.Tick != nil {
		ticks, stopTick = p.Tick(every)
	} else {
		t := time.NewTicker(every)
		ticks, stopTick = t.C, t.Stop
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticks:
				if !p.Sessions.Alive(sess) {
					revoked.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); stopTick() }) }, revoked
}

// Methods is the allowed WebDAV subset.
var Methods = []string{"PROPFIND", http.MethodGet, http.MethodPut, "MKCOL", http.MethodDelete, "MOVE", "COPY", http.MethodHead}

// request headers forwarded upstream; everything else (cookies,
// Authorization, Origin, Referer, X-Forwarded-*) is dropped.
var forwardRequest = []string{"Content-Type", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "Overwrite", "Range", "If-Range", "Accept"}

// response headers returned to the browser; Set-Cookie and
// WWW-Authenticate never are.
var forwardResponse = []string{"Etag", "Last-Modified", "Content-Range", "Accept-Ranges", "Content-Length"}

func upstreamURL(l Link, rel string) (string, error) {
	base, err := url.Parse(l.BaseURL)
	if err != nil {
		return "", err
	}
	root, err := CleanRel(l.Root)
	if err != nil || root == "" {
		return "", fmt.Errorf("craftfiles: invalid root folder")
	}
	segs := append([]string{"remote.php", "dav", "files", l.Account}, strings.Split(root, "/")...)
	if rel != "" {
		segs = append(segs, strings.Split(rel, "/")...)
	}
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	return strings.TrimSuffix(base.String(), "/") + "/" + strings.Join(segs, "/"), nil
}

// upstreamPrefix is the href prefix of the root folder as Nextcloud
// returns it (escaped), rewritten to the proxy prefix in PROPFIND replies.
func upstreamPrefix(l Link) string {
	base, _ := url.Parse(l.BaseURL)
	root, _ := CleanRel(l.Root)
	segs := append([]string{"remote.php", "dav", "files", l.Account}, strings.Split(root, "/")...)
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	return strings.TrimSuffix(base.EscapedPath(), "/") + "/" + strings.Join(segs, "/")
}

// FilesPrefix is where the proxy's file tree is mounted on a craft host.
const FilesPrefix = "/_homestead/files/f"

type limitedBody struct {
	r        io.Reader
	left     int64
	n        int64
	exceeded bool
}

var errTooLarge = errors.New("upload exceeds the configured limit")

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		// Probe for one more byte: more data means too large.
		var one [1]byte
		n, err := b.r.Read(one[:])
		if n > 0 {
			b.exceeded = true
			return 0, errTooLarge
		}
		if err == nil {
			err = io.EOF
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	b.n += int64(n)
	return n, err
}

// Serve proxies one request. rel is the normalised path under the root.
func (p *Proxy) Serve(w http.ResponseWriter, r *http.Request, sess *Session, rel string) {
	lim := p.Limits
	if p.LimitsFn != nil {
		lim = p.LimitsFn()
	}
	lim = lim.withDefaults()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	var dest string
	status, bytesMoved := p.serve(w, r, sess, rel, lim, &dest)
	p.Audit.Record(Event{
		Operator: sess.Operator, Session: sess.ID, App: sess.App,
		Method: r.Method, Path: "/" + rel, Dest: dest, Status: status, Bytes: bytesMoved,
	})
}

func (p *Proxy) fail(w http.ResponseWriter, status int, msg string) int {
	http.Error(w, msg, status)
	return status
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request, sess *Session, rel string, lim Limits, auditDest *string) (int, int64) {
	if !sess.acquire(lim.PerSession) {
		w.Header().Set("Retry-After", "1")
		return p.fail(w, http.StatusTooManyRequests, "too many concurrent requests for this session"), 0
	}
	defer sess.release()

	link, linked, err := p.Link()
	if err != nil || !linked {
		return p.fail(w, http.StatusServiceUnavailable, "Nextcloud is not linked"), 0
	}
	password, err := p.Password()
	if err != nil || password == "" {
		return p.fail(w, http.StatusServiceUnavailable, "the stored Nextcloud credential is unavailable; relink Nextcloud in Homestead"), 0
	}
	target, err := upstreamURL(link, rel)
	if err != nil {
		return p.fail(w, http.StatusInternalServerError, "invalid Nextcloud link"), 0
	}

	ctx, cancel := context.WithTimeout(r.Context(), lim.Timeout)
	defer cancel()
	stopWatch, revoked := p.watch(ctx, cancel, sess)
	defer stopWatch()

	var body io.Reader
	var lb *limitedBody
	contentLength := int64(0)
	switch r.Method {
	case http.MethodPut:
		if r.ContentLength > lim.MaxUpload {
			return p.fail(w, http.StatusRequestEntityTooLarge, "file exceeds the upload limit"), 0
		}
		lb = &limitedBody{r: r.Body, left: lim.MaxUpload}
		body = lb
		contentLength = r.ContentLength
	case "PROPFIND":
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
		buf, err := io.ReadAll(io.LimitReader(r.Body, maxPropfindBody+1))
		if err != nil || len(buf) > maxPropfindBody {
			return p.fail(w, http.StatusRequestEntityTooLarge, "request body too large"), 0
		}
		body = bytes.NewReader(buf)
		contentLength = int64(len(buf))
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, body)
	if err != nil {
		return p.fail(w, http.StatusInternalServerError, "invalid upstream request"), 0
	}
	if body != nil {
		req.ContentLength = contentLength
		if contentLength == 0 {
			req.Body = http.NoBody
		}
	}
	for _, h := range forwardRequest {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	if r.Method == "PROPFIND" {
		depth := r.Header.Get("Depth")
		if depth != "0" && depth != "1" {
			depth = "1"
		}
		req.Header.Set("Depth", depth)
	}
	if r.Method == "MOVE" || r.Method == "COPY" {
		dest, destRel, err := p.destination(r, link)
		if err != nil {
			return p.fail(w, http.StatusBadRequest, err.Error()), 0
		}
		*auditDest = "/" + destRel
		req.Header.Set("Destination", dest)
	}
	req.SetBasicAuth(link.Account, password)
	req.Header.Set("User-Agent", "homestead-craftfiles")

	resp, err := p.Client().Do(req)
	if err != nil {
		switch {
		case lb != nil && lb.exceeded:
			return p.fail(w, http.StatusRequestEntityTooLarge, "file exceeds the upload limit"), lb.n
		case errors.Is(err, context.DeadlineExceeded):
			return p.fail(w, http.StatusGatewayTimeout, "Nextcloud did not answer in time"), counted(lb)
		case revoked.Load():
			return p.fail(w, http.StatusUnauthorized, "craft session ended"), counted(lb)
		case errors.Is(err, context.Canceled):
			return 499, counted(lb)
		}
		p.logf("upstream request failed", r.Method, err)
		return p.fail(w, http.StatusBadGateway, "could not reach Nextcloud"), counted(lb)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		w.Header().Set("X-Homestead-Upstream", "auth-rejected")
		return p.fail(w, http.StatusBadGateway, "Nextcloud rejected the stored app password; relink Nextcloud in Homestead"), counted(lb)
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return p.fail(w, http.StatusBadGateway, "Nextcloud answered with an unexpected redirect"), counted(lb)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		// Never relay an upstream error body: with no Content-Type on a
		// non-GET it could be sniffed as a page on the craft origin.
		return p.fail(w, resp.StatusCode, fmt.Sprintf("Nextcloud answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))), counted(lb)
	}

	h := w.Header()
	for _, name := range forwardResponse {
		if r.Method == "PROPFIND" && name == "Content-Length" {
			continue
		}
		if v := resp.Header.Get(name); v != "" {
			h.Set(name, v)
		}
	}
	if r.Method == http.MethodGet {
		// User files are data, never a page on the craft origin.
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", "attachment")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	} else if ct := resp.Header.Get("Content-Type"); ct != "" && r.Method == "PROPFIND" {
		h.Set("Content-Type", ct)
	}

	if r.Method == "PROPFIND" {
		reply, err := io.ReadAll(io.LimitReader(resp.Body, maxPropfindReply+1))
		if err != nil || len(reply) > maxPropfindReply {
			return p.fail(w, http.StatusBadGateway, "Nextcloud listing too large"), 0
		}
		reply = bytes.ReplaceAll(reply, []byte(upstreamPrefix(link)), []byte(FilesPrefix))
		h.Set("Content-Length", fmt.Sprint(len(reply)))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(reply)
		return resp.StatusCode, int64(len(reply))
	}
	w.WriteHeader(resp.StatusCode)
	var moved int64
	if r.Method != http.MethodHead {
		moved, _ = io.Copy(w, resp.Body)
	}
	if lb != nil {
		moved = lb.n
	}
	return resp.StatusCode, moved
}

func counted(lb *limitedBody) int64 {
	if lb == nil {
		return 0
	}
	return lb.n
}

func (p *Proxy) logf(msg, method string, err error) {
	if p.Log == nil {
		return
	}
	// Only the error class: url.Error text carries the target URL, which
	// is not secret, but nothing from bodies or headers is ever logged.
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	p.Log.Warn("craft files: "+msg, "method", method, "error", err)
}

// destination maps a MOVE/COPY Destination header (an absolute URL or
// path under the proxy prefix on the same host) to the upstream URL,
// normalised and confined to the root folder.
func (p *Proxy) destination(r *http.Request, link Link) (target, rel string, err error) {
	raw := r.Header.Get("Destination")
	if raw == "" {
		return "", "", errors.New("Destination header required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", errors.New("invalid Destination")
	}
	if u.Host != "" && !strings.EqualFold(u.Host, r.Host) {
		return "", "", errors.New("Destination must be on this host")
	}
	if hasDotSegment(u.Path) {
		return "", "", errors.New("invalid Destination path")
	}
	rest, ok := strings.CutPrefix(u.Path, FilesPrefix+"/")
	if !ok && u.Path != FilesPrefix {
		return "", "", errors.New("Destination must be under the files path")
	}
	rel, err = CleanRel(rest)
	if err != nil || rel == "" {
		return "", "", errors.New("invalid Destination path")
	}
	target, err = upstreamURL(link, rel)
	return target, rel, err
}

// NewClient returns the upstream client: no redirects followed (a
// redirect could leave the linked Nextcloud), and the response header
// wait bounded. tlsConfig may add trusted CAs (the internal CA).
func NewClient(tlsConfig *tls.Config) *http.Client {
	tr := &http.Transport{
		Proxy:                 nil,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
		DisableCompression:    true,
	}
	tr.TLSClientConfig = tlsConfig
	return &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Upstream builds and caches the HTTP client for the linked Nextcloud. When
// Roots returns PEM (the Homestead internal CA root, ADR-030), it is
// trusted in addition to the system roots, so a Nextcloud served from the
// appliance's own edge verifies; the client is rebuilt when the PEM
// changes (CA rotation).
type Upstream struct {
	Roots func() []byte

	mu     sync.Mutex
	pem    string
	client *http.Client
}

// Client returns the current client.
func (u *Upstream) Client() *http.Client {
	var pem []byte
	if u.Roots != nil {
		pem = u.Roots()
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.client != nil && u.pem == string(pem) {
		return u.client
	}
	var cfg *tls.Config
	if len(pem) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(pem)
		cfg = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	u.client, u.pem = NewClient(cfg), string(pem)
	return u.client
}
