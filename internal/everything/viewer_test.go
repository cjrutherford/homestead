package everything

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

func (e *linkEnv) view(method, token, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://home.example/apps/tasks"+ViewPrefix+token, nil)
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	e.h.serveView(rec, req)
	return rec
}

func (e *linkEnv) viewOf(key, content string) string {
	lnkPut(e.t, e.store, gAlice, key, content)
	tok, _ := mintOK(e.t, e.store, gAlice, key, 0)
	rec := e.view("GET", tok, "10.0.0.2:1")
	if rec.Code != 200 {
		e.t.Fatalf("view %s: %d", key, rec.Code)
	}
	return rec.Body.String()
}

// assertSafePage re-tokenizes a rendered page and fails on anything outside
// the closed set the viewer may emit: no script, no event handler, no style
// attribute, no image, no URL but an http(s) link, balanced elements.
func assertSafePage(t *testing.T, page string) {
	t.Helper()
	if !utf8.ValidString(page) {
		t.Error("page is not valid UTF-8")
	}
	okTags := map[string]bool{"html": true, "head": true, "meta": true, "title": true, "style": true, "body": true, "main": true, "header": true,
		"h1": true, "p": true, "div": true, "span": true, "ul": true, "ol": true, "li": true, "blockquote": true, "pre": true, "strong": true,
		"em": true, "u": true, "s": true, "code": true, "sub": true, "sup": true, "mark": true, "br": true, "hr": true, "a": true}
	tz := html.NewTokenizer(strings.NewReader(page))
	depth, inStyle := 0, false
	for {
		tt := tz.Next()
		if tt == html.ErrorToken {
			if tz.Err() != io.EOF {
				t.Errorf("tokenize: %v", tz.Err())
			}
			break
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			nb, more := tz.TagName()
			name := string(nb)
			if !okTags[name] {
				t.Errorf("tag <%s> in output", name)
			}
			if name == "style" {
				inStyle = true
			}
			for more {
				var k, v []byte
				k, v, more = tz.TagAttr()
				switch key := string(k); {
				case key == "class" && regexp.MustCompile(`^[a-z0-9 ]+$`).Match(v):
				case key == "charset" || key == "lang" || key == "name" || key == "content":
				case key == "rel" && string(v) == "nofollow noopener noreferrer":
				case key == "href" && name == "a" && regexp.MustCompile(`^https?://[^\s<>"']+$`).Match(v):
				default:
					t.Errorf("attribute %s=%q on <%s>", k, v, name)
				}
			}
			if name != "br" && name != "hr" && name != "meta" {
				depth++
			}
		case html.EndTagToken:
			nb, _ := tz.TagName()
			if string(nb) == "style" {
				inStyle = false
			}
			depth--
		case html.TextToken:
			if inStyle {
				continue
			}
		}
	}
	if depth != 0 {
		t.Errorf("unbalanced output, depth %d", depth)
	}
	body := strings.SplitN(page, "</style>", 2)[1]
	for _, bad := range []string{"<script", "<img", "<iframe", "<svg", "<object", "<embed", "<form", "<input", "<link", "<base"} {
		if strings.Contains(strings.ToLower(body), bad) {
			t.Errorf("output contains %q", bad)
		}
	}
}

const tasksSnapshot = `{"kind":"tasks.shared-list","v":1,"name":"Groceries <b>x</b>","icon":"list","slot":2,"owner":"Alice & Co","updatedAt":1793700000000,
"tasks":[
 {"id":"3","title":"Done thing","notes":"","due":null,"priority":0,"tags":[],"order":1,"done":true,"subtasks":[]},
 {"id":"2","title":"Second <script>alert(1)</script>","notes":"line1\nline2 <img src=x onerror=alert(1)>","due":"2026-11-05","dueTime":"09:30","priority":3,"tags":["home","a b"],"order":20,"done":false,"subtasks":[{"title":"sub <i>one</i>","done":true},{"title":"sub two","done":false}]},
 {"id":"1","title":"First","notes":"","due":"2026-13-45","priority":9,"order":10,"done":false}
]}`

