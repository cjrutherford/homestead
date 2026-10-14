package main

import "path/filepath"

// stagingDirFor is where platform apply stages artifacts (S265, ADR-027):
// inside the private state directory, so only root reads or swaps staged
// bytes. The daemon and the apply unit use the same directory.
func stagingDirFor(dataDir string) string { return filepath.Join(dataDir, "staging") }
