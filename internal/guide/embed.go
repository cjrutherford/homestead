//go:build embeddash

package guide

import (
	"embed"
	"io/fs"
)

// Dist holds the built owner guide. Refresh it with `make guide-build`
// (python3 -I scripts/guide/build.py). all: keeps the committed .gitkeep so a
// tagged build without a built guide still compiles; Embedded then reports
// no guide.
//
//go:embed all:dist
var Dist embed.FS

// Embedded returns the built guide for the daemon to mount.
func Embedded() (fs.FS, bool) {
	sub, err := fs.Sub(Dist, "dist")
	if err != nil || !Available(sub) {
		return nil, false
	}
	return sub, true
}
