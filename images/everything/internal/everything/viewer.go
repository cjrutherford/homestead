package everything

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// ViewPrefix is the route under the application's base path that serves the
// read-only viewer for a read link (ADR-058 section 4). It uses the same
// token, expiry, revocation and rate limit as ReadPrefix.
const ViewPrefix = "/_homestead/view/"

// Bounds for what the viewer will look at. The file is untrusted data.
const (
	viewMaxFile      = 8 << 20   // larger files get a "too large" page
	viewMaxPageBytes = 1 << 20   // rendered output cap
	viewMaxTasks     = 2000      // tasks shown
	viewMaxSubtasks  = 100       // subtasks shown per task
	viewMaxTags      = 20        // tags shown per task
	viewMaxOutlines  = 200       // note outlines shown
	viewMaxDepth     = 32        // open elements in the rendered notes HTML
	viewMaxHTMLBytes = 4 << 20   // tokenizer buffer per outline
	viewMaxText      = 20000     // runes of one text field
	viewMaxTitle     = 300       // runes of a title or name
	viewMaxLinkLen   = 2048      // bytes of a shown link
	viewTokenCeiling = 5_000_000 // tokens examined per outline
)

// viewerCSS is the one fixed stylesheet. The policy carries its hash, so no
// other style (and no style attribute) can apply.
const viewerCSS = `:root{color-scheme:light dark}
body{margin:0;font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;background:#fafaf8;color:#1d1d1b}
main{max-width:46rem;margin:0 auto;padding:1.5rem 1rem 4rem}
header.top{border-bottom:1px solid #c9c9c2;margin-bottom:1rem;padding-bottom:.5rem}
h1{font-size:1.6rem;margin:.2rem 0;overflow-wrap:anywhere}
.meta,.banner{color:#5b5b55;font-size:.875rem}
.banner{margin:0 0 .25rem}
ul.tasks,ul.sub{list-style:none;margin:0;padding:0}
li.task{border-bottom:1px solid #e1e1da;padding:.6rem 0}
li.task.done .tt{text-decoration:line-through;color:#6b6b64}
.tt{font-weight:600;overflow-wrap:anywhere}
.box{display:inline-block;width:1.4rem}
.notes{white-space:pre-wrap;overflow-wrap:anywhere;margin:.25rem 0 0 1.4rem}
.meta.line{margin:.25rem 0 0 1.4rem}
ul.sub{margin:.25rem 0 0 1.4rem}
ul.sub li{overflow-wrap:anywhere}
.ns{display:inline-block;border:1px dashed #8a8a80;color:#5b5b55;font-size:.8125rem;font-style:italic;padding:0 .4rem}
.outline{margin:0 0 1.25rem;overflow-wrap:anywhere}
.h1{font-size:1.4rem;font-weight:700;margin:.6rem 0 .2rem}
.h2{font-size:1.2rem;font-weight:700;margin:.5rem 0 .2rem}
.h3{font-size:1.05rem;font-weight:700;margin:.4rem 0 .2rem}
.p{margin:.35rem 0}
blockquote{border-left:3px solid #bbb;margin:.4rem 0;padding-left:.75rem;color:#555}
pre{background:#eeeee8;padding:.4rem .6rem;white-space:pre-wrap;overflow-wrap:anywhere}
.cell{display:inline-block;border:1px solid #c9c9c2;padding:0 .3rem;margin:0 .1rem 0 0}
a{color:#0b5cad}
.empty{color:#5b5b55}
@media (prefers-color-scheme:dark){body{background:#1b1b19;color:#ecece6}.meta,.banner,.ns,.empty{color:#a8a8a0}header.top{border-color:#44443f}li.task{border-color:#33332f}pre{background:#2a2a27}a{color:#8ab4f8}.cell{border-color:#44443f}blockquote{color:#b5b5ad}}`

