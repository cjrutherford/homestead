package everything

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// net.fetch (ADR-058 section 5). One CSRF-protected same-origin data
// operation: JSON {url, method, headers, body(base64)} in, {status,
// statusText, headers, body(base64)} out. Every request goes through
// NetFence.Client(); there is no other HTTP client here.

// Limits (owner, 2026-11-03; ADR-058 "Limits" and "Accepted limits").
const (
	NetMaxRequestBody  = 2 << 20
	NetMaxResponseBody = 8 << 20
	NetPerMinute       = 60
	NetPerDay          = 5000
	NetPersonConc      = 5
	NetAppConc         = 20
	netMaxHeaders      = 64
	netMaxHeaderValue  = 8192
	netUserAgent       = "Homestead-Everything/1 (+net.fetch)"
	netWriteBudget     = fenceTotalTimeout + 15*time.Second
)

// netMethods are the methods an app may use. CONNECT, TRACE and anything
// else are refused.
var netMethods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true}

// netDropRequest are headers the bridge never forwards (ADR-058): Host, Cookie
// (the bridge has no cookie jar), Proxy-*, Forwarded, X-Forwarded-*, the
// hop-by-hop headers, and the ones the transport sets itself.
var netDropRequest = map[string]bool{
	"host": true, "cookie": true, "forwarded": true, "x-real-ip": true, "user-agent": true,
	"connection": true, "keep-alive": true, "te": true, "trailer": true, "transfer-encoding": true,
	"upgrade": true, "proxy-authorization": true, "proxy-authenticate": true, "proxy-connection": true,
	"content-length": true, "accept-encoding": true, "expect": true,
}

// netAllowResponse is the allowlist of response headers returned to the app.
// Set-Cookie, hop-by-hop headers and anything else are dropped.
var netAllowResponse = map[string]bool{
	"content-type": true, "content-language": true, "content-disposition": true, "content-range": true,
	"accept-ranges": true, "etag": true, "last-modified": true, "cache-control": true, "expires": true,
	"link": true, "retry-after": true, "vary": true, "date": true, "age": true, "allow": true, "location": true,
	"www-authenticate": true,
}

func netAllowedResponseHeader(name string) bool {
	n := strings.ToLower(name)
	return netAllowResponse[n] || strings.HasPrefix(n, "x-ratelimit-")
}

func netDroppedRequestHeader(name string) bool {
	n := strings.ToLower(name)
	return netDropRequest[n] || strings.HasPrefix(n, "proxy-") || strings.HasPrefix(n, "x-forwarded-")
}

func netHeaderName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func netHeaderValue(s string) bool {
	if len(s) > netMaxHeaderValue {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}

// netLimiter enforces per-person (60 a minute, 5000 a day, 5 at once) and
// per-app (20 at once) limits. Keys include the app so apps never share a
// person's budget. State is in memory: a restart resets it.
type netLimiter struct {
	mu     sync.Mutex
	people map[string]*netPerson
	apps   map[string]int
}

type netPerson struct {
	hits   []time.Time // within the last 24 hours, oldest first
	active int
}

// acquire counts one attempt and returns a release function, or false when
// any limit is reached (a refused attempt is not counted).
func (l *netLimiter) acquire(scope Scope, now time.Time) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.people == nil {
		l.people, l.apps = map[string]*netPerson{}, map[string]int{}
	}
	key := scope.App + "\x00" + scope.Subject
	p := l.people[key]
	if p == nil {
		p = &netPerson{}
		l.people[key] = p
	}
	cut := now.Add(-24 * time.Hour)
	i := 0
	for i < len(p.hits) && !p.hits[i].After(cut) {
		i++
	}
	p.hits = p.hits[i:]
	minute := 0
	for j := len(p.hits) - 1; j >= 0 && p.hits[j].After(now.Add(-time.Minute)); j-- {
		minute++
	}
	if minute >= NetPerMinute || len(p.hits) >= NetPerDay || p.active >= NetPersonConc || l.apps[scope.App] >= NetAppConc {
		return nil, false
	}
	p.hits = append(p.hits, now)
	p.active++
	l.apps[scope.App]++
	if len(l.people) > 10000 { // bound memory: drop idle people
		for k, v := range l.people {
			if v.active == 0 && (len(v.hits) == 0 || !v.hits[len(v.hits)-1].After(cut)) {
				delete(l.people, k)
			}
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			p.active--
			l.apps[scope.App]--
		})
	}, true
}

// netFail writes the uniform app-facing error. The class of a refusal
// (blocked address, resolve failure, mail port, redirect, connect) is logged
// but never told to the app, so the operation cannot be used to map the
// appliance's network.
func netFail(w http.ResponseWriter, status int, code string) { shareErr(w, status, code) }

