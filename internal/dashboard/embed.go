//go:build embeddash

package dashboard

import (
	"embed"
	"io/fs"
)

// Dist holds the production dashboard. Refresh it after every UI change:
//
//	make dashboard-build && make dashboard-embed
//
// Building with -tags embeddash without running dashboard-embed first
// fails here, telling you exactly what to do.
var (
	//go:embed dist
	Dist embed.FS
)

// Embedded returns the production assets for the daemon to mount.
func Embedded() (fs.FS, bool) {
	sub, err := fs.Sub(Dist, "dist")
	if err != nil {
		return nil, false
	}
	return sub, true
}