// ViewerPolicy is the viewer's Content-Security-Policy: no script, no images,
// no external request, one hashed stylesheet.
var ViewerPolicy = func() string {
	sum := sha256.Sum256([]byte(viewerCSS))
	return "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) +
		"'; img-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}()

// renderViewer turns the linked file into a complete page. It never fails:
// anything it cannot show becomes a page that says so. The kind comes from
// the JSON "kind" (Tasks) or "format" (Notes) field of a .json file; no other
// file is parsed and nothing is sniffed.
func renderViewer(key string, value []byte) []byte {
	if len(value) > viewMaxFile {
		return viewerNotice("Too large to show", "This file is too large for the viewer.")
	}
	if strings.ToLower(path.Ext(key)) == ".json" {
		var doc any
		if err := json.Unmarshal(value, &doc); err == nil {
			if m, ok := doc.(map[string]any); ok {
				kind := vStr(m["kind"], 64)
				if kind == "" {
					kind = vStr(m["format"], 64)
				}
				switch kind {
				case "tasks.shared-list":
					return renderTasks(m)
				case "notes-shared-page":
					return renderNotes(m)
				}
			}
		}
	}
	return viewerNotice("No viewer for this file", "This kind of file has no viewer here. The owner can share it another way.")
}

func viewerNotice(title, msg string) []byte {
	var b strings.Builder
	b.WriteString("<header class=\"top\"><p class=\"banner\">Shared read-only</p><h1>" + vEsc(title, viewMaxTitle) + "</h1></header>")
	b.WriteString("<p class=\"empty\">" + vEsc(msg, viewMaxTitle) + "</p>")
	return viewerPage(title, b.String())
}

func viewerPage(title, body string) []byte {
	return []byte("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">" +
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">" +
		"<meta name=\"referrer\" content=\"no-referrer\"><meta name=\"robots\" content=\"noindex, nofollow\">" +
		"<title>" + vEsc(title, viewMaxTitle) + "</title><style>" + viewerCSS + "</style></head><body><main>" +
		body + "</main></body></html>\n")
}

// --- untrusted-value helpers ---

// vClean makes s printable, valid UTF-8 of at most max runes. Control
// characters other than newline and tab, and bidirectional overrides, are
// dropped.
func vClean(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= max {
			break
		}
		switch {
		case r == '\n' || r == '\t':
		case r == '\r':
			continue
		case unicode.IsControl(r), r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0xFEFF:
			continue
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// vEsc is the only way text reaches the page: cleaned, then HTML-escaped.
func vEsc(s string, max int) string { return html.EscapeString(vClean(s, max)) }

func vStr(v any, max int) string {
	s, _ := v.(string)
	return vClean(s, max)
}

func vArr(v any) []any {
	a, _ := v.([]any)
	return a
}

func vNum(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// --- Tasks ---

func renderTasks(m map[string]any) []byte {
	name := vStr(m["name"], viewMaxTitle)
	if name == "" {
		name = "Shared list"
	}
	var b strings.Builder
	b.WriteString("<header class=\"top\"><p class=\"banner\">Shared read-only list</p><h1>" + vEsc(name, viewMaxTitle) + "</h1><p class=\"meta\">")
	var meta []string
	if o := vStr(m["owner"], viewMaxTitle); o != "" {
		meta = append(meta, "Shared by "+html.EscapeString(o))
	}
	if t, ok := vTime(m["updatedAt"]); ok {
		meta = append(meta, "Updated "+t.Format("2006-01-02 15:04")+" UTC")
	}
	b.WriteString(strings.Join(meta, " &middot; ") + "</p></header>")

	type task struct {
		m     map[string]any
		order float64
		done  bool
	}
	var tasks []task
	for _, t := range vArr(m["tasks"]) {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		o, _ := vNum(tm["order"])
		d, _ := tm["done"].(bool)
		tasks = append(tasks, task{tm, o, d})
	}
	// The owner's manual order, open tasks first.
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].done != tasks[j].done {
			return !tasks[i].done
		}
		return tasks[i].order < tasks[j].order
	})
	if len(tasks) == 0 {
		b.WriteString("<p class=\"empty\">This list has no tasks.</p>")
	} else {
		b.WriteString("<ul class=\"tasks\">")
		for i, t := range tasks {
			if i >= viewMaxTasks {
				break
			}
			writeTask(&b, t.m, t.done)
			if b.Len() > viewMaxPageBytes {
				break
			}
		}
		b.WriteString("</ul>")
		if len(tasks) > viewMaxTasks || b.Len() > viewMaxPageBytes {
			b.WriteString("<p class=\"empty\">The list is longer than the viewer shows.</p>")
		}
	}
	return viewerPage(name+" (shared list)", b.String())
}

func vTime(v any) (time.Time, bool) {
	f, ok := vNum(v)
	if !ok || f <= 0 || f > 253402300799000 {
		return time.Time{}, false
	}
	return time.UnixMilli(int64(f)).UTC(), true
}

func writeTask(b *strings.Builder, t map[string]any, done bool) {
	cls := "task"
	box := "&#9744;"
	if done {
		cls += " done"
		box = "&#9745;"
	}
	title := vStr(t["title"], viewMaxTitle)
	if title == "" {
		title = "(untitled)"
	}
	b.WriteString("<li class=\"" + cls + "\"><div class=\"tt\"><span class=\"box\">" + box + "</span>" + html.EscapeString(title) + "</div>")
	if n := vStr(t["notes"], viewMaxText); n != "" {
		b.WriteString("<div class=\"notes\">" + html.EscapeString(n) + "</div>")
	}
	var meta []string
	if done {
		meta = append(meta, "Done")
	}
	if d, ok := t["due"].(string); ok {
		if _, err := time.Parse("2006-01-02", d); err == nil && len(d) == 10 {
			due := d
			if tm, ok := t["dueTime"].(string); ok && len(tm) == 5 {
				if _, err := time.Parse("15:04", tm); err == nil {
					due += " " + tm
				}
			}
			meta = append(meta, "Due "+due)
		}
	}
	if p, ok := vNum(t["priority"]); ok {
		switch p {
		case 1:
			meta = append(meta, "Low priority")
		case 2:
			meta = append(meta, "Medium priority")
		case 3:
			meta = append(meta, "High priority")
		}
	}
	tags := 0
	for _, tg := range vArr(t["tags"]) {
		s := vStr(tg, 64)
		if s == "" {
			continue
		}
		if tags++; tags > viewMaxTags {
			break
		}
		meta = append(meta, "#"+html.EscapeString(strings.Join(strings.Fields(s), "-")))
	}
	if len(meta) > 0 {
		b.WriteString("<div class=\"meta line\">" + strings.Join(meta, " &middot; ") + "</div>")
	}
	subs := 0
	var sb strings.Builder
	for _, s := range vArr(t["subtasks"]) {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if subs++; subs > viewMaxSubtasks {
			break
		}
		mark := "&#9744; "
		if d, _ := sm["done"].(bool); d {
			mark = "&#9745; "
		}
		sb.WriteString("<li>" + mark + vEsc(vStr(sm["title"], viewMaxTitle), viewMaxTitle) + "</li>")
	}
	if sb.Len() > 0 {
		b.WriteString("<ul class=\"sub\">" + sb.String() + "</ul>")
	}
	b.WriteString("</li>")
}

// --- Notes ---

const noteMarker = "<span class=\"ns\">not shown in this viewer</span>"

func renderNotes(m map[string]any) []byte {
	title := vStr(m["title"], viewMaxTitle)
	if title == "" {
		title = "Shared page"
	}
	var b strings.Builder
	b.WriteString("<header class=\"top\"><p class=\"banner\">Shared read-only page</p><h1>" + vEsc(title, viewMaxTitle) + "</h1><p class=\"meta\">")
	if p, ok := m["published"].(string); ok {
		if t, err := time.Parse(time.RFC3339, p); err == nil && t.Year() <= 9999 && t.Year() >= 1 {
			b.WriteString("Published " + t.UTC().Format("2006-01-02 15:04") + " UTC")
		}
	}
	b.WriteString("</p></header>")

	type outline struct {
		html string
		x, y float64
	}
	var outs []outline
	for _, o := range vArr(m["outlines"]) {
		om, ok := o.(map[string]any)
		if !ok {
			continue
		}
		s, _ := om["html"].(string)
		x, _ := vNum(om["x"])
		y, _ := vNum(om["y"])
		outs = append(outs, outline{s, x, y})
	}
	sort.SliceStable(outs, func(i, j int) bool {
		if outs[i].y != outs[j].y {
			return outs[i].y < outs[j].y
		}
		return outs[i].x < outs[j].x
	})
	shortened := false
	for i, o := range outs {
		if i >= viewMaxOutlines || b.Len() > viewMaxPageBytes {
			shortened = true
			break
		}
		b.WriteString("<div class=\"outline\">")
		renderNoteHTML(&b, o.html)
		b.WriteString("</div>")
	}
	if len(outs) == 0 {
		b.WriteString("<p class=\"empty\">This page has no text.</p>")
	}
	if ink := vArr(m["ink"]); len(ink) > 0 {
		b.WriteString("<p>Handwriting and drawings: " + noteMarker + "</p>")
	}
	if shortened {
		b.WriteString("<p class=\"empty\">The page is longer than the viewer shows.</p>")
	}
	return viewerPage(title+" (shared page)", b.String())
}

type openEl struct{ src, out string }

// mapping of source tag to emitted tag; the class names a div's role.
var noteBlocks = map[string]string{
	"p": `<div class="p">`, "div": "<div>", "section": "<div>", "article": "<div>", "figure": "<div>", "figcaption": "<div>",
	"h1": `<div class="h1">`, "h2": `<div class="h2">`, "h3": `<div class="h3">`,
	"h4": `<div class="h3">`, "h5": `<div class="h3">`, "h6": `<div class="h3">`,
	"table": "<div>", "thead": "<div>", "tbody": "<div>", "tr": "<div>",
	"td": `<span class="cell">`, "th": `<span class="cell">`,
	"blockquote": "<blockquote>", "pre": "<pre>", "ul": "<ul>", "ol": "<ol>", "li": "<li>",
	"b": "<strong>", "strong": "<strong>", "i": "<em>", "em": "<em>", "u": "<u>",
	"s": "<s>", "strike": "<s>", "del": "<s>", "code": "<code>", "kbd": "<code>",
	"sub": "<sub>", "sup": "<sup>", "mark": "<mark>", "a": "", // a is built from href
}

var noteVoid = map[string]bool{"br": true, "hr": true, "img": true, "col": true, "input": true, "meta": true, "link": true, "base": true, "source": true, "track": true, "wbr": true, "area": true, "embed": true}

// elements whose content is dropped; the ones that stand for media or
// embedded content leave the marker.
var noteDropSilent = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "head": true, "title": true, "button": true, "select": true, "textarea": true, "form": true, "frameset": true, "frame": true, "applet": true, "noembed": true, "noframes": true, "xmp": true, "plaintext": true}
var noteDropMarker = map[string]bool{"svg": true, "math": true, "iframe": true, "object": true, "video": true, "audio": true, "canvas": true, "picture": true}