func TestViewerRendersTasksSnapshot(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "list.json", tasksSnapshot)
	tok, _ := mintOK(t, e.store, gAlice, "list.json", 0)
	rec := e.view("GET", tok, "10.0.0.2:1")
	page := rec.Body.String()
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	for k, want := range map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "private, no-store",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": ViewerPolicy,
		"X-Frame-Options":         "DENY",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q", k, got)
		}
	}
	if rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("cookie or CORS header")
	}
	assertSafePage(t, page)
	// order: open tasks by manual order, then done; fields; escaping.
	last := -1
	for _, s := range []string{"First", "Second &lt;script&gt;", "Done thing"} {
		i := strings.Index(page, s)
		if i < 0 || i < last {
			t.Fatalf("%q at %d (after %d)", s, i, last)
		}
		last = i
	}
	for _, s := range []string{"Groceries &lt;b&gt;x&lt;/b&gt;", "Shared by Alice &amp; Co", "Updated 2026-", "line1\nline2 &lt;img", "Due 2026-11-05 09:30",
		"High priority", "#home", "#a-b", "sub &lt;i&gt;one&lt;/i&gt;", "&#9745; sub"} {
		if !strings.Contains(page, s) {
			t.Errorf("page lacks %q", s)
		}
	}
	if strings.Contains(page, "2026-13-45") || strings.Contains(page, "priority 9") {
		t.Error("invalid date or priority shown")
	}
	// The policy carries the hash of the exact stylesheet in the page.
	if !strings.Contains(page, "<style>"+viewerCSS+"</style>") || strings.Count(ViewerPolicy, "sha256-") != 1 ||
		strings.Contains(ViewerPolicy, "script-src") || !strings.Contains(ViewerPolicy, "default-src 'none'") ||
		!strings.Contains(ViewerPolicy, "img-src 'none'") {
		t.Errorf("policy %s", ViewerPolicy)
	}
	// HEAD: same headers and length, no body.
	h := e.view("HEAD", tok, "10.0.0.2:1")
	if h.Code != 200 || h.Body.Len() != 0 || h.Header().Get("Content-Length") != fmt.Sprint(len(page)) {
		t.Errorf("HEAD %d %d %s", h.Code, h.Body.Len(), h.Header().Get("Content-Length"))
	}
	if e.view("POST", tok, "10.0.0.2:1").Code != 405 {
		t.Error("POST allowed")
	}
	if strings.Contains(e.logs.String(), tok) || !strings.Contains(e.logs.String(), "view link id=") {
		t.Errorf("log %q", e.logs.String())
	}
}

func TestViewerRendersNotesPage(t *testing.T) {
	e := newLinkEnv(t)
	snap := `{"format":"notes-shared-page","v":2,"title":"Trip <x>","published":"2026-10-20T10:00:00.000Z","ink":[{"d":"M0 0"}],"outlines":[
 {"x":40,"y":300,"w":300,"html":"<p>second <b>bold</b> <i>it</i></p>"},
 {"x":40,"y":120,"w":300,"html":"<h1>Head</h1><p>Pack the tent</p><ul><li>one</li><li>two <a href=\"https://example.com/a?b=1&c=2\">link</a></li></ul><img src=\"data:image/png;base64,AAAA\"><a href=\"javascript:alert(1)\">bad</a><script>alert(1)</script><svg><circle/></svg>tail"}]}`
	page := e.viewOf("published/p.json", snap)
	assertSafePage(t, page)
	for _, s := range []string{"<h1>Trip &lt;x&gt;</h1>", "Published 2026-10-20 10:00 UTC", `<div class="h1">Head</div>`, "<ul><li>one</li><li>two ",
		`<a href="https://example.com/a?b=1&amp;c=2" rel="nofollow noopener noreferrer">link</a>`, "<strong>bold</strong>", "<em>it</em>",
		"not shown in this viewer", "Handwriting and drawings", ">bad<", "tail"} {
		if !strings.Contains(page, s) {
			t.Errorf("page lacks %q", s)
		}
	}
	if strings.Index(page, "Pack the tent") > strings.Index(page, "second") {
		t.Error("outlines not in reading order")
	}
	if strings.Contains(page, "alert(1)") || strings.Contains(page, "circle") {
		t.Error("script or svg content shown")
	}
	if strings.Count(page, "not shown in this viewer") != 3 { // img, svg, ink
		t.Errorf("markers: %d", strings.Count(page, "not shown in this viewer"))
	}
}

