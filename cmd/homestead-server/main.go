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
	"path/filepath"
	"syscall"

	"homestead/internal/claim"
	"homestead/internal/config"
	"homestead/internal/desired"
	"homestead/internal/layout"
	"homestead/internal/schema"
	"homestead/internal/server"
	"homestead/internal/store"
	"homestead/internal/version"
)

func main() {
	listen := flag.String("listen", server.DefaultAddr, "address to bind (loopback by default; see ADR-006)")
	configPath := flag.String("config", desired.DefaultPath(), "desired-state file (S014)")
	dataDir := flag.String("data-dir", layout.LibDir.Path, "operational state directory (S012)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	if err := run(*listen, *configPath, *dataDir); err != nil {
		fmt.Fprintln(os.Stderr, "homestead-server:", err)
		os.Exit(1)
	}
}

func run(listen, configPath, dataDir string) error {
	// The state directory is provisioned by S012 and never created here:
	// a missing data dir fails loudly instead of scattering state.
	st, err := store.Open(filepath.Join(dataDir, store.DBFileName))
	if err != nil {
		return err
	}
	defer st.Close()
	claimSvc := claim.New(st)
	// Empty schema: domain slices register their fields in later slices;
	// until then the API serves state and rejects unknown settings (S016).
	srv := server.New(listen)
	srv.MountConfig(config.New(schema.Schema{}, configPath))
	srv.MountClaim(claimSvc)
	if err := ensureClaimToken(claimSvc); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return srv.Run(ctx)
}

// ensureClaimToken mints the console token for a fresh appliance that has
// none live. Plaintext appears here once, on the console (journal), and in
// the owner's hands — never in storage.
func ensureClaimToken(svc *claim.Service) error {
	state, err := svc.CurrentState()
	if err != nil {
		return err
	}
	if state == claim.StateClaimed {
		return nil
	}
	live, err := svc.LiveToken()
	if err != nil {
		return err
	}
	if live {
		return nil
	}
	token, err := svc.MintToken(claim.TokenTTL)
	if err != nil {
		return err
	}
	fmt.Printf("homestead-server: UNCLAIMED — claim token (single use, expires in %v):\n%s\n", claim.TokenTTL, token)
	return nil
}
