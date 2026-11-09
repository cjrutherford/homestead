package everything

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Events (ADR-058 section 6). A person's page opens one Server-Sent Events
// stream (GET EventsPath?client=<id>) and subscribes to subjects with
// CSRF-protected POST data operations (events.subscribe, events.unsubscribe).
// The hub lives in memory, one per bridge process: a restart ends every
// stream and the shim answers by re-reading (a `gap`). Values are never sent;
// an event names an operation and a key the person may already read.

// EventsPath is the same-origin stream route.
const EventsPath = "/_homestead/events"

// Event limits (ADR-058).
const (
	MaxStreamsPerPerson = 5
	MaxSubsPerStream    = 50
	EventQueueLen       = 256
	EventHeartbeat      = 25 * time.Second
	EventMaxAge         = time.Hour
	EventIdleNoSubs     = 2 * time.Minute
	eventWriteTimeout   = 10 * time.Second
)

var clientIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

const (
	subNotif  = "notif"
	subOwn    = "own"
	subHandle = "handle"
)

type eventSub struct {
	subject string
	kind    string
	prefix  string // own: key prefix; handle: the grant's prefix in the owner's scope
	handle  string
	owner   string // handle: the owner's subject
}

type eventMsg struct {
	handle string // set for handle events: the grant is re-checked on delivery
	data   string
}

type eventConn struct {
	app, subject, client string
	req                  *http.Request // kept to re-validate the session cookie
	subs                 map[string]*eventSub
	q                    []eventMsg
	gap                  bool
	noSubsSince          time.Time
	wake                 chan struct{}
	done                 chan struct{}
	doneOnce             sync.Once
}

func (c *eventConn) close() { c.doneOnce.Do(func() { close(c.done) }) }

type eventHub struct {
	mu        sync.Mutex
	conns     map[*eventConn]struct{}
	heartbeat time.Duration
	maxAge    time.Duration
	idle      time.Duration
	queueLen  int
}

func newEventHub() *eventHub {
	return &eventHub{conns: map[*eventConn]struct{}{}, heartbeat: EventHeartbeat, maxAge: EventMaxAge, idle: EventIdleNoSubs, queueLen: EventQueueLen}
}

