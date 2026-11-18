// Package version reports the Homestead build version.
package version

// Version is the current Homestead version. Release engineering overrides
// it at link time, e.g. -ldflags "-X homestead/internal/version.Version=1.2.3".
var Version = "0.0.2"
