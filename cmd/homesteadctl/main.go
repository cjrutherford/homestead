// Command homesteadctl is the Homestead command-line client.
//
// S002 bootstrap placeholder: it reports its version and exits. The real
// API-driven CLI arrives in S018. See ADR-004.
package main

import (
	"flag"
	"fmt"

	"homestead/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	fmt.Println("homesteadctl " + version.Version + " (bootstrap placeholder; full CLI arrives in S018)")
}