// enqueue adds a message; overflow collapses the queue into one pending gap.
// Callers hold hub.mu.
func (hb *eventHub) enqueue(c *eventConn, m eventMsg) {
	if len(c.q) >= hb.queueLen {
		c.q, c.gap = nil, true
	} else {
		c.q = append(c.q, m)
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func eventJSON(subject string, ev map[string]any) string {
	b, _ := json.Marshal(map[string]any{"subject": subject, "event": ev})
	return string(b)
}

// publishKV tells the subscribers of a key change in the owner's scope: the
// owner's own-prefix subscriptions and every handle subscription whose grant
// prefix covers the key. Values are never included.
func (hb *eventHub) publishKV(app, owner, writer, client, op, key string) {
	hb.mu.Lock()
	defer hb.mu.Unlock()
	for c := range hb.conns {
		if c.app != app {
			continue
		}
		self := client != "" && c.client == client && c.subject == writer
		for _, s := range c.subs {
			switch {
			case s.kind == subOwn && c.subject == owner && strings.HasPrefix(key, s.prefix):
				hb.enqueue(c, eventMsg{data: eventJSON(s.subject, map[string]any{"op": op, "key": key, "self": self, "viaWorker": false})})
			case s.kind == subHandle && s.owner == owner && strings.HasPrefix(key, s.prefix):
				hb.enqueue(c, eventMsg{handle: s.handle, data: eventJSON(s.subject,
					map[string]any{"op": op, "key": key[len(s.prefix):], "self": self, "viaWorker": false})})
			}
		}
	}
}

// notify sends a notif:app-user event to every stream of one person.
func (hb *eventHub) notify(app, subject, typ string, extra map[string]any) {
	hb.mu.Lock()
	defer hb.mu.Unlock()
	for c := range hb.conns {
		if c.app != app || c.subject != subject {
			continue
		}
		if s := c.subs["notif:app-user"]; s != nil {
			ev := map[string]any{"type": typ}
			for k, v := range extra {
				ev[k] = v
			}
			hb.enqueue(c, eventMsg{data: eventJSON(s.subject, ev)})
		}
	}
}

// dropHandle ends every subscription to a revoked handle at once and tells
// the affected streams to re-read.
func (hb *eventHub) dropHandle(app, handle string) {
	hb.mu.Lock()
	defer hb.mu.Unlock()
	for c := range hb.conns {
		if c.app != app {
			continue
		}
		hit := false
		for k, s := range c.subs {
			if s.kind == subHandle && s.handle == handle {
				delete(c.subs, k)
				hit = true
			}
		}
		if hit {
			c.gap = true
			select {
			case c.wake <- struct{}{}:
			default:
			}
		}
	}
}

func (hb *eventHub) find(app, subject, client string) *eventConn {
	for c := range hb.conns {
		if c.app == app && c.subject == subject && c.client == client {
			return c
		}
	}
	return nil
}

// ---- handler side ----

func (h *handler) publishKVWrite(owner Scope, writer Scope, client, op, key string) {
	if h.store.sharing {
		h.hub.publishKV(owner.App, owner.Subject, writer.Subject, client, op, key)
	}
}

func (h *handler) notify(app, subject, typ string, extra map[string]any) {
	if h.store.sharing {
		h.hub.notify(app, subject, typ, extra)
	}
}

// statusWriter records whether a reply was an error.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(c int) {
	if s.status == 0 {
		s.status = c
	}
	s.ResponseWriter.WriteHeader(c)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func eventsOp(op string) bool { return op == "events.subscribe" || op == "events.unsubscribe" }

// handleEventsOp serves events.subscribe and events.unsubscribe.
func (h *handler) handleEventsOp(w http.ResponseWriter, r *http.Request, scope Scope, body request) {
	if !h.store.sharing {
		shareErr(w, http.StatusForbidden, "events_disabled")
		return
	}
	if !clientIDRe.MatchString(body.Client) || !validText(body.Topic, 1100) {
		shareErr(w, http.StatusBadRequest, "invalid_argument")
		return
	}
	hb := h.hub
	if body.Operation == "events.unsubscribe" {
		hb.mu.Lock()
		if c := hb.find(scope.App, scope.Subject, body.Client); c != nil {
			delete(c.subs, body.Topic)
			if len(c.subs) == 0 {
				c.noSubsSince = time.Now()
			}
		}
		hb.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	sub, status, code := h.authoriseSub(r, scope, body.Topic)
	if code != "" {
		if status == http.StatusUnauthorized {
			http.Error(w, "unauthorized", status)
			return
		}
		shareErr(w, status, code)
		return
	}
	hb.mu.Lock()
	c := hb.find(scope.App, scope.Subject, body.Client)
	switch {
	case c == nil:
		hb.mu.Unlock()
		shareErr(w, http.StatusConflict, "events_not_connected")
		return
	case c.subs[sub.subject] == nil && len(c.subs) >= MaxSubsPerStream:
		hb.mu.Unlock()
		shareErr(w, http.StatusTooManyRequests, "events_subscription_limit")
		return
	}
	c.subs[sub.subject] = sub
	hb.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// authoriseSub applies the read rule of the subject's operation: the person's
// own prefix, or an active grant for the recipient. Other apps' subjects,
// and anything else, are bridge_unsupported.
func (h *handler) authoriseSub(r *http.Request, scope Scope, subject string) (*eventSub, int, string) {
	if subject == "notif:app-user" {
		return &eventSub{subject: subject, kind: subNotif}, 0, ""
	}
	parts := strings.SplitN(subject, ":", 3)
	if len(parts) != 3 || parts[0] != "kv" {
		return nil, http.StatusBadRequest, "bridge_unsupported"
	}
	if parts[1] == AppID(scope.App) {
		if !strings.HasSuffix(parts[2], "*") {
			return nil, http.StatusBadRequest, "bridge_unsupported"
		}
		prefix := strings.TrimSuffix(parts[2], "*")
		if prefix != "" && !validSharePrefix(prefix) {
			return nil, http.StatusBadRequest, "invalid_argument"
		}
		return &eventSub{subject: subject, kind: subOwn, prefix: prefix}, 0, ""
	}
	if parts[2] != "*" || strings.HasPrefix(parts[1], "app-") {
		return nil, http.StatusBadRequest, "bridge_unsupported"
	}
	if !h.fresh(r) { // a recipient-side use needs a young session
		return nil, http.StatusUnauthorized, "unauthorized"
	}
	rv, err := h.store.ResolveGrant(r.Context(), scope, parts[1], GrantKV, "", false)
	switch {
	case err == nil:
		return &eventSub{subject: subject, kind: subHandle, handle: parts[1], owner: rv.Owner.Subject, prefix: rv.Key}, 0, ""
	case errors.Is(err, ErrNotFound):
		return nil, http.StatusNotFound, "kv_handle_not_found"
	}
	return nil, http.StatusInternalServerError, "storage"
}

// serveEvents is the Server-Sent Events stream. It changes no state.
func (h *handler) serveEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// A same-origin GET carries no Origin header; any Origin that is sent
	// must be trusted, and a browser that says the request is not
	// same-origin is refused.
	if o := r.Header.Get("Origin"); o != "" && !h.origins[o] {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	scope, err := h.identity.Authenticate(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.store.sharing {
		shareErr(w, http.StatusForbidden, "events_disabled")
		return
	}
	client := r.URL.Query().Get("client")
	if !clientIDRe.MatchString(client) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if !h.personLive(r.Context(), scope) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	hb := h.hub
	c := &eventConn{app: scope.App, subject: scope.Subject, client: client, req: r, subs: map[string]*eventSub{},
		noSubsSince: time.Now(), wake: make(chan struct{}, 1), done: make(chan struct{})}
	hb.mu.Lock()
	if old := hb.find(scope.App, scope.Subject, client); old != nil { // a reconnect replaces the old stream
		old.close()
		delete(hb.conns, old)
	}
	n := 0
	for o := range hb.conns {
		if o.app == scope.App && o.subject == scope.Subject {
			n++
		}
	}
	if n >= MaxStreamsPerPerson {
		hb.mu.Unlock()
		shareErr(w, http.StatusTooManyRequests, "events_stream_limit")
		return
	}
	hb.conns[c] = struct{}{}
	hb.mu.Unlock()
	defer func() {
		hb.mu.Lock()
		delete(hb.conns, c)
		hb.mu.Unlock()
		c.close()
	}()

	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{}) // the server's read timeout must not end a stream
	write := func(s string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(eventWriteTimeout))
		if _, err := w.Write([]byte(s)); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("X-Accel-Buffering", "no") // proxies that honour it must not buffer
	w.WriteHeader(http.StatusOK)
	if !write("retry: 3000\n\nevent: ready\ndata: {}\n\n") {
		return
	}
	tick := time.NewTicker(hb.heartbeat)
	defer tick.Stop()
	life := time.NewTimer(hb.maxAge)
	defer life.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-life.C:
			return // the client reconnects and is re-validated
		case <-c.wake:
			if !h.drain(ctx, c, write) {
				return
			}
		case <-tick.C:
			if !h.beat(ctx, c, write) {
				return
			}
		}
	}
}

// alive reports whether the stream's person still has a valid session and has
// not been purged.
func (h *handler) alive(ctx context.Context, c *eventConn) bool {
	sc, err := h.identity.Authenticate(c.req)
	return err == nil && sc.App == c.app && sc.Subject == c.subject && h.personLive(ctx, sc)
}

func (h *handler) personLive(ctx context.Context, scope Scope) bool {
	ok, err := ownerUsable(ctx, h.store.db, scope.App, scope.Subject)
	return err == nil && ok
}

// recheck drops handle subscriptions whose grant no longer works for the
// person and reports whether any were dropped.
func (h *handler) recheck(ctx context.Context, c *eventConn, only string) bool {
	h.hub.mu.Lock()
	var hs []*eventSub
	for _, s := range c.subs {
		if s.kind == subHandle && (only == "" || s.handle == only) {
			hs = append(hs, s)
		}
	}
	h.hub.mu.Unlock()
	dropped := false
	for _, s := range hs {
		if _, err := h.store.ResolveGrant(ctx, Scope{App: c.app, Subject: c.subject}, s.handle, GrantKV, "", false); err != nil {
			h.hub.mu.Lock()
			if c.subs[s.subject] == s {
				delete(c.subs, s.subject)
				dropped = true
			}
			h.hub.mu.Unlock()
		}
	}
	return dropped
}

const gapFrame = "event: gap\ndata: {}\n\n"

// drain delivers the queue. Every delivery re-checks the session, the person
// and (for handle events) the grant, so a revoked share stops at once.
func (h *handler) drain(ctx context.Context, c *eventConn, write func(string) bool) bool {
	h.hub.mu.Lock()
	q, gap := c.q, c.gap
	c.q, c.gap = nil, false
	h.hub.mu.Unlock()
	if !h.alive(ctx, c) {
		return false
	}
	if gap && !write(gapFrame) {
		return false
	}
	ok := map[string]bool{}
	dropped := false
	for _, m := range q {
		if m.handle != "" {
			good, seen := ok[m.handle]
			if !seen {
				if h.recheck(ctx, c, m.handle) {
					dropped = true
				}
				h.hub.mu.Lock()
				for _, s := range c.subs {
					if s.kind == subHandle && s.handle == m.handle {
						good = true
					}
				}
				h.hub.mu.Unlock()
				ok[m.handle] = good
			}
			if !good {
				continue
			}
		}
		if !write("event: msg\ndata: " + m.data + "\n\n") {
			return false
		}
	}
	if dropped { // the person re-reads and learns the share is gone
		return write(gapFrame)
	}
	return true
}

// beat is the heartbeat: it re-validates the stream and ends it when it has
// been useless for too long.
func (h *handler) beat(ctx context.Context, c *eventConn, write func(string) bool) bool {
	if !h.alive(ctx, c) {
		return false
	}
	dropped := h.recheck(ctx, c, "")
	h.hub.mu.Lock()
	nsubs, hasHandle := len(c.subs), false
	for _, s := range c.subs {
		if s.kind == subHandle {
			hasHandle = true
		}
	}
	if nsubs == 0 {
		if c.noSubsSince.IsZero() {
			c.noSubsSince = time.Now()
		}
		if time.Since(c.noSubsSince) >= h.hub.idle {
			h.hub.mu.Unlock()
			return false
		}
	}
	h.hub.mu.Unlock()
	if hasHandle && !h.fresh(c.req) {
		return false // stale session: reconnect, and sign in again if needed
	}
	if dropped && !write(gapFrame) {
		return false
	}
	return write(": ping\n\n")
}
