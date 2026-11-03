package everything

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReadPrefix is the route under the application's base path that serves a
// read link; the token follows.
const ReadPrefix = "/_homestead/read/"

// Rate limits for the read route (ADR-058): per client address and per app
// (one bridge process serves one app) per minute. A token that does not work
// costs double.
const (
	ReadPerClientPerMinute = 60
	ReadPerAppPerMinute    = 600
)

// DefaultTrustedProxies are the peers whose X-Forwarded-For the bridge
// believes: loopback and private ranges. The Homestead edge (Caddy) reaches an
// app over the platform's private Docker network, apps publish no port, and
// Caddy replaces any client-sent X-Forwarded-For (its trusted_proxies is
// unset), so the header is the address Caddy saw. A bridge whose port is
// published to a LAN must set the list to "none".
var DefaultTrustedProxies = []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}

// SetTrustedProxies sets the peer networks whose X-Forwarded-For is believed.
// Empty selects DefaultTrustedProxies; "none" trusts no peer.
func (s *Store) SetTrustedProxies(cidrs []string) error {
	if len(cidrs) == 0 {
		cidrs = DefaultTrustedProxies
	}
	var nets []*net.IPNet
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" || c == "none" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return fmt.Errorf("everything: trusted proxy %q: %w", c, err)
		}
		nets = append(nets, n)
	}
	s.trusted, s.trustedSet = nets, true
	return nil
}

func (s *Store) trustedNets() []*net.IPNet {
	if !s.trustedSet {
		_ = s.SetTrustedProxies(nil)
	}
	return s.trusted
}

// clientAddr is the address a rate limit counts. X-Forwarded-For is believed
// only when the TCP peer is a trusted proxy (the edge); then the rightmost
// valid entry is used, because that is the one the edge itself appended or
// set. Anything the client sent is to its left. IPv6 is grouped by /64.
func clientAddr(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, perr := netip.ParseAddr(host)
	if perr != nil {
		return "unknown"
	}
	addr = addr.Unmap()
	in := false
	for _, n := range trusted {
		if n.Contains(addr.AsSlice()) {
			in = true
			break
		}
	}
	if in {
		var parts []string
		for _, v := range r.Header.Values("X-Forwarded-For") {
			parts = append(parts, strings.Split(v, ",")...)
		}
		if len(parts) > 0 {
			// Only the rightmost entry counts; if it is not an address the
			// peer (the edge) is used rather than anything the client sent.
			if a, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
				addr = a.Unmap()
			}
		}
	}
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

// rateWindow is a one minute sliding window of weighted hits.
type rateWindow struct {
	mu   sync.Mutex
	hits map[string][]rateHit
}

type rateHit struct {
	at   time.Time
	cost int
}