func closeTag(open string) string {
	switch {
	case strings.HasPrefix(open, "<span"):
		return "</span>"
	case strings.HasPrefix(open, "<a"):
		return "</a>"
	}
	i := strings.IndexAny(open[1:], " >")
	return "</" + open[1:1+i] + ">"
}

// safeHref returns a link target only for an absolute http or https URL made
// of printable ASCII.
func safeHref(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > viewMaxLinkLen {
		return "", false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= 0x20 || raw[i] >= 0x7f || strings.IndexByte("\"'<>`\\", raw[i]) >= 0 {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return "", false
	}
	return raw, true
}

// renderNoteHTML renders one outline's HTML through an allowlist subset. The
// source is only tokenized, never copied: text is re-escaped and every tag is
// one the renderer chooses to emit, so nothing passes through.
func renderNoteHTML(out *strings.Builder, src string) {
	tz := html.NewTokenizer(bytes.NewReader([]byte(src)))
	tz.SetMaxBuf(viewMaxHTMLBytes)
	var stack []openEl
	skip, skipDepth := "", 0
	startLen := out.Len()
	budget := viewTokenCeiling
	closeTo := func(i int) {
		for len(stack) > i {
			out.WriteString(stack[len(stack)-1].out)
			stack = stack[:len(stack)-1]
		}
	}
	insideA := func() bool {
		for _, e := range stack {
			if e.src == "a" {
				return true
			}
		}
		return false
	}
	for budget > 0 && out.Len()-startLen < viewMaxPageBytes {
		budget--
		tt := tz.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.TextToken:
			if skip == "" {
				out.WriteString(vEsc(string(tz.Text()), viewMaxText))
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			nameB, hasAttr := tz.TagName()
			name := strings.ToLower(string(nameB))
			var href string
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = tz.TagAttr()
				if name == "a" && string(k) == "href" {
					href = string(v)
				}
			}
			if skip != "" {
				if name == skip && tt == html.StartTagToken {
					skipDepth++
				}
				continue
			}
			switch {
			case name == "br":
				out.WriteString("<br>")
			case name == "hr":
				out.WriteString("<hr>")
			case name == "img" || name == "embed" || name == "source" || name == "track":
				out.WriteString(noteMarker)
			case noteDropMarker[name] || noteDropSilent[name]:
				if noteDropMarker[name] {
					out.WriteString(noteMarker)
				}
				if tt == html.StartTagToken {
					skip, skipDepth = name, 1
				}
			case noteVoid[name]:
			default:
				open, ok := noteBlocks[name]
				if !ok || tt == html.SelfClosingTagToken && name != "a" {
					continue // unknown element: its text is shown, the tag is not
				}
				if len(stack) >= viewMaxDepth {
					continue
				}
				// Close what HTML would implicitly close, so the emitted
				// markup never depends on a browser's error recovery.
				switch name {
				case "p", "td", "th", "tr", "li":
					if n := len(stack); n > 0 && stack[n-1].src == name {
						closeTo(n - 1)
					}
				}
				if name == "li" {
					if n := len(stack); n == 0 || (stack[n-1].src != "ul" && stack[n-1].src != "ol") {
						open = "<div>"
					}
				}
				if name == "a" {
					h, ok := safeHref(href)
					if !ok || insideA() {
						continue
					}
					open = `<a href="` + html.EscapeString(h) + `" rel="nofollow noopener noreferrer">`
				}
				out.WriteString(open)
				stack = append(stack, openEl{name, closeTag(open)})
			}
		case html.EndTagToken:
			nameB, _ := tz.TagName()
			name := strings.ToLower(string(nameB))
			if skip != "" {
				if name == skip {
					if skipDepth--; skipDepth == 0 {
						skip = ""
					}
				}
				continue
			}
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].src == name {
					closeTo(i)
					break
				}
			}
		}
	}
	closeTo(0)
}
