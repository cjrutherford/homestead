package everything

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// evIdentity is a cookie identity for the stream tests: the cookie names a
// session, which tests can end or age.
type evIdentity struct {
	mu   sync.Mutex
	sess map[string]*evSess
}
type evSess struct {
	scope Scope
	age   time.Duration
}

func (i *evIdentity) get(r *http.Request) (*evSess, bool) {
	c, err := r.Cookie("s")
	if err != nil {
		return nil, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	s, ok := i.sess[c.Value]
	return s, ok
}
func (i *evIdentity) Authenticate(r *http.Request) (Scope, error) {
	if s, ok := i.get(r); ok {
		return s.scope, nil
	}
	return Scope{}, ErrInvalid
}
func (i *evIdentity) CheckCSRF(r *http.Request) bool {
	_, ok := i.get(r)
	return ok && r.Header.Get("X-T") == "ok"
}
func (i *evIdentity) SessionAge(r *http.Request) (time.Duration, bool) {
	s, ok := i.get(r)
	if !ok {
		return 0, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return s.age, true
}
func (i *evIdentity) end(cookie string) { i.mu.Lock(); delete(i.sess, cookie); i.mu.Unlock() }
func (i *evIdentity) setAge(cookie string, d time.Duration) {
	i.mu.Lock()
	i.sess[cookie].age = d
	i.mu.Unlock()
}

type evEnv struct {
	t     *testing.T
	store *Store
	id    *evIdentity
	h     *handler
	srv   *httptest.Server
}

func newEvEnv(t *testing.T, sharing bool) *evEnv {
	s, _ := grantStore(t)
	s.SetSharing(sharing)
	id := &evIdentity{sess: map[string]*evSess{
		"alice": {scope: gAlice}, "alice2": {scope: gAlice}, "bob": {scope: gBob}, "carol": {scope: gCarol},
		"nalice": {scope: Scope{App: "notes", Subject: "s-alice"}},
	}}
	hi, err := NewHandler(s, id, []string{"https://home.example"})
	if err != nil {
		t.Fatal(err)
	}
	h := hi.(*handler)
	h.hub.heartbeat = time.Hour // tests that need a beat lower it before connecting
	mux := http.NewServeMux()
	mux.Handle(DataPath, h)
	mux.HandleFunc(EventsPath, h.serveEvents)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &evEnv{t: t, store: s, id: id, h: h, srv: srv}
}

// post sends a data operation as the cookie's session.
func (e *evEnv) post(cookie, body string) (int, map[string]any) {
	req, _ := http.NewRequest("POST", e.srv.URL+DataPath, bytes.NewBufferString(body))
	req.Header.Set("Origin", "https://home.example")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-T", "ok")
	req.AddCookie(&http.Cookie{Name: "s", Value: cookie})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(res.Body)
	if json.Unmarshal(b, &out) != nil {
		out = map[string]any{"text": string(b)}
	}
	return res.StatusCode, out
}

type frame struct{ event, data string }

type stream struct {
	t      *testing.T
	res    *http.Response
	frames chan frame
	raw    *bytes.Buffer
	mu     sync.Mutex
	closed chan struct{}
}

func (e *evEnv) open(cookie, client string, hdr map[string]string) *stream {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+EventsPath+"?client="+client, nil)
	req.AddCookie(&http.Cookie{Name: "s", Value: cookie})
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	st := &stream{t: e.t, res: res, frames: make(chan frame, 1000), raw: &bytes.Buffer{}, closed: make(chan struct{})}
	if res.StatusCode != 200 {
		return st
	}
	e.t.Cleanup(func() { res.Body.Close() })
	go func() {
		defer close(st.closed)
		sc := bufio.NewScanner(res.Body)
		var f frame
		for sc.Scan() {
			line := sc.Text()
			st.mu.Lock()
			st.raw.WriteString(line + "\n")
			st.mu.Unlock()
			switch {
			case line == "":
				if f.event != "" {
					st.frames <- f
				}
				f = frame{}
			case strings.HasPrefix(line, "event: "):
				f.event = line[7:]
			case strings.HasPrefix(line, "data: "):
				f.data = line[6:]
			case strings.HasPrefix(line, ": "):
				st.frames <- frame{event: "comment", data: line[2:]}
			}
		}
	}()
	return st
}

func (s *stream) next() frame {
	s.t.Helper()
	select {
	case f := <-s.frames:
		return f
	case <-time.After(3 * time.Second):
		s.t.Fatal("no frame")
	}
	return frame{}
}

// nextMsg skips pings and returns the next msg frame decoded.
func (s *stream) nextMsg() (string, map[string]any) {
	s.t.Helper()
	for {
		f := s.next()
		if f.event == "comment" {
			continue
		}
		if f.event != "msg" {
			s.t.Fatalf("got %s %s, want msg", f.event, f.data)
		}
		var m struct {
			Subject string
			Event   map[string]any
		}
		if err := json.Unmarshal([]byte(f.data), &m); err != nil {
			s.t.Fatal(err)
		}
		return m.Subject, m.Event
	}
}

func (s *stream) none(d time.Duration) {
	s.t.Helper()
	select {
	case f := <-s.frames:
		if f.event != "comment" {
			s.t.Fatalf("unexpected frame %v", f)
		}
	case <-time.After(d):
	}
}

func (s *stream) ends() {
	s.t.Helper()
	select {
	case <-s.closed:
	case <-time.After(3 * time.Second):
		s.t.Fatal("stream did not end")
	}
}

func (s *stream) ready() {
	s.t.Helper()
	if f := s.next(); f.event != "ready" {
		s.t.Fatalf("first frame %v", f)
	}
}

const (
	cA  = "client-alice-aaaaaaaa"
	cA2 = "client-alice-bbbbbbbb"
	cB  = "client-bob-cccccccccc"
)

func (e *evEnv) sub(cookie, client, subject string) (int, map[string]any) {
	return e.post(cookie, fmt.Sprintf(`{"operation":"events.subscribe","client":%q,"topic":%q}`, client, subject))
}

func (e *evEnv) mustSub(cookie, client, subject string) {
	e.t.Helper()
	if st, out := e.sub(cookie, client, subject); st != 200 {
		e.t.Fatalf("subscribe %s: %d %v", subject, st, out)
	}
}

func TestEventsOwnPrefixSelfAndNoValues(t *testing.T) {
	e := newEvEnv(t, true)
	a := e.open("alice", cA, nil)
	a.ready()
	b := e.open("alice2", cA2, nil)
	b.ready()
	e.mustSub("alice", cA, "kv:app-tasks:tasks:*")
	e.mustSub("alice2", cA2, "kv:app-tasks:tasks:*")
	if st, out := e.post("alice", fmt.Sprintf(`{"operation":"put","kind":"kv","key":"tasks:x","value":%q,"client":%q}`, enc64("SECRET-VALUE"), cA)); st != 200 {
		t.Fatal(st, out)
	}
	subj, ev := a.nextMsg()
	if subj != "kv:app-tasks:tasks:*" || ev["op"] != "set" || ev["key"] != "tasks:x" || ev["self"] != true {
		t.Fatalf("own: %v %v", subj, ev)
	}
	_, ev = b.nextMsg()
	if ev["self"] != false {
		t.Fatalf("second tab must not be self: %v", ev)
	}
	// A key outside the prefix, a failed write and a read send nothing.
	e.post("alice", fmt.Sprintf(`{"operation":"put","kind":"kv","key":"other","value":%q,"client":%q}`, enc64("1"), cA))
	e.post("alice", `{"operation":"get","kind":"kv","key":"tasks:x"}`)
	if st, _ := e.post("alice", fmt.Sprintf(`{"operation":"delete","kind":"kv","key":"tasks:x","client":%q}`, cA)); st != 200 {
		t.Fatal(st)
	}
	if _, ev = a.nextMsg(); ev["op"] != "del" || ev["key"] != "tasks:x" {
		t.Fatalf("delete: %v", ev)
	}
	e.post("alice", fmt.Sprintf(`{"operation":"putbatch","kind":"kv","entries":[{"key":"tasks:b1","value":%q},{"key":"tasks:b2","value":%q}],"client":%q}`, enc64("SECRET-VALUE"), enc64("SECRET-VALUE"), cA))
	a.nextMsg()
	a.nextMsg()
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.Contains(a.raw.String(), "SECRET") || strings.Contains(a.raw.String(), "U0VDUkVU") {
		t.Fatal("a value reached the stream")
	}
	// Bob never sees Alice's own-prefix events, even with the same prefix.
}

func TestEventsHandleSharedWritesAndNotifications(t *testing.T) {
	e := newEvEnv(t, true)
	bob := e.open("bob", cB, nil)
	bob.ready()
	e.mustSub("bob", cB, "notif:app-user")
	alice := e.open("alice", cA, nil)
	alice.ready()
	e.mustSub("alice", cA, "notif:app-user")
	e.mustSub("alice", cA, "kv:app-tasks:tasks:collab:*")
	_, out := e.post("alice", `{"operation":"kv.share","prefix":"tasks:collab:","recipient":"bob","access":"write","label":"L"}`)
	h := out["handle"].(string)
	subj, ev := bob.nextMsg()
	if subj != "notif:app-user" || ev["type"] != "kv.share.received" || ev["handle"] != h {
		t.Fatalf("received: %v %v", subj, ev)
	}
	alice.none(100 * time.Millisecond) // the sharer gets no notice of her own share
	// Bob subscribes to the handle; Carol and unknown handles are refused alike.
	e.mustSub("bob", cB, "kv:"+h+":*")
	st, out := e.sub("carol", "client-carol-dddddddd", "kv:"+h+":*")
	wantCode(t, st, out, 404, "kv_handle_not_found")
	e.open("carol", "client-carol-dddddddd", nil).ready()
	st, out = e.sub("carol", "client-carol-dddddddd", "kv:"+h+":*")
	wantCode(t, st, out, 404, "kv_handle_not_found")
	st, out = e.sub("bob", cB, "kv:nope-nope-nope-nope-n:*")
	wantCode(t, st, out, 404, "kv_handle_not_found")
	// Alice writes: Bob sees a key relative to the share.
	e.post("alice", fmt.Sprintf(`{"operation":"put","kind":"kv","key":"tasks:collab:meta","value":%q,"client":%q}`, enc64("1"), cA))
	subj, ev = bob.nextMsg()
	if subj != "kv:"+h+":*" || ev["key"] != "meta" || ev["op"] != "set" || ev["self"] != false {
		t.Fatalf("recipient event: %v %v", subj, ev)
	}
	alice.nextMsg() // her own-prefix subscription
	// Bob writes through the handle: self for Bob, a plain event for Alice.
	e.post("bob", fmt.Sprintf(`{"operation":"put","kind":"kv","key":"t:1","value":%q,"handle":%q,"client":%q}`, enc64("1"), h, cB))
	_, ev = bob.nextMsg()
	if ev["self"] != true || ev["key"] != "t:1" {
		t.Fatalf("bob self: %v", ev)
	}
	_, ev = alice.nextMsg()
	if ev["self"] != false || ev["key"] != "tasks:collab:t:1" {
		t.Fatalf("alice sees: %v", ev)
	}
	// Carol is told nothing.
	// Revocation: Bob is told, his handle stream stops at once.
	if st, _ = e.post("alice", fmt.Sprintf(`{"operation":"kv.unshare","handle":%q}`, h)); st != 200 {
		t.Fatal(st)
	}
	if f := bob.next(); f.event != "gap" {
		t.Fatalf("revoke: %v", f)
	}
	if subj, ev = bob.nextMsg(); subj != "notif:app-user" || ev["type"] != "kv.share.revoked" {
		t.Fatalf("revoked notice: %v %v", subj, ev)
	}
	e.post("alice", fmt.Sprintf(`{"operation":"put","kind":"kv","key":"tasks:collab:after","value":%q,"client":%q}`, enc64("1"), cA))
	alice.nextMsg()
	bob.none(150 * time.Millisecond)
}

func TestEventsFSShareNotices(t *testing.T) {
	e := newEvEnv(t, true)
	bob := e.open("bob", cB, nil)
	bob.ready()
	e.mustSub("bob", cB, "notif:app-user")
	if err := e.store.Put(context.Background(), gAlice, KindFile, "docs/a.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if st, out := e.post("alice", `{"operation":"fs.share","kind":"file","key":"docs/a.txt","recipient":"bob","access":"read"}`); st != 200 {
		t.Fatal(st, out)
	}
	if _, ev := bob.nextMsg(); ev["type"] != "fs.share.received" {
		t.Fatal(ev)
	}
	e.post("alice", `{"operation":"fs.unshare","kind":"file","key":"docs/a.txt","recipient":"bob"}`)
	if _, ev := bob.nextMsg(); ev["type"] != "fs.share.revoked" {
		t.Fatal(ev)
	}
}

func TestEventsSubjectsAreScoped(t *testing.T) {
	e := newEvEnv(t, true)
	e.open("alice", cA, nil).ready()
	for _, s := range []string{"kv:app-notes:x*", "fs:anything", "notif:other", "kv:app-tasks:x", "garbage", "kv:app-:*", "a:b:c"} {
		st, out := e.sub("alice", cA, s)
		wantCode(t, st, out, 400, "bridge_unsupported")
	}
	if st, out := e.sub("alice", cA, "kv:app-tasks:"+strings.Repeat("a", 2000)+"*"); st != 400 {
		t.Fatal(st, out)
	}
	// Not connected, bad client id, unsubscribe is idempotent.
	st, out := e.sub("alice", "client-alice-zzzzzzzz", "notif:app-user")
	wantCode(t, st, out, 409, "events_not_connected")
	if st, _ = e.sub("alice", "short", "notif:app-user"); st != 400 {
		t.Fatal(st)
	}
	// Another person's stream is not reachable by guessing the client id.
	st, out = e.sub("bob", cA, "notif:app-user")
	wantCode(t, st, out, 409, "events_not_connected")
	if st, _ = e.post("alice", `{"operation":"events.unsubscribe","client":"`+cA+`","topic":"notif:app-user"}`); st != 200 {
		t.Fatal(st)
	}
}

func TestEventsCrossAppAndCrossPersonIsolation(t *testing.T) {
	e := newEvEnv(t, true)
	// The notes bridge shares the database file in tests only; a notes stream
	// must not receive a tasks write, nor see tasks handles.
	n := e.open("nalice", cA, nil)
	n.ready()
	e.mustSub("nalice", cA, "kv:app-notes:*")
	bob := e.open("bob", cB, nil)
	bob.ready()
	e.mustSub("bob", cB, "kv:app-tasks:*")
	e.post("alice", fmt.Sprintf(`{"operation":"put","kind":"kv","key":"k","value":%q}`, enc64("1")))
	n.none(150 * time.Millisecond)
	bob.none(150 * time.Millisecond)
	_, out := e.post("alice", `{"operation":"kv.share","prefix":"k","recipient":"carol","access":"read"}`)
	h := out["handle"].(string)
	st, out := e.sub("nalice", cA, "kv:"+h+":*")
	wantCode(t, st, out, 404, "kv_handle_not_found")
}

func TestEventsLimits(t *testing.T) {
	e := newEvEnv(t, true)
	var ss []*stream
	for i := 0; i < MaxStreamsPerPerson; i++ {
		s := e.open("alice", fmt.Sprintf("client-limit-%08d", i), nil)
		s.ready()
		ss = append(ss, s)
	}
	if s := e.open("alice", "client-limit-99999999", nil); s.res.StatusCode != 429 {
		t.Fatalf("sixth stream: %d", s.res.StatusCode)
	}
	// Bob has his own allowance.
	e.open("bob", cB, nil).ready()
	// The same client reconnecting replaces its old stream.
	re := e.open("alice", "client-limit-00000000", nil)
	re.ready()
	ss[0].ends()
	for i := 0; i < MaxSubsPerStream; i++ {
		e.mustSub("alice", "client-limit-00000000", fmt.Sprintf("kv:app-tasks:p%d*", i))
	}
	st, out := e.sub("alice", "client-limit-00000000", "kv:app-tasks:extra*")
	wantCode(t, st, out, 429, "events_subscription_limit")
	// Re-subscribing an existing subject is not a new subscription.
	e.mustSub("alice", "client-limit-00000000", "kv:app-tasks:p0*")
}

func TestEventsQueueOverflowCollapsesToGap(t *testing.T) {
	hb := newEventHub()
	hb.queueLen = 3
	c := &eventConn{app: "tasks", subject: "s-alice", client: cA, subs: map[string]*eventSub{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	c.subs["kv:app-tasks:*"] = &eventSub{subject: "kv:app-tasks:*", kind: subOwn}
	hb.conns[c] = struct{}{}
	for i := 0; i < 3; i++ {
		hb.publishKV("tasks", "s-alice", "s-alice", "", "set", "k")
	}
	if len(c.q) != 3 || c.gap {
		t.Fatalf("filled: %d %v", len(c.q), c.gap)
	}
	hb.publishKV("tasks", "s-alice", "s-alice", "", "set", "k")
	if len(c.q) != 0 || !c.gap {
		t.Fatalf("overflow: %d %v", len(c.q), c.gap)
	}
}

func TestEventsGapFrameOnTheWire(t *testing.T) {
	e := newEvEnv(t, true)
	e.h.hub.queueLen = 2
	a := e.open("alice", cA, nil)
	a.ready()
	e.mustSub("alice", cA, "kv:app-tasks:*")
	h := e.h.hub
	h.mu.Lock()
	var c *eventConn
	for k := range h.conns {
		c = k
	}
	h.mu.Unlock()
	for i := 0; i < 10; i++ { // a burst bigger than the queue
		h.publishKV("tasks", "s-alice", "s-alice", "", "set", "k")
	}
	sawGap := false
	deadline := time.After(3 * time.Second)
	for !sawGap {
		select {
		case f := <-a.frames:
			if f.event == "gap" {
				sawGap = true
			}
		case <-deadline:
			t.Fatal("no gap frame")
		}
	}
	_ = c
}

func TestEventsHeartbeatAndStreamHeaders(t *testing.T) {
	e := newEvEnv(t, true)
	e.h.hub.heartbeat = 30 * time.Millisecond
	a := e.open("alice", cA, nil)
	if ct := a.res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatal(ct)
	}
	if cc := a.res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") || !strings.Contains(cc, "no-transform") {
		t.Fatal(cc)
	}
	if a.res.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("proxy buffering header")
	}
	a.ready()
	if f := a.next(); f.event != "comment" || f.data != "ping" {
		t.Fatalf("heartbeat: %v", f)
	}
}