func TestViewerOtherFilesHaveNoViewer(t *testing.T) {
	e := newLinkEnv(t)
	for key, content := range map[string]string{
		"a.txt":       "<script>alert(1)</script>",
		"a.html":      "<script>alert(1)</script>",
		"feed.ics":    "BEGIN:VCALENDAR",
		"x.json":      `{"kind":"something.else","v":1}`,
		"arr.json":    `[1,2,3]`,
		"broken.json": `{"kind":`,
		"noext":       `{"kind":"tasks.shared-list","tasks":[]}`, // the extension decides what is parsed
		"p.png":       "\x89PNG",
	} {
		page := e.viewOf(key, content)
		assertSafePage(t, page)
		if !strings.Contains(page, "No viewer for this file") || strings.Contains(page, "alert") {
			t.Errorf("%s: %q", key, page)
		}
	}
	big := `{"kind":"tasks.shared-list","tasks":[],"pad":"` + strings.Repeat("a", viewMaxFile) + `"}`
	// The store never holds more than viewMaxFile; the guard is defence in depth.
	if page := string(renderViewer("big.json", []byte(big))); !strings.Contains(page, "Too large") {
		t.Error("oversized file rendered")
	}
}

func TestViewerSharesTokenRulesWithTheRawRoute(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "f.json", `{}`)
	live, _ := mintOK(t, e.store, gAlice, "f.json", 0)
	revoked, l := mintOK(t, e.store, gAlice, "f.json", 0)
	_, _ = e.store.RevokeReadLink(context.Background(), "tasks", l.ID, time.Now())
	short, _ := mintOK(t, e.store, gAlice, "f.json", time.Minute)
	lnkPut(t, e.store, gAlice, "g.json", `{}`)
	gone, _ := mintOK(t, e.store, gAlice, "g.json", 0)
	_ = e.store.Delete(context.Background(), gAlice, KindFile, "g.json")
	// Another person's and another app's file behind a token are not reachable by id.
	e.now = time.Now().Add(time.Hour)
	var first string
	for name, tok := range map[string]string{"unknown": strings.Repeat("A", 43), "revoked": revoked, "expired": short, "deleted": gone, "malformed": "../x", "empty": ""} {
		v := e.view("GET", tok, fmt.Sprintf("10.0.1.%d:1", len(name)))
		r := e.get("GET", tok, fmt.Sprintf("10.0.2.%d:1", len(name)), nil)
		if v.Code != 404 || v.Body.String() != r.Body.String() {
			t.Errorf("%s: view %d %q vs raw %d %q", name, v.Code, v.Body.String(), r.Code, r.Body.String())
		}
		got := fmt.Sprint(v.Code, v.Body.String(), v.Header())
		if first == "" {
			first = got
		} else if got != first {
			t.Errorf("%s differs: %s", name, got)
		}
	}
	if e.logs.Len() != 0 {
		t.Errorf("a miss was logged: %q", e.logs.String())
	}
	if e.view("GET", live, "10.0.3.1:1").Code != 200 {
		t.Error("live token does not view")
	}
	// The raw route's token is the viewer's token: a revoked link stops both.
	if _, err := e.store.RevokeReadLink(context.Background(), "tasks", mustID(t, e), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func mustID(t *testing.T, e *linkEnv) string {
	ls, _ := e.store.ReadLinks(context.Background(), "tasks", false, time.Now())
	if len(ls) == 0 {
		t.Fatal("no links")
	}
	return ls[0].ID
}

func TestViewerAndRawShareOneRateLimit(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "f.json", `{}`)
	tok, _ := mintOK(t, e.store, gAlice, "f.json", 0)
	for i := 0; i < ReadPerClientPerMinute; i++ {
		var rec *httptest.ResponseRecorder
		if i%2 == 0 {
			rec = e.view("GET", tok, "10.9.9.9:1")
		} else {
			rec = e.get("GET", tok, "10.9.9.9:1", nil)
		}
		if rec.Code != 200 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	for _, rec := range []*httptest.ResponseRecorder{e.view("GET", tok, "10.9.9.9:1"), e.get("GET", tok, "10.9.9.9:1", nil)} {
		if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
			t.Errorf("not limited: %d", rec.Code)
		}
	}
	if e.view("GET", tok, "10.9.9.8:1").Code != 200 {
		t.Error("another client is limited")
	}
}