func (h *handler) handleNetFetch(w http.ResponseWriter, r *http.Request, scope Scope, body request) {
	w.Header().Set("Content-Type", "application/json")
	if !h.store.netOn {
		netFail(w, http.StatusForbidden, "net_disabled")
		return
	}
	start := h.now()
	ctx := r.Context()
	person, err := h.store.personUUID(ctx, scope.App, scope.Subject)
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	e := NetLogEntry{Time: start.Unix(), App: scope.App, Person: person, Method: "-", Scheme: "-", Host: "-", Class: "ok"}
	method := strings.ToUpper(body.Method)
	if method == "" {
		method = "GET"
	}
	if netMethods[method] {
		e.Method = method
	}
	u, uerr := url.Parse(body.URL)
	if uerr == nil && u != nil {
		netLogTarget(&e, u)
	}
	finish := func(status int, code string, class string) {
		e.Class = class
		e.DurationMS = h.now().Sub(start).Milliseconds()
		if lerr := h.store.LogNet(ctx, e); lerr != nil {
			netFail(w, http.StatusServiceUnavailable, "net_unavailable")
			return
		}
		netFail(w, status, code)
	}
	release, ok := h.netLim.acquire(scope, start)
	if !ok {
		finish(http.StatusTooManyRequests, "net_rate_limited", "rate_limited")
		return
	}
	defer release()

	// Validate. Nothing here reaches the network.
	if !netMethods[method] || uerr != nil || len(body.URL) > 8192 || len(body.Body) > NetMaxRequestBody ||
		((method == "GET" || method == "HEAD") && len(body.Body) > 0) {
		finish(http.StatusBadRequest, "net_invalid", "invalid")
		return
	}
	if len(body.Headers) > netMaxHeaders {
		finish(http.StatusBadRequest, "net_invalid", "invalid")
		return
	}
	hdr := http.Header{}
	for k, v := range body.Headers {
		if !netHeaderName(k) || !netHeaderValue(v) {
			finish(http.StatusBadRequest, "net_invalid", "invalid")
			return
		}
		if netDroppedRequestHeader(k) {
			continue
		}
		hdr.Set(k, v)
	}
	hdr.Set("User-Agent", netUserAgent)
	if ferr := FenceCheckURL(u); ferr != nil {
		var fe *FenceError
		class := "invalid"
		if errors.As(ferr, &fe) {
			class = fe.Class
		}
		if class == FenceScheme || class == FenceUserinfo || class == FenceHost {
			finish(http.StatusBadRequest, "net_invalid", class)
		} else {
			finish(http.StatusBadGateway, "net_failed", class)
		}
		return
	}

	// The only outbound path: a per-request copy of the fence that notes the
	// address it dials, then NetFence.Client().
	base := h.store.netFence
	var addrMu sync.Mutex
	fence := &NetFence{Resolver: base.Resolver, TLSConfig: base.TLSConfig,
		Dial: func(c context.Context, network, addr string) (net.Conn, error) {
			conn, derr := base.dial(c, network, addr)
			if derr == nil {
				if host, _, serr := net.SplitHostPort(addr); serr == nil {
					addrMu.Lock()
					e.Addr = host
					addrMu.Unlock()
				}
			}
			return conn, derr
		}}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(netWriteBudget))
	rctx, cancel := context.WithTimeout(ctx, fenceTotalTimeout)
	defer cancel()
	var rd io.Reader
	if len(body.Body) > 0 {
		rd = bytes.NewReader(body.Body)
	}
	req, rerr := http.NewRequestWithContext(rctx, method, u.String(), rd)
	if rerr != nil {
		finish(http.StatusBadRequest, "net_invalid", "invalid")
		return
	}
	req.Header = hdr
	e.BytesOut = int64(len(body.Body))
	resp, derr := fence.Client().Do(req)
	if derr != nil {
		code, status, class := netClassify(derr, rctx)
		finish(status, code, class)
		return
	}
	defer resp.Body.Close()
	e.Status = resp.StatusCode
	if resp.ContentLength > NetMaxResponseBody {
		finish(http.StatusRequestEntityTooLarge, "net_too_large", "too_large")
		return
	}
	data, rerr := io.ReadAll(io.LimitReader(resp.Body, NetMaxResponseBody+1))
	e.BytesIn = int64(len(data))
	if rerr != nil {
		code, status, class := netClassify(rerr, rctx)
		finish(status, code, class)
		return
	}
	if len(data) > NetMaxResponseBody {
		// Truncation is an error, never a silent short body.
		e.BytesIn = NetMaxResponseBody
		finish(http.StatusRequestEntityTooLarge, "net_too_large", "too_large")
		return
	}
	out := map[string]string{}
	for k, vs := range resp.Header {
		if netAllowedResponseHeader(k) && len(vs) > 0 {
			out[strings.ToLower(k)] = strings.Join(vs, ", ")
		}
	}
	text := strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))
	e.Class = "ok"
	e.DurationMS = h.now().Sub(start).Milliseconds()
	if lerr := h.store.LogNet(ctx, e); lerr != nil {
		netFail(w, http.StatusServiceUnavailable, "net_unavailable")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": resp.StatusCode, "statusText": text, "headers": out, "body": data})
}

// netLogTarget fills the loggable parts of a URL: never the query, fragment
// or user information.
func netLogTarget(e *NetLogEntry, u *url.URL) {
	if u.Scheme == "http" || u.Scheme == "https" {
		e.Scheme = u.Scheme
	}
	if h := strings.ToLower(u.Hostname()); h != "" {
		if len(h) > 253 {
			h = h[:253]
		}
		e.Host = h
	}
	switch p := u.Port(); {
	case p != "":
		e.Port, _ = strconv.Atoi(p)
	case u.Scheme == "https":
		e.Port = 443
	case u.Scheme == "http":
		e.Port = 80
	}
	if p := u.EscapedPath(); p != "" {
		if len(p) > 200 {
			p = p[:200]
		}
		e.Path = p
	} else {
		e.Path = "/"
	}
}

// netClassify maps a transport failure to the uniform app-facing code and
// the logged class.
func netClassify(err error, ctx context.Context) (code string, status int, class string) {
	var fe *FenceError
	if errors.As(err, &fe) {
		return "net_failed", http.StatusBadGateway, fe.Class
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded || (errors.As(err, &ne) && ne.Timeout()) {
		return "net_timeout", http.StatusGatewayTimeout, "timeout"
	}
	return "net_failed", http.StatusBadGateway, "upstream"
}
