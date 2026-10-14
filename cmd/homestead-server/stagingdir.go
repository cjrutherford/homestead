package main

import (
	"path/filepath"

	"homestead/internal/layout"
)

// stagingDir is where platform apply stages artifacts (S265): inside the
// private state directory, so only root reads or swaps staged bytes.
func stagingDir() string { return filepath.Join(layout.LibDir.Path, "staging") }