func TestEventsStreamEndings(t *testing.T) {
	t.Run("session ends", func(t *testing.T) {
		e := newEvEnv(t, true)
		e.h.hub.heartbeat = 30 * time.Millisecond
		a := e.open("alice", cA, nil)
		a.ready()
		e.id.end("alice")
		a.ends()
	})
	t.Run("session ends then an event arrives", func(t *testing.T) {
		e := newEvEnv(t, true)
		a := e.open("bob", cB, nil)
		a.ready()
		e.mustSub("bob", cB, "kv:app-tasks:*")
		e.id.end("bob")
		e.h.hub.publishKV("tasks", "s-bob", "s-bob", "", "set", "k")
		a.ends()
	})
	t.Run("purge", func(t *testing.T) {
		e := newEvEnv(t, true)
		e.h.hub.heartbeat = 30 * time.Millisecond
		a := e.open("bob", cB, nil)
		a.ready()
		if n, err := e.store.PurgePerson(context.Background(), "tasks", "s-bob", "", time.Now()); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		a.ends()
		// A purged person cannot open a new stream.
		if s := e.open("bob", cB, nil); s.res.StatusCode != 401 {
			t.Fatalf("purged: %d", s.res.StatusCode)
		}
	})
	t.Run("max age", func(t *testing.T) {
		e := newEvEnv(t, true)
		e.h.hub.maxAge = 80 * time.Millisecond
		a := e.open("alice", cA, nil)
		a.ready()
		a.ends()
	})
	t.Run("idle without subscriptions", func(t *testing.T) {
		e := newEvEnv(t, true)
		e.h.hub.heartbeat = 20 * time.Millisecond
		e.h.hub.idle = 60 * time.Millisecond
		a := e.open("alice", cA, nil)
		a.ready()
		a.ends()
	})
	t.Run("grant revoked elsewhere", func(t *testing.T) {
		e := newEvEnv(t, true)
		e.h.hub.heartbeat = 30 * time.Millisecond
		_, out := e.post("alice", `{"operation":"kv.share","prefix":"p:","recipient":"bob","access":"read"}`)
		h := out["handle"].(string)
		b := e.open("bob", cB, nil)
		b.ready()
		e.mustSub("bob", cB, "kv:"+h+":*")
		// The owner's tool revokes in another process: only the database changes.
		if n, err := e.store.RevokeGrantByID(context.Background(), "tasks", h, time.Now()); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		for {
			f := b.next()
			if f.event == "gap" {
				break
			}
		}
		e.h.hub.publishKV("tasks", "s-alice", "s-alice", "", "set", "p:k")
		b.none(150 * time.Millisecond)
	})
	t.Run("stale session on a recipient stream", func(t *testing.T) {
		e := newEvEnv(t, true)
		e.h.hub.heartbeat = 30 * time.Millisecond
		_, out := e.post("alice", `{"operation":"kv.share","prefix":"p:","recipient":"bob","access":"read"}`)
		h := out["handle"].(string)
		b := e.open("bob", cB, nil)
		b.ready()
		e.mustSub("bob", cB, "kv:"+h+":*")
		e.id.setAge("bob", 25*time.Hour)
		b.ends()
	})
}

