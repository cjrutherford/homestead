package everything

import (
	"bytes"
	"encoding/json"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SourcePath is the signed-in page that offers the source of a copyleft app
// (S423, ADR-057). SourceDirEnv names the directory the image build filled
// with offer.json and the source tarball; an app without offer.json has no
// source page and no link.
const (
	SourcePath   = "/_homestead/source"
	SourceDirEnv = "HOMESTEAD_EVERYTHING_SOURCE_DIR"
	// DefaultSourceDir is where the Everything image build puts the offer.
	DefaultSourceDir = "/srv/source"
	maxOfferBytes    = 64 << 10
)

// sourceOffer is offer.json, written by scripts/everything/make_source_offer.py.
type sourceOffer struct {
	App        string `json:"app"`
	Title      string `json:"title"`
	License    string `json:"license"`
	LicenseURL string `json:"license_url"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	Tarball    *struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"tarball"`
	Upstream *struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	} `json:"upstream_archive"`
	Components []sourceComponent `json:"components"`
	// LinkOffset places the Source link (CSS lengths for top, right, bottom,
	// left) where an app has nothing under it; the default is the bottom-right corner.
	LinkOffset map[string]string `json:"link_offset"`
	dir        string
}

type sourceComponent struct {
	Name        string `json:"name"`
	License     string `json:"license"`
	Version     string `json:"version"`
	SourceURL   string `json:"source_url"`
	LicenseURL  string `json:"license_url"`
	LicenseFile string `json:"license_file"` // served by the app, relative to its base
	Note        string `json:"note"`
}

var (
	offerApp     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	offerTarball = regexp.MustCompile(`^[a-z][a-z0-9-]*-source\.tar\.gz$`)
	offerLength  = regexp.MustCompile(`^[0-9]{1,3}(px|%)$`)
	offerHex     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	offerCommit  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// loadSourceOffer reads dir/offer.json. It returns nil, and the app serves no
// source page, when there is no offer or the offer is malformed: a bad offer
// is never half shown.
func loadSourceOffer(dir string) *sourceOffer {
	if dir == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(dir, "offer.json"))
	if err != nil {
		return nil
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxOfferBytes+1))
	if err != nil || len(body) > maxOfferBytes {
		return nil
	}
	var o sourceOffer
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&o) != nil || !offerApp.MatchString(o.App) || o.Title == "" || o.License == "" ||
		!httpsURL(o.Repository) || !offerCommit.MatchString(o.Commit) || (o.LicenseURL != "" && !httpsURL(o.LicenseURL)) {
		return nil
	}
	if o.Tarball != nil {
		if o.Tarball.Name != o.App+"-source.tar.gz" || !offerTarball.MatchString(o.Tarball.Name) ||
			!offerHex.MatchString(o.Tarball.SHA256) || o.Tarball.Size < 1 {
			return nil
		}
		if st, err := os.Stat(filepath.Join(dir, o.Tarball.Name)); err != nil || !st.Mode().IsRegular() || st.Size() != o.Tarball.Size {
			return nil
		}
	}
	if o.Upstream != nil && !offerHex.MatchString(o.Upstream.SHA256) {
		return nil
	}
	for _, c := range o.Components {
		clean := path.Clean("/" + c.LicenseFile)
		if c.Name == "" || c.License == "" || !httpsURL(c.SourceURL) || (c.LicenseURL != "" && !httpsURL(c.LicenseURL)) ||
			(c.LicenseFile != "" && (clean != "/"+c.LicenseFile || strings.Contains(c.LicenseFile, "\\"))) {
			return nil
		}
	}
	for k, v := range o.LinkOffset {
		if (k != "top" && k != "right" && k != "bottom" && k != "left") || !offerLength.MatchString(v) {
			return nil
		}
	}
	if o.Tarball == nil && len(o.Components) == 0 {
		return nil
	}
	o.dir = dir
	return &o
}

