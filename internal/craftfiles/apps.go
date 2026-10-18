// Package craftfiles is the Nextcloud file proxy for craft apps (S254,
// ADR-029, ADR-032). The daemon holds a sealed Nextcloud app password and
// performs every WebDAV request itself; no credential reaches a browser.
// The package serves a dedicated unix-socket listener behind the Caddy
// edge: a one-time launch code is exchanged for a craft-scoped cookie bound
// to the operator session, and that cookie authorises a fixed WebDAV method
// subset confined to one owner-configured root folder.
package craftfiles

import "sort"

// Mode says how much of the file lifecycle an app's hook supports.
const (
	ModeOpenSave = "open-save" // postMessage bridge: open and save
	ModeOpenOnly = "open-only" // URL hook: open only, saves stay local
)

// App describes one craft app with a usable file hook (S254 scope).
type App struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Mode  string `json:"mode"`
}

var hooked = []App{
	{Name: "cadcraft", Title: "CADCraft", Mode: ModeOpenSave},
	{Name: "effectcraft", Title: "EffectCraft", Mode: ModeOpenOnly},
	{Name: "filmcraft", Title: "FilmCraft", Mode: ModeOpenOnly},
	{Name: "gridcraft", Title: "GridCraft", Mode: ModeOpenSave},
	{Name: "pdfcraft", Title: "PdfCraft", Mode: ModeOpenSave},
}

// Apps lists the hooked craft apps, sorted by name. Every other craft
// app (WordCraft and seven more) ships browser-local only.
func Apps() []App {
	out := append([]App(nil), hooked...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// HookedApp returns the app with a file hook by name.
func HookedApp(name string) (App, bool) {
	for _, a := range hooked {
		if a.Name == name {
			return a, true
		}
	}
	return App{}, false
}