func TestEventsRefusals(t *testing.T) {
	e := newEvEnv(t, true)
	get := func(hdr map[string]string, cookie string) int {
		return e.open(cookie, cA, hdr).res.StatusCode
	}
	if c := get(map[string]string{"Origin": "https://evil.example"}, "alice"); c != 403 {
		t.Fatalf("evil origin: %d", c)
	}
	if c := get(map[string]string{"Sec-Fetch-Site": "cross-site"}, "alice"); c != 403 {
		t.Fatalf("cross-site: %d", c)
	}
	if c := get(map[string]string{"Sec-Fetch-Site": "same-site"}, "alice"); c != 403 {
		t.Fatalf("same-site: %d", c)
	}
	if c := get(map[string]string{"Origin": "https://home.example", "Sec-Fetch-Site": "same-origin"}, "alice"); c != 200 {
		t.Fatalf("ok: %d", c)
	}
	if c := get(nil, "nobody"); c != 401 {
		t.Fatalf("no session: %d", c)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+EventsPath+"?client="+cA, nil)
	req.AddCookie(&http.Cookie{Name: "s", Value: "alice"})
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 405 {
		t.Fatalf("post: %d", res.StatusCode)
	}
	if res, _ := http.Get(e.srv.URL + EventsPath + "?client=bad"); res.StatusCode != 401 {
		t.Fatalf("anonymous: %d", res.StatusCode)
	}
	// Subscribe and unsubscribe need the CSRF token like every write.
	for _, op := range []string{"events.subscribe", "events.unsubscribe"} {
		req, _ := http.NewRequest("POST", e.srv.URL+DataPath,
			bytes.NewBufferString(fmt.Sprintf(`{"operation":%q,"client":%q,"topic":"notif:app-user"}`, op, cA)))
		req.Header.Set("Origin", "https://home.example")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "s", Value: "alice"})
		res, _ := http.DefaultClient.Do(req)
		if res.StatusCode != 403 {
			t.Fatalf("%s without CSRF: %d", op, res.StatusCode)
		}
	}
	// A bad client id on a write is refused, not ignored.
	if st, _ := e.post("alice", `{"operation":"put","kind":"kv","key":"k","value":"MQ==","client":"x"}`); st != 400 {
		t.Fatalf("bad client: %d", st)
	}
}