var sourceTemplate = template.Must(template.New("source").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Source code - {{.Title}}</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:46rem;margin:2rem auto;padding:0 1rem;color:#1b1b1b;background:#fff}
code{font:14px ui-monospace,monospace;word-break:break-all}dt{font-weight:600;margin-top:.8rem}dd{margin:0}</style></head>
<body><main>
<h1>Source code for {{.Title}}</h1>
{{if .Tarball}}<p>{{.Title}} is free software under the {{.License}} licence{{if .LicenseURL}} (<a href="{{.LicenseURL}}" rel="noopener noreferrer">licence text</a>){{end}}.
Homestead runs it with its own changes. The complete corresponding source of the version running here is offered to every person who uses it, at no charge.</p>
<h2>Download</h2>
<p><a href="{{.TarballHref}}">{{.Tarball.Name}}</a> ({{.Tarball.Size}} bytes)<br>SHA-256 <code>{{.Tarball.SHA256}}</code></p>
<p>It holds the pinned upstream source archive{{if .Upstream}} (<code>{{.Upstream.Name}}</code>, SHA-256 <code>{{.Upstream.SHA256}}</code>){{end}}, Homestead's patch list and added files, the browser shim, the preparation script and the image recipe, with a README that says how to rebuild.</p>
{{else}}<p>{{.Title}} is itself under a permissive licence ({{.License}}), but it includes the free-software components listed below. Their source is offered here.</p>
{{end}}<h2>Upstream</h2>
<dl><dt>Repository</dt><dd><a href="{{.Repository}}" rel="noopener noreferrer">{{.Repository}}</a></dd>
<dt>Pinned commit</dt><dd><code>{{.Commit}}</code></dd>
<dt>Path in the repository</dt><dd><code>{{.Path}}</code></dd></dl>
{{if .Components}}<h2>Bundled components</h2>
<dl>{{range .Components}}<dt>{{.Name}}{{if .Version}} {{.Version}}{{end}} ({{.License}})</dt>
<dd>{{if .Note}}{{.Note}}<br>{{end}}<a href="{{.SourceURL}}" rel="noopener noreferrer">Source</a>{{if .LicenseFile}} - <a href="{{.LicenseHref}}">licence text served with this app</a>{{end}}{{if .LicenseURL}} - <a href="{{.LicenseURL}}" rel="noopener noreferrer">licence text</a>{{end}}</dd>{{end}}</dl>
{{end}}<p><a href="{{.Home}}">Back to {{.Title}}</a></p>
</main></body></html>
`))

type sourceView struct {
	*sourceOffer
	TarballHref string
	Home        string
	Components  []sourceComponentView
}

type sourceComponentView struct {
	sourceComponent
	LicenseHref string
}

// sourceCSP is the page's policy: no script of any kind, inline styles only.
const sourceCSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func (a *appServer) sourceHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", sourceCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "private, no-cache")
}

// source serves the offer page and the tarball to signed-in people. The offer
// is made to the people who use the app (AGPL section 13), and every one of
// them is signed in; the closed-by-default catalog keeps anyone else out, as
// it does for the app itself.
func (a *appServer) source(w http.ResponseWriter, r *http.Request, p string) {
	o := a.offer
	a.sourceHeaders(w)
	if o == nil {
		deny(w, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		deny(w, http.StatusMethodNotAllowed)
		return
	}
	isPage := p == SourcePath
	if !isPage && (o.Tarball == nil || p != SourcePath+"/"+o.Tarball.Name) {
		deny(w, http.StatusNotFound)
		return
	}
	if _, _, ok := a.src.Session(r); !ok {
		if isPage {
			http.Redirect(w, r, a.base+LoginPath, http.StatusFound)
			return
		}
		deny(w, http.StatusUnauthorized)
		return
	}
	if !isPage {
		f, err := os.Open(filepath.Join(o.dir, o.Tarball.Name))
		if err != nil {
			deny(w, http.StatusNotFound)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+o.Tarball.Name+`"`)
		http.ServeContent(w, r, o.Tarball.Name, time.Time{}, f)
		return
	}
	v := sourceView{sourceOffer: o, Home: a.base + "/"}
	if o.Tarball != nil {
		v.TarballHref = a.base + SourcePath + "/" + o.Tarball.Name
	}
	for _, c := range o.Components {
		cv := sourceComponentView{sourceComponent: c}
		if c.LicenseFile != "" {
			cv.LicenseHref = a.base + "/" + c.LicenseFile
		}
		v.Components = append(v.Components, cv)
	}
	var buf bytes.Buffer
	if err := sourceTemplate.Execute(&buf, v); err != nil {
		deny(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

var bodyTag = regexp.MustCompile(`(?i)<body(\s[^>]*)?>`)

// sourceLink is the small fixed corner link added to the served index.html.
// It is plain HTML with a style attribute (the policy allows styles, no new
// script), kept in a corner and dimmed so it covers no control.
func (a *appServer) sourceLink() string {
	label := "Source code offer for " + a.offer.Title + " (" + a.offer.License + ")"
	pos := ""
	for _, k := range []string{"top", "right", "bottom", "left"} {
		if v, ok := a.offer.LinkOffset[k]; ok {
			pos += k + ":" + v + ";"
		}
	}
	if len(a.offer.LinkOffset) == 0 {
		pos = "right:4px;bottom:4px;"
	}
	return `<a id="homestead-source" href="` + html.EscapeString(a.base+SourcePath) + `" aria-label="` + html.EscapeString(label) +
		`" title="` + html.EscapeString(label) + `" style="position:fixed;` + pos + `z-index:2147483647;` +
		`font:11px/1.2 system-ui,sans-serif;padding:2px 6px;border-radius:3px;background:rgba(0,0,0,.65);` +
		`color:#fff;text-decoration:none;opacity:.75">Source</a>`
}
