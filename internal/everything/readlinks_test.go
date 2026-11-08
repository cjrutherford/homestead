package everything

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const lnkOrigin = "https://home.example"

func mintOK(t *testing.T, s *Store, sc Scope, key string, ttl time.Duration) (string, ReadLink) {
	t.Helper()
	tok, l, err := s.CreateReadLink(context.Background(), sc, key, ttl, time.Now())
	if err != nil {
		t.Fatalf("mint %s: %v", key, err)
	}
	return tok, l
}

func lnkPut(t *testing.T, s *Store, sc Scope, key, v string) {
	t.Helper()
	if err := s.Put(context.Background(), sc, KindFile, key, []byte(v)); err != nil {
		t.Fatal(err)
	}
}

func opens(s *Store, tok string) bool {
	_, _, err := s.OpenReadLink(context.Background(), tok, time.Now())
	return err == nil
}

func TestReadLinkRoundTripHashedAtRestAndCurrentContent(t *testing.T) {
	s, p := grantStore(t)
	ctx := context.Background()
	lnkPut(t, s, gAlice, "a/feed.ics", "v1")
	tok, l := mintOK(t, s, gAlice, "a/feed.ics", 0)
	if len(tok) != 43 {
		t.Fatalf("token length %d", len(tok))
	}
	if want := time.Now().Add(DefaultReadLinkTTL).Unix(); l.ExpiresAt < want-5 || l.ExpiresAt > want+5 {
		t.Fatalf("default ttl: %d vs %d", l.ExpiresAt, want)
	}
	_, v, err := s.OpenReadLink(ctx, tok, time.Now())
	if err != nil || string(v) != "v1" {
		t.Fatalf("open %q %v", v, err)
	}
	lnkPut(t, s, gAlice, "a/feed.ics", "v2") // the link serves the file, not a copy
	if _, v, _ = s.OpenReadLink(ctx, tok, time.Now()); string(v) != "v2" {
		t.Fatalf("not current: %q", v)
	}
	// Only the hash is stored.
	db, err := sql.Open("sqlite", p)
	if err == nil {
		defer db.Close()
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM read_links WHERE token_hash = ? OR id = ?`, tok, tok).Scan(&n)
		if n != 0 {
			t.Fatal("token stored in the clear")
		}
	}
	ls, _ := s.ReadLinks(ctx, "tasks", false, time.Now())
	if len(ls) != 1 || ls[0].Hits != 2 || ls[0].OwnerName != "Alice" {
		t.Fatalf("list %+v", ls)
	}
}

func TestReadLinkTTLBounds(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	lnkPut(t, s, gAlice, "f.txt", "x")
	if _, _, err := s.CreateReadLink(ctx, gAlice, "f.txt", 30*time.Second, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("below minimum: %v", err)
	}
	_, l := mintOK(t, s, gAlice, "f.txt", 1000*24*time.Hour)
	if l.ExpiresAt-l.CreatedAt != int64(MaxReadLinkTTL/time.Second) {
		t.Fatalf("not capped at 365 days: %d", l.ExpiresAt-l.CreatedAt)
	}
	s.SetReadLinkMax(48 * time.Hour)
	_, l = mintOK(t, s, gAlice, "f.txt", 365*24*time.Hour)
	if l.ExpiresAt-l.CreatedAt != 48*3600 {
		t.Fatalf("owner-lowered cap ignored: %d", l.ExpiresAt-l.CreatedAt)
	}
	s.SetReadLinkMax(9999 * 24 * time.Hour) // never raised above 365 days
	_, l = mintOK(t, s, gAlice, "f.txt", 1000*24*time.Hour)
	if l.ExpiresAt-l.CreatedAt != int64(MaxReadLinkTTL/time.Second) {
		t.Fatal("cap raised")
	}
}

func TestReadLinkExpiryAndRevocation(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	lnkPut(t, s, gAlice, "f.txt", "x")
	tok, l := mintOK(t, s, gAlice, "f.txt", time.Hour)
	if _, _, err := s.OpenReadLink(ctx, tok, time.Now().Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired link opened: %v", err)
	}
	if n, err := s.RevokeReadLink(ctx, "notes", l.ID, time.Now()); err != nil || n != 0 {
		t.Fatalf("revoked from another app: %d %v", n, err)
	}
	if !opens(s, tok) {
		t.Fatal("other app's revoke ended the link")
	}
	if n, err := s.RevokeReadLink(ctx, "tasks", l.ID, time.Now()); err != nil || n != 1 {
		t.Fatalf("revoke %d %v", n, err)
	}
	if opens(s, tok) {
		t.Fatal("revoked link opens")
	}
	if n, _ := s.RevokeReadLink(ctx, "tasks", l.ID, time.Now()); n != 0 {
		t.Fatal("double revoke counted")
	}
}

func TestReadLinkOwnershipAndFileKinds(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	lnkPut(t, s, gAlice, "dir/f.txt", "x")
	// Bob cannot mint for Alice's file: he only reaches his own scope.
	if _, _, err := s.CreateReadLink(ctx, gBob, "dir/f.txt", 0, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-person mint: %v", err)
	}
	// Nor from another app of the same subject.
	other := Scope{App: "notes", Subject: gAlice.Subject}
	if _, _, err := s.CreateReadLink(ctx, other, "dir/f.txt", 0, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-app mint: %v", err)
	}
	if _, _, err := s.CreateReadLink(ctx, gAlice, "dir", 0, time.Now()); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInvalid) {
		t.Fatalf("folder mint: %v", err)
	}
	if _, _, err := s.CreateReadLink(ctx, gAlice, "~shared/x/y", 0, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("shared path mint: %v", err)
	}
	if _, _, err := s.CreateReadLink(ctx, gAlice, "../etc", 0, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad key mint: %v", err)
	}
}

func TestReadLinkDiesWithTheFile(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	for _, k := range []string{"d.txt", "r.txt", "t/a.txt", "t/sub/b.txt", "ts/keep.txt"} {
		lnkPut(t, s, gAlice, k, "x")
	}
	d, _ := mintOK(t, s, gAlice, "d.txt", 0)
	r, _ := mintOK(t, s, gAlice, "r.txt", 0)
	ta, _ := mintOK(t, s, gAlice, "t/a.txt", 0)
	tb, _ := mintOK(t, s, gAlice, "t/sub/b.txt", 0)
	keep, _ := mintOK(t, s, gAlice, "ts/keep.txt", 0)
	if err := s.Delete(ctx, gAlice, KindFile, "d.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, gAlice, "r.txt", "r2.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteTree(ctx, gAlice, "t"); err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"delete": d, "rename": r, "tree a": ta, "tree b": tb} {
		if opens(s, tok) {
			t.Errorf("%s: link still opens", name)
		}
	}
	if !opens(s, keep) {
		t.Fatal("tree delete of t revoked the sibling prefix ts")
	}
	// Re-creating the paths never revives anything.
	lnkPut(t, s, gAlice, "d.txt", "again")
	lnkPut(t, s, gAlice, "r.txt", "again")
	if opens(s, d) || opens(s, r) {
		t.Fatal("re-created file revived a link")
	}
	// An overwrite keeps the link (Tasks depends on current content).
	lnkPut(t, s, gAlice, "ts/keep.txt", "new")
	if !opens(s, keep) {
		t.Fatal("overwrite revoked the link")
	}
	// Another person's delete of the same name does not touch Alice's link.
	lnkPut(t, s, gBob, "ts/keep.txt", "bob")
	if err := s.Delete(ctx, gBob, KindFile, "ts/keep.txt"); err != nil {
		t.Fatal(err)
	}
	if !opens(s, keep) {
		t.Fatal("bob's delete revoked alice's link")
	}
}

func TestReadLinkRecipientDeleteThroughFolderShareRevokes(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	lnkPut(t, s, gAlice, "pub/a.txt", "x")
	tok, _ := mintOK(t, s, gAlice, "pub/a.txt", 0)
	g := mustGrant(t, s, gAlice, GrantRequest{Username: "Bob", Kind: GrantFS, Key: "pub", IsPrefix: true, Access: AccessWrite})
	if err := s.GrantDelete(ctx, gBob, g.ID, GrantFS, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if opens(s, tok) {
		t.Fatal("recipient delete left the owner's link live")
	}
}

func TestReadLinkPurgeRevokesAndOwnerPurgedFails(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	lnkPut(t, s, gAlice, "a.txt", "x")
	lnkPut(t, s, gBob, "b.txt", "x")
	ta, _ := mintOK(t, s, gAlice, "a.txt", 0)
	tb, _ := mintOK(t, s, gBob, "b.txt", 0)
	if n, err := s.PurgePerson(ctx, "tasks", "s-alice", "", time.Now()); err != nil || n != 1 {
		t.Fatalf("purge %d %v", n, err)
	}
	if opens(s, ta) {
		t.Fatal("purged person's link opens")
	}
	if !opens(s, tb) {
		t.Fatal("purge of alice ended bob's link")
	}
	// Purge in another app leaves tasks links alone.
	if _, err := s.PurgePerson(ctx, "notes", "s-bob", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !opens(s, tb) {
		t.Fatal("purge in notes ended a tasks link")
	}
	if _, _, err := s.CreateReadLink(ctx, gAlice, "a.txt", 0, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged person minted: %v", err)
	}
}

func TestReadLinkLimits(t *testing.T) {
	s, _ := grantStore(t)
	ctx := context.Background()
	lnkPut(t, s, gAlice, "p.txt", "x")
	for i := 0; i < MaxLinksPerPath; i++ {
		mintOK(t, s, gAlice, "p.txt", 0)
	}
	if _, _, err := s.CreateReadLink(ctx, gAlice, "p.txt", 0, time.Now()); !errors.Is(err, ErrLinkLimit) {
		t.Fatalf("11th link on a path: %v", err)
	}
	if _, _, err := s.CreateReadLink(ctx, gBob, "p.txt", 0, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatal("bob counted against alice")
	}
	// Expired links do not count.
	if _, _, err := s.CreateReadLink(ctx, gAlice, "p.txt", 0, time.Now().Add(8*24*time.Hour)); err != nil {
		t.Fatalf("expired links still count: %v", err)
	}
	live, _ := s.ReadLinks(ctx, "tasks", false, time.Now())
	for n, i := len(live), 0; n < MaxLinksPerOwner; i++ {
		k := fmt.Sprintf("q%d.txt", i)
		lnkPut(t, s, gAlice, k, "x")
		for j := 0; j < MaxLinksPerPath && n < MaxLinksPerOwner; j++ {
			mintOK(t, s, gAlice, k, 0)
			n++
		}
	}
	lnkPut(t, s, gAlice, "last.txt", "x")
	if _, _, err := s.CreateReadLink(ctx, gAlice, "last.txt", 0, time.Now()); !errors.Is(err, ErrLinkLimit) {
		t.Fatalf("owner limit: %v", err)
	}
	lnkPut(t, s, gBob, "last.txt", "x")
	if _, _, err := s.CreateReadLink(ctx, gBob, "last.txt", 0, time.Now()); err != nil {
		t.Fatalf("limit leaked across people: %v", err)
	}
}

// --- HTTP ---

type linkEnv struct {
	t     *testing.T
	store *Store
	h     *handler
	now   time.Time
	logs  *bytes.Buffer
	id    *agedIdentity
}

func newLinkEnv(t *testing.T) *linkEnv {
	s, _ := grantStore(t)
	e := &linkEnv{t: t, store: s, now: time.Now(), logs: &bytes.Buffer{}}
	e.id = &agedIdentity{scope: gAlice}
	hh, err := NewHandler(s, e.id, []string{lnkOrigin})
	if err != nil {
		t.Fatal(err)
	}
	e.h = hh.(*handler)
	e.h.base = "/apps/tasks"
	e.h.now = func() time.Time { return e.now }
	var mu sync.Mutex
	e.h.logger = func(f string, a ...any) { mu.Lock(); fmt.Fprintf(e.logs, f+"\n", a...); mu.Unlock() }
	return e
}

func (e *linkEnv) mint(body string) (int, map[string]any) {
	c := &caller{e: &shareEnv{t: e.t, store: e.store, now: e.now}, id: e.id, h: e.h}
	return c.do(body)
}

func (e *linkEnv) get(method, token, remote string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://home.example/apps/tasks"+ReadPrefix+token, nil)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.serveRead(rec, req)
	return rec
}

func TestGetReadURLOperationAndRoute(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "cal/feed.ics", "BEGIN:VCALENDAR")
	st, out := e.mint(`{"operation":"fs.getReadURL","key":"cal/feed.ics","ttl":3600}`)
	if st != 200 {
		t.Fatalf("mint %d %v", st, out)
	}
	url, _ := out["url"].(string)
	prefix := lnkOrigin + "/apps/tasks" + ReadPrefix
	if !strings.HasPrefix(url, prefix) || len(url) != len(prefix)+43 {
		t.Fatalf("url %q", url)
	}
	if strings.Contains(url, "feed") || strings.Contains(url, "alice") {
		t.Fatal("url names the file or owner")
	}
	tok := strings.TrimPrefix(url, prefix)

	// Anonymous: cookies and Authorization are ignored, nothing is set.
	rec := e.get("GET", tok, "10.0.0.2:1234", map[string]string{"Cookie": "hs_session=zzz", "Authorization": "Bearer nope"})
	if rec.Code != 200 || rec.Body.String() != "BEGIN:VCALENDAR" {
		t.Fatalf("get %d %q", rec.Code, rec.Body.String())
	}
	want := map[string]string{
		"Content-Type":                 "text/calendar; charset=utf-8",
		"X-Content-Type-Options":       "nosniff",
		"Content-Security-Policy":      "sandbox; default-src 'none'",
		"Cache-Control":                "private, no-store",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Content-Disposition":          "inline",
	}
	for k, v := range want {
		if rec.Header().Get(k) != v {
			t.Errorf("%s = %q, want %q", k, rec.Header().Get(k), v)
		}
	}
	for _, k := range []string{"Set-Cookie", "Access-Control-Allow-Origin", "Accept-Ranges"} {
		if rec.Header().Get(k) != "" {
			t.Errorf("%s must not be set", k)
		}
	}
	// HEAD: headers, no body.
	if h := e.get("HEAD", tok, "10.0.0.2:1", nil); h.Code != 200 || h.Body.Len() != 0 || h.Header().Get("Content-Length") != "15" {
		t.Fatalf("head %d %d %q", h.Code, h.Body.Len(), h.Header().Get("Content-Length"))
	}
	if r := e.get("POST", tok, "10.0.0.2:1", nil); r.Code != 405 {
		t.Fatalf("post %d", r.Code)
	}
	// The log has the id, status and size, never the token or the URL.
	logs := e.logs.String()
	if !strings.Contains(logs, "status=200 bytes=15") || strings.Contains(logs, tok) || strings.Contains(logs, "_homestead") {
		t.Fatalf("log %q", logs)
	}
}

func TestReadRouteContentTypesAndDisposition(t *testing.T) {
	e := newLinkEnv(t)
	cases := map[string][3]string{
		"a.json":   {"application/json", "inline", ""},
		"a.txt":    {"text/plain; charset=utf-8", "inline", ""},
		"a.html":   {"application/octet-stream", "attachment", `"a.html"`},
		"a.svg":    {"application/octet-stream", "attachment", ""},
		"a.png":    {"image/png", "attachment", ""},
		"a.md":     {"text/markdown; charset=utf-8", "attachment", ""},
		"noext":    {"application/octet-stream", "attachment", ""},
		"we ird\"": {"application/octet-stream", "attachment", `"we_ird_"`},
	}
	for name, want := range cases {
		lnkPut(t, e.store, gAlice, name, "x")
		tok, _ := mintOK(t, e.store, gAlice, name, 0)
		rec := e.get("GET", tok, "10.0.0.2:1", nil)
		cd := rec.Header().Get("Content-Disposition")
		if rec.Code != 200 || rec.Header().Get("Content-Type") != want[0] || !strings.HasPrefix(cd, want[1]) || !strings.Contains(cd, want[2]) {
			t.Errorf("%s: %d %q %q", name, rec.Code, rec.Header().Get("Content-Type"), cd)
		}
	}
}

func TestReadRouteUniform404(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "f.txt", "x")
	live, _ := mintOK(t, e.store, gAlice, "f.txt", 0)
	revoked, l := mintOK(t, e.store, gAlice, "f.txt", 0)
	_, _ = e.store.RevokeReadLink(context.Background(), "tasks", l.ID, time.Now())
	short, _ := mintOK(t, e.store, gAlice, "f.txt", time.Minute)
	gone := func() string { // file deleted
		lnkPut(t, e.store, gAlice, "g.txt", "x")
		tok, _ := mintOK(t, e.store, gAlice, "g.txt", 0)
		_ = e.store.Delete(context.Background(), gAlice, KindFile, "g.txt")
		return tok
	}()
	_ = live
	var first string
	e.now = time.Now().Add(time.Hour) // the short one is now expired
	for name, tok := range map[string]string{
		"unknown": strings.Repeat("A", 43), "revoked": revoked, "expired": short, "deleted": gone,
		"malformed": "../../etc/passwd", "empty": "", "short": "abc", "long": strings.Repeat("A", 200), "bad chars": strings.Repeat("A", 42) + "!",
	} {
		rec := e.get("GET", tok, fmt.Sprintf("10.0.0.%d:1", len(name)), nil)
		got := fmt.Sprintf("%d|%q|%v", rec.Code, rec.Body.String(), rec.Header())
		if rec.Code != 404 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if first == "" {
			first = got
		} else if got != first {
			t.Errorf("%s differs from the others:\n%s\n%s", name, got, first)
		}
	}
	if e.logs.Len() != 0 {
		t.Fatalf("a miss was logged: %q", e.logs.String())
	}
}

func TestReadRouteRateLimits(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "f.txt", "x")
	tok, _ := mintOK(t, e.store, gAlice, "f.txt", 0)
	// 60 per client per minute; the 61st is refused.
	for i := 0; i < ReadPerClientPerMinute; i++ {
		if r := e.get("GET", tok, "10.1.1.1:5", nil); r.Code != 200 {
			t.Fatalf("request %d: %d", i, r.Code)
		}
	}
	if r := e.get("GET", tok, "10.1.1.1:6", nil); r.Code != 429 || r.Header().Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d", r.Code)
	}
	// Another client is unaffected; the window slides.
	if r := e.get("GET", tok, "10.1.1.2:6", nil); r.Code != 200 {
		t.Fatalf("other client: %d", r.Code)
	}
	e.now = e.now.Add(61 * time.Second)
	if r := e.get("GET", tok, "10.1.1.1:7", nil); r.Code != 200 {
		t.Fatalf("after the window: %d", r.Code)
	}
	// Unknown tokens cost double: 30 misses fill a client.
	miss := strings.Repeat("B", 43)
	n := 0
	for ; n < 100; n++ {
		if e.get("GET", miss, "10.2.2.2:1", nil).Code == 429 {
			break
		}
	}
	if n != ReadPerClientPerMinute/2 {
		t.Fatalf("misses allowed before the limit: %d", n)
	}
	// The whole route is limited too, across clients.
	e2 := newLinkEnv(t)
	lnkPut(t, e2.store, gAlice, "f.txt", "x")
	tok2, _ := mintOK(t, e2.store, gAlice, "f.txt", 0)
	ok := 0
	for i := 0; i < 700; i++ {
		if e2.get("GET", tok2, fmt.Sprintf("10.3.%d.%d:1", i/200, i%200), nil).Code == 200 {
			ok++
		}
	}
	if ok != ReadPerAppPerMinute {
		t.Fatalf("app limit: %d served", ok)
	}
}

func TestReadRouteRateLimitUsesATrustworthyAddress(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "f.txt", "x")
	tok, _ := mintOK(t, e.store, gAlice, "f.txt", 0)
	// The edge (a trusted peer) names the real client; a forged left-hand entry is ignored.
	spoof := map[string]string{"X-Forwarded-For": "1.1.1.1, 203.0.113.9"}
	for i := 0; i < ReadPerClientPerMinute; i++ {
		e.get("GET", tok, "172.18.0.5:9", spoof)
	}
	if r := e.get("GET", tok, "172.18.0.5:9", spoof); r.Code != 429 {
		t.Fatalf("edge client not limited: %d", r.Code)
	}
	// A different real client behind the same edge is still served.
	if r := e.get("GET", tok, "172.18.0.5:9", map[string]string{"X-Forwarded-For": "203.0.113.10"}); r.Code != 200 {
		t.Fatalf("second client behind the edge: %d", r.Code)
	}
	// A direct, untrusted peer cannot choose its own bucket with the header.
	for i := 0; i < ReadPerClientPerMinute; i++ {
		e.get("GET", tok, "198.51.100.7:9", map[string]string{"X-Forwarded-For": fmt.Sprintf("9.9.9.%d", i%250)})
	}
	if r := e.get("GET", tok, "198.51.100.7:9", map[string]string{"X-Forwarded-For": "8.8.8.8"}); r.Code != 429 {
		t.Fatalf("untrusted peer escaped its limit with X-Forwarded-For: %d", r.Code)
	}
}

func TestClientAddr(t *testing.T) {
	s, _ := grantStore(t)
	_ = s.SetTrustedProxies(nil)
	tr := s.trustedNets()
	req := func(remote string, xff ...string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		for _, v := range xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		return r
	}
	for _, c := range []struct {
		name string
		r    *http.Request
		want string
	}{
		{"edge sets one value", req("172.18.0.2:1", "203.0.113.5"), "203.0.113.5"},
		{"rightmost wins", req("172.18.0.2:1", "6.6.6.6, 203.0.113.5"), "203.0.113.5"},
		{"split headers", req("172.18.0.2:1", "6.6.6.6", "203.0.113.5"), "203.0.113.5"},
		{"junk rightmost uses the edge", req("172.18.0.2:1", "6.6.6.6, junk"), "172.18.0.2"},
		{"empty header from the tunnel", req("172.18.0.2:1", ""), "172.18.0.2"},
		{"untrusted peer", req("203.0.113.77:1", "1.2.3.4"), "203.0.113.77"},
		{"loopback peer", req("127.0.0.1:1", "203.0.113.5"), "203.0.113.5"},
		{"ipv6 /64", req("[2001:db8:1:2:aaaa::1]:1"), "2001:db8:1:2::/64"},
		{"v4 in v6", req("[::ffff:203.0.113.8]:1"), "203.0.113.8"},
		{"bad remote", req("nonsense"), "unknown"},
	} {
		if got := clientAddr(c.r, tr); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	_ = s.SetTrustedProxies([]string{"none"})
	if got := clientAddr(req("172.18.0.2:1", "203.0.113.5"), s.trustedNets()); got != "172.18.0.2" {
		t.Errorf("none still trusted: %s", got)
	}
	_ = s.SetTrustedProxies([]string{"192.0.2.0/24"})
	if got := clientAddr(req("192.0.2.9:1", "203.0.113.5"), s.trustedNets()); got != "203.0.113.5" {
		t.Errorf("custom range ignored: %s", got)
	}
	if s.SetTrustedProxies([]string{"nope"}) == nil {
		t.Error("bad CIDR accepted")
	}
	_ = net.IPv4len
}

func TestGetReadURLErrorsAndCSRF(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "f.txt", "x")
	lnkPut(t, e.store, gAlice, "d/f.txt", "x")
	for body, code := range map[string]string{
		`{"operation":"fs.getReadURL","key":"missing.txt"}`:     "path_not_found",
		`{"operation":"fs.getReadURL","key":"d"}`:               "invalid_argument",
		`{"operation":"fs.getReadURL","key":"f.txt","ttl":30}`:  "invalid_argument",
		`{"operation":"fs.getReadURL","key":"f.txt","ttl":-5}`:  "invalid_argument",
		`{"operation":"fs.getReadURL","key":"~shared/x/f.txt"}`: "invalid_argument",
		`{"operation":"fs.getReadURL","key":"../f.txt"}`:        "invalid_argument",
		`{"operation":"fs.getReadURL","key":""}`:                "invalid_argument",
	} {
		if st, out := e.mint(body); out["error"] != code {
			t.Errorf("%s: %d %v, want %s", body, st, out, code)
		}
	}
	// Bob asking for Alice's file name gets the same answer as for a missing one.
	e.id.scope = gBob
	if st, out := e.mint(`{"operation":"fs.getReadURL","key":"f.txt"}`); st != 404 || out["error"] != "path_not_found" {
		t.Fatalf("cross-person: %d %v", st, out)
	}
	// CSRF and origin checks apply like every write.
	e.id.scope = gAlice
	req := httptest.NewRequest("POST", "https://home.example/x", bytes.NewBufferString(`{"operation":"fs.getReadURL","key":"f.txt"}`))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 403 || strings.Contains(rec.Body.String(), "url") {
		t.Fatalf("foreign origin: %d", rec.Code)
	}
	e.h.identity = noCSRF{e.id}
	req = httptest.NewRequest("POST", "https://home.example/x", bytes.NewBufferString(`{"operation":"fs.getReadURL","key":"f.txt"}`))
	req.Header.Set("Origin", lnkOrigin)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("no csrf: %d", rec.Code)
	}
	if ls, _ := e.store.ReadLinks(context.Background(), "tasks", true, time.Now()); len(ls) != 0 {
		t.Fatalf("refused requests minted %d links", len(ls))
	}
	// 10 per path, then read_link_limit.
	e.h.identity = e.id
	for i := 0; i < MaxLinksPerPath; i++ {
		if st, _ := e.mint(`{"operation":"fs.getReadURL","key":"f.txt"}`); st != 200 {
			t.Fatalf("mint %d: %d", i, st)
		}
	}
	if st, out := e.mint(`{"operation":"fs.getReadURL","key":"f.txt"}`); st != 429 || out["error"] != "read_link_limit" {
		t.Fatalf("limit: %d %v", st, out)
	}
}

type noCSRF struct{ *agedIdentity }

func (noCSRF) CheckCSRF(*http.Request) bool { return false }

func TestReadRouteIsRegisteredUnderTheBasePath(t *testing.T) {
	s, _ := grantStore(t)
	id := &agedIdentity{scope: gAlice}
	srv, err := NewServerAt("/apps/tasks", s, id, []string{lnkOrigin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lnkPut(t, s, gAlice, "f.json", `{"a":1}`)
	tok, _ := mintOK(t, s, gAlice, "f.json", 0)
	for path, want := range map[string]int{"/apps/tasks" + ReadPrefix + tok: 200, ReadPrefix + tok: 404} {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "10.0.0.9:1"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}

// S465: once read-link exposure is approved, links are minted on the approved
// public origin even when the caller is on a local name; without approval
// they stay on the caller's own origin.
func TestGetReadURLUsesTheApprovedPublicOrigin(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "f.txt", "x")
	link := func() string {
		st, out := e.mint(`{"operation":"fs.getReadURL","key":"f.txt"}`)
		if st != 200 {
			t.Fatalf("mint %d %v", st, out)
		}
		return out["url"].(string)
	}
	if u := link(); !strings.HasPrefix(u, lnkOrigin+"/apps/tasks"+ReadPrefix) {
		t.Fatalf("unapproved link %q", u)
	}
	if err := e.store.SetPublicReadOrigin("https://homestead.example.org"); err != nil {
		t.Fatal(err)
	}
	if u := link(); !strings.HasPrefix(u, "https://homestead.example.org/apps/tasks"+ReadPrefix) {
		t.Fatalf("approved link %q", u)
	}
	if err := e.store.SetPublicReadOrigin(""); err != nil {
		t.Fatal(err)
	}
	if u := link(); !strings.HasPrefix(u, lnkOrigin) {
		t.Fatalf("link after approval removed %q", u)
	}
}