func TestEventsSharingOffAndStaleSession(t *testing.T) {
	e := newEvEnv(t, false)
	if s := e.open("alice", cA, nil); s.res.StatusCode != 403 {
		t.Fatalf("stream with sharing off: %d", s.res.StatusCode)
	}
	st, out := e.sub("alice", cA, "notif:app-user")
	wantCode(t, st, out, 403, "events_disabled")

	e = newEvEnv(t, true)
	_, out = e.post("alice", `{"operation":"kv.share","prefix":"p:","recipient":"bob","access":"read"}`)
	h := out["handle"].(string)
	e.open("bob", cB, nil).ready()
	e.id.setAge("bob", 25*time.Hour)
	if st, _ := e.sub("bob", cB, "kv:"+h+":*"); st != 401 {
		t.Fatalf("stale: %d", st)
	}
}

func TestEventsRouteIsServed(t *testing.T) {
	s, _ := grantStore(t)
	s.SetSharing(true)
	id := &evIdentity{sess: map[string]*evSess{"alice": {scope: gAlice}}}
	hdl, err := NewServerAt("/apps/tasks", s, id, []string{"https://home.example"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(hdl)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest("GET", srv.URL+"/apps/tasks"+EventsPath+"?client="+cA, nil)
	req.AddCookie(&http.Cookie{Name: "s", Value: "alice"})
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%v %v", res, err)
	}
	res.Body.Close()
}

// The bridge's http.Server has read and write timeouts; a stream must outlive
// them (each write sets its own deadline).
func TestEventsOutliveServerTimeouts(t *testing.T) {
	e := newEvEnv(t, true)
	e.h.hub.heartbeat = 120 * time.Millisecond
	mux := http.NewServeMux()
	mux.Handle(DataPath, e.h)
	mux.HandleFunc(EventsPath, e.h.serveEvents)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ReadTimeout = 100 * time.Millisecond
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	e.srv = srv
	a := e.open("alice", cA, nil)
	a.ready()
	for i := 0; i < 3; i++ {
		if f := a.next(); f.event != "comment" {
			t.Fatalf("beat %d: %v", i, f)
		}
	}
}
