//go:build !embeddash

package dashboard

import "io/fs"

// Embedded reports no production assets in plain builds: the daemon runs
// API-only. Build with -tags embeddash after make dashboard-embed for the
// appliance binary that also serves the UI (S031).
func Embedded() (fs.FS, bool) {
	return nil, false
}