func (l *rateWindow) used(key string, now time.Time) int {
	cut := now.Add(-time.Minute)
	kept := l.hits[key][:0]
	n := 0
	for _, h := range l.hits[key] {
		if h.at.After(cut) {
			kept = append(kept, h)
			n += h.cost
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = kept
	}
	return n
}

func (l *rateWindow) add(key string, cost int, now time.Time) {
	if l.hits == nil {
		l.hits = map[string][]rateHit{}
	}
	l.hits[key] = append(l.hits[key], rateHit{now, cost})
}

// readLimiter holds the per-client and whole-route windows.
type readLimiter struct {
	mu     sync.Mutex
	client rateWindow
	app    rateWindow
}

// allow charges one request to the client and the route; false when either
// window is full (a refused request is not charged).
func (l *readLimiter) allow(client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client.used(client, now)+1 > ReadPerClientPerMinute || l.app.used("", now)+1 > ReadPerAppPerMinute {
		return false
	}
	l.client.add(client, 1, now)
	l.app.add("", 1, now)
	return true
}

// penalize charges the second unit of a miss.
func (l *readLimiter) penalize(client string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.client.add(client, 1, now)
	l.app.add("", 1, now)
}

var readTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// readTypes is the fixed Content-Type allowlist by extension; anything else is
// application/octet-stream.
var readTypes = map[string]string{
	".json": "application/json",
	".txt":  "text/plain; charset=utf-8",
	".ics":  "text/calendar; charset=utf-8",
	".md":   "text/markdown; charset=utf-8",
	".csv":  "text/csv; charset=utf-8",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".pdf":  "application/pdf",
}

// readType returns the response type and whether it is shown inline: only
// JSON, plain text and calendar feeds are; everything else downloads.
func readType(key string) (string, bool) {
	t, ok := readTypes[strings.ToLower(path.Ext(key))]
	if !ok {
		return "application/octet-stream", false
	}
	return t, strings.HasPrefix(t, "application/json") || strings.HasPrefix(t, "text/plain") || strings.HasPrefix(t, "text/calendar")
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func downloadName(key string) string {
	n := unsafeName.ReplaceAllString(path.Base(key), "_")
	if n == "" || n == "." || n == ".." || len(n) > 120 {
		return "download"
	}
	return n
}

// serveRead answers GET and HEAD <base>/_homestead/read/<token>. It ignores
// and sets no cookie, reads no Authorization header, and answers one plain 404
// for every token that does not work.
func (h *handler) serveRead(w http.ResponseWriter, r *http.Request) { h.serveLink(w, r, false) }

// serveView answers GET and HEAD <base>/_homestead/view/<token>: the same
// token, expiry, revocation, rate limit and uniform 404 as serveRead, with the
// file rendered as a script-free page (viewer.go).
func (h *handler) serveView(w http.ResponseWriter, r *http.Request) { h.serveLink(w, r, true) }

func (h *handler) serveLink(w http.ResponseWriter, r *http.Request, view bool) {
	prefix := ReadPrefix
	if view {
		prefix = ViewPrefix
	}
	hd := w.Header()
	hd.Set("Cache-Control", "private, no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("Cross-Origin-Resource-Policy", "same-origin")
	if view {
		hd.Set("Content-Security-Policy", ViewerPolicy)
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("X-Robots-Tag", "noindex, nofollow")
	} else {
		hd.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hd.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	client := clientAddr(r, h.store.trustedNets())
	now := h.now()
	if !h.reads.allow(client, now) {
		hd.Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	notFound := func() {
		h.reads.penalize(client, now) // a miss costs double
		http.Error(w, "not found", http.StatusNotFound)
	}
	i := strings.Index(r.URL.Path, prefix)
	token := ""
	if i >= 0 {
		token = r.URL.Path[i+len(prefix):]
	}
	if !readTokenRe.MatchString(token) {
		notFound()
		return
	}
	link, value, err := h.store.OpenReadLink(r.Context(), token, now)
	if errors.Is(err, ErrNotFound) {
		notFound()
		return
	}
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if view {
		page := renderViewer(link.Key, value)
		hd.Set("Content-Type", "text/html; charset=utf-8")
		hd.Set("Content-Length", strconv.Itoa(len(page)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(page)
		}
		h.logf("everything: view link id=%s status=200 bytes=%d", link.ID, len(page))
		return
	}
	ctype, inline := readType(link.Key)
	hd.Set("Content-Type", ctype)
	if inline {
		hd.Set("Content-Disposition", "inline")
	} else {
		hd.Set("Content-Disposition", `attachment; filename="`+downloadName(link.Key)+`"`)
	}
	hd.Set("Content-Length", strconv.Itoa(len(value)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(value)
	}
	// Never the token or the URL path: the link id, status and size only.
	h.logf("everything: read link id=%s status=200 bytes=%d", link.ID, len(value))
}

func (h *handler) logf(format string, args ...any) {
	if h.logger != nil {
		h.logger(format, args...)
		return
	}
	log.Printf(format, args...)
}

// handleReadURL serves fs.getReadURL: mint a link to one of the caller's own
// files, on the origin the caller is using. That origin already passed the
// exact allowlist check (the only configured origin; a public name is not an
// origin the bridge accepts until the exposure plan, S433).
func (h *handler) handleReadURL(w http.ResponseWriter, r *http.Request, scope Scope, body request) {
	if body.TTL < 0 || body.TTL > 10*365*24*3600 {
		shareErr(w, http.StatusBadRequest, "invalid_argument")
		return
	}
	token, link, err := h.store.CreateReadLink(r.Context(), scope, body.Key, time.Duration(body.TTL)*time.Second, h.now())
	switch {
	case err == nil:
		url := r.Header.Get("Origin") + h.base + ReadPrefix + token
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"url": url, "id": link.ID, "expires": link.ExpiresAt})
	case errors.Is(err, ErrNotFound):
		shareErr(w, http.StatusNotFound, "path_not_found")
	case errors.Is(err, ErrInvalid):
		shareErr(w, http.StatusBadRequest, "invalid_argument")
	case errors.Is(err, ErrLinkLimit):
		shareErr(w, http.StatusTooManyRequests, "read_link_limit")
	default:
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
	}
}