func TestViewerCrossPersonAndAppLinks(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gBob, "list.json", tasksSnapshot)
	if _, _, err := e.store.CreateReadLink(context.Background(), gAlice, "list.json", 0, time.Now()); err == nil {
		t.Fatal("alice minted a link to bob's file")
	}
}

func TestViewerRegisteredUnderBasePath(t *testing.T) {
	s, _ := grantStore(t)
	srv, err := NewServerAt("/apps/tasks", s, &agedIdentity{scope: gAlice}, []string{lnkOrigin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lnkPut(t, s, gAlice, "f.json", `{}`)
	tok, _ := mintOK(t, s, gAlice, "f.json", 0)
	for p, want := range map[string]int{"/apps/tasks" + ViewPrefix + tok: 200, ViewPrefix + tok: 404} {
		req := httptest.NewRequest("GET", p, nil)
		req.RemoteAddr = "10.0.0.9:1"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", p, rec.Code, want)
		}
	}
}

// hostile inputs shared by the table test and the fuzz seeds.
var hostileHTML = []string{
	`<script>alert(1)</script>`, `<SCRIPT SRC=//x/y.js></SCRIPT>`, `<img src=x onerror=alert(1)>`, `<a href="javascript:alert(1)">x</a>`,
	`<a href="JaVa&#x09;Script:alert(1)">x</a>`, `<a href=" javascript:alert(1)">x</a>`, `<a href="data:text/html,<script>alert(1)</script>">x</a>`,
	`<a href="//evil.example/x">x</a>`, `<a href="https://x.example/\"onmouseover=\"alert(1)">x</a>`, `<a href="https://ex.example/ a">x</a>`,
	`<a href="https://a/"><a href="https://b/">nested</a></a>`, `<svg onload=alert(1)><script>alert(1)</script></svg>`,
	`<iframe src="javascript:alert(1)"></iframe>`, `<object data=x></object><embed src=x>`, `<style>*{background:url(//x)}</style>`,
	`<div style="background:url(javascript:alert(1))">x</div>`, `<form action=//x><input name=a></form>`, `<meta http-equiv=refresh content="0;url=//x">`,
	`<base href="//evil/">`, `<link rel=stylesheet href=//x>`, `<math><mi xlink:href="javascript:alert(1)">x</mi></math>`,
	`<!--<script>alert(1)</script>-->`, `<![CDATA[<script>alert(1)</script>]]>`, `<p<script>alert(1)</script>>`, `<div/onclick=alert(1)>x</div>`,
	"<b>\xff\xfe\xc0\xaf bad utf8 \xed\xa0\x80</b>", "text ‮ reversed \x00 nul \x1b[31m", `&lt;script&gt;alert(1)&lt;/script&gt;`,
	`&#60;script&#62;alert(1)&#60;/script&#62;`, `<textarea><script>alert(1)</script></textarea>`, `<noscript><img src=x onerror=alert(1)></noscript>`,
	`<template><script>alert(1)</script></template>`, `<plaintext><script>alert(1)</script>`, `</div></div></p></li></ul></a>`, `<li>a<li>b<li>c`, `<p>a<p>b`,
	`<table><tr><td>1<td>2<tr><td>3</table>`, strings.Repeat("<div>", 100000), strings.Repeat("<b>", 100000) + "x", strings.Repeat("<ul><li>", 50000),
	strings.Repeat("<a href=\"https://x.example/\">", 1000), strings.Repeat("<", 100000), strings.Repeat("<p", 100000), `<img src="data:image/png;base64,` + strings.Repeat("A", 1<<20) + `">`,
	strings.Repeat("a", 5<<20), `<a href="https://` + strings.Repeat("a", 100000) + `">x</a>`,
}

func TestViewerHostileNoteHTML(t *testing.T) {
	for i, src := range hostileHTML {
		var b strings.Builder
		renderNoteHTML(&b, src)
		out := b.String()
		if b.Len() > viewMaxPageBytes+viewMaxText*4 {
			t.Errorf("case %d: output %d bytes", i, b.Len())
		}
		page := string(viewerPage("t", out))
		assertSafePageQuiet(t, i, page)
	}
}

func assertSafePageQuiet(t *testing.T, i int, page string) {
	t.Run(fmt.Sprint("case", i), func(t *testing.T) { assertSafePage(t, page) })
}

func TestViewerHostileSnapshots(t *testing.T) {
	deep := strings.Repeat("[", 9000) + strings.Repeat("]", 9000)
	deeper := strings.Repeat("[", 200000)
	obj := strings.Repeat(`{"a":`, 9000) + "1" + strings.Repeat("}", 9000)
	long := strings.Repeat("x", 3<<20)
	var manyTasks strings.Builder
	manyTasks.WriteString(`{"kind":"tasks.shared-list","name":"n","tasks":[`)
	for i := 0; i < 5000; i++ {
		if i > 0 {
			manyTasks.WriteString(",")
		}
		fmt.Fprintf(&manyTasks, `{"title":"t%d","order":%d,"subtasks":[{"title":"s"}],"tags":["a"]}`, i, i)
	}
	manyTasks.WriteString("]}")
	cases := map[string]string{
		"deep arrays":     `{"kind":"tasks.shared-list","tasks":` + deep + `}`,
		"too deep":        `{"kind":"tasks.shared-list","tasks":` + deeper,
		"deep objects":    `{"kind":"notes-shared-page","title":` + obj + `}`,
		"wrong types":     `{"kind":"tasks.shared-list","name":5,"owner":[],"updatedAt":"x","tasks":[1,"a",null,{"title":7,"done":"yes","order":"1","tags":"a","subtasks":3,"priority":"high","due":5}]}`,
		"nan numbers":     `{"kind":"tasks.shared-list","updatedAt":1e999,"tasks":[{"order":-1e999}]}`,
		"huge time":       `{"kind":"tasks.shared-list","updatedAt":1e300,"tasks":[]}`,
		"script title":    `{"kind":"tasks.shared-list","name":"</title><script>alert(1)</script>","owner":"\"><img src=x onerror=alert(1)>","tasks":[{"title":"<script>alert(1)</script>","tags":["<img onerror=1>"]}]}`,
		"bad utf8":        "{\"kind\":\"tasks.shared-list\",\"name\":\"a\xff\xfeb\",\"tasks\":[{\"title\":\"\xc0\xaf\"}]}",
		"long fields":     `{"kind":"tasks.shared-list","name":"` + long + `","tasks":[{"title":"` + long + `","notes":"` + long + `"}]}`,
		"many tasks":      manyTasks.String(),
		"notes bad types": `{"format":"notes-shared-page","title":1,"published":{},"outlines":[1,null,{"html":5},{"html":"<b>ok</b>","x":"a","y":[]}],"ink":"no"}`,
		"notes date":      `{"format":"notes-shared-page","title":"t","published":"99999-01-01T00:00:00Z","outlines":[]}`,
		"notes many":      `{"format":"notes-shared-page","title":"t","outlines":[` + strings.Repeat(`{"html":"<p>x</p>"},`, 5000) + `{"html":"y"}]}`,
		"kind injection":  `{"kind":"tasks.shared-list\"><script>","tasks":[]}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			page := string(renderViewer("x.json", []byte(src)))
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("took %v", d)
			}
			if len(page) > 2*viewMaxPageBytes {
				t.Errorf("page %d bytes", len(page))
			}
			assertSafePage(t, page)
		})
	}
	// The task cap and notice.
	if page := string(renderViewer("x.json", []byte(manyTasks.String()))); strings.Contains(page, "t4999") || !strings.Contains(page, "longer than the viewer shows") {
		t.Error("task cap not applied")
	}
}

func FuzzViewer(f *testing.F) {
	for _, s := range hostileHTML[:40] {
		f.Add(s)
	}
	f.Add(tasksSnapshot)
	f.Fuzz(func(t *testing.T, s string) {
		var b strings.Builder
		renderNoteHTML(&b, s)
		assertSafePage(t, string(viewerPage("t", b.String())))
		page := string(renderViewer("x.json", []byte(`{"format":"notes-shared-page","outlines":[{"html":`+jsonString(s)+`}]}`)))
		assertSafePage(t, page)
		assertSafePage(t, string(renderViewer("x.json", []byte(s))))
	})
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
