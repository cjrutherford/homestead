//go:build !embeddash

package guide

import "io/fs"

// Embedded reports no guide in plain builds; /guide/ answers 404.
func Embedded() (fs.FS, bool) { return nil, false }
