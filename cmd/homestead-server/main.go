// Command homestead-server is the Homestead control daemon.
//
// Native static Go daemon, systemd-managed (S011), root-owned. See ADR-001.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"homestead/internal/config"
	"homestead/internal/desired"
	"homestead/internal/schema"
	"homestead/internal/server"
	"homestead/internal/version"
)

func main() {
	listen := flag.String("listen", server.DefaultAddr, "address to bind (loopback by default; see ADR-006)")
	configPath := flag.String("config", desired.DefaultPath(), "desired-state file (S014)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Empty schema: domain slices register their fields in later slices;
	// until then the API serves state and rejects unknown settings (S016).
	srv := server.New(*listen)
	srv.MountConfig(config.New(schema.Schema{}, *configPath))
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "homestead-server:", err)
		os.Exit(1)
	}
}
