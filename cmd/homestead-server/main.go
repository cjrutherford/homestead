// Command homestead-server is the Homestead control daemon.
//
// Native static Go daemon, systemd-managed (S011), root-owned. See ADR-001.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/config"
	"homestead/internal/dashboard"
	"homestead/internal/desired"
	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/firewall"
	"homestead/internal/layout"
	"homestead/internal/mdns"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/server"
	"homestead/internal/store"
	"homestead/internal/version"
	"homestead/internal/wizard"
)

func main() {
	listen := flag.String("listen", server.DefaultAddr, "address to bind (loopback by default; see ADR-006)")
	configPath := flag.String("config", desired.DefaultPath(), "desired-state file (S014)")
	dataDir := flag.String("data-dir", layout.LibDir.Path, "operational state directory (S012)")
	socketPath := flag.String("socket", server.DefaultSocketPath, "root-only recovery socket (S021)")
	catalogDir := flag.String("catalog-dir", "catalog", "curated package directory (S160; packaging installs it)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	if err := run(*listen, *configPath, *dataDir, *socketPath, *catalogDir); err != nil {
		fmt.Fprintln(os.Stderr, "homestead-server:", err)
		os.Exit(1)
	}
}

func run(listen, configPath, dataDir, socketPath, catalogDir string) error {
	// The state directory is provisioned by S012 and never created here:
	// a missing data dir fails loudly instead of scattering state.
	st, err := store.Open(filepath.Join(dataDir, store.DBFileName))
	if err != nil {
		return err
	}
	defer st.Close()
	// No operation record may claim to run forever: fail anything a dead
	// daemon left behind (S061).
	if err := operation.NewRunner(st).Recover(); err != nil {
		return err
	}
	claimSvc := claim.New(st)
	authSvc := auth.New(st, claimSvc)
	// Domain slices register their settings fields here; the config API,
	// CLI, and dashboard render them from this one schema (S015/S016).
	srv := server.New(listen)
	configSvc := config.New(firewall.Schema(), configPath)
	srv.MountConfig(configSvc)
	configSecrets, err := secret.New(st, secret.DefaultKeyPath())
	if err != nil {
		return err
	}
	srv.MountConfigIO(configPath, configSecrets)
	srv.MountClaim(claimSvc)
	srv.MountAuth(authSvc, audit.New(st))
	srv.MountAudit(audit.New(st))
	srv.MountRecovery(authSvc)
	srv.MountWizards(wizard.New(st))
	srv.MountDoctor()
	srv.MountOperations(operation.New(st))
	srv.MountCatalog(st, catalogDir)
	srv.MountStorage(st)
	srv.MountBackups(st)
	srv.MountDrift(st, catalogDir)
	dockerClient := docker.NewClient("")
	srv.MountDocker(dockerClient)
	srv.MountDockge(dockerClient)
	srv.MountEdge(st)
	srv.MountUpdates(st, catalogDir, dockerClient)
	if port, err := daemonPort(listen); err != nil {
		return err
	} else {
		srv.MountFirewall(port, configSvc)
		srv.MountExposure(st, port)
	}
	if files, ok := dashboard.Embedded(); ok {
		srv.MountDashboard(dashboard.New(files))
	} else {
		fmt.Fprintln(os.Stderr, "homestead-server: dashboard UI not embedded (make dashboard-embed, build with -tags embeddash)")
	}
	if err := ensureClaimToken(claimSvc); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// First-run local discovery (S221): answer homestead.local plus
	// the service advertisement on the LAN, with live claim state so
	// owners spot unclaimed boxes. Discovery is convenience, never
	// boot-critical: failures only warn.
	startLocalDiscovery(ctx, claimSvc, listen)
	// Both listeners share ctx and drain together. A socket failure is
	// fatal: silent loss of the recovery channel is worse than no start.
	unixErr := make(chan error, 1)
	go func() {
		unixErr <- srv.RunUnix(ctx, socketPath)
	}()
	if err := srv.Run(ctx); err != nil {
		return err
	}
	return <-unixErr
}

// startLocalDiscovery serves mDNS answers in the background:
// homestead.local A records plus the service advertisement carrying
// live claim state and LAN addresses. A bad listen address or a
// taken mDNS port only warns on stderr — discovery must never fail
// the boot it ornaments.
func startLocalDiscovery(ctx context.Context, claimSvc *claim.Service, listen string) {
	port, err := daemonPort(listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "homestead-server: local discovery off:", err)
		return
	}
	cfg := mdns.Config{
		Hostname: "homestead",
		Port:     port,
		Version:  version.Version,
		State: func() string {
			state, err := claimSvc.CurrentState()
			if err != nil {
				return "unknown"
			}
			return state.String()
		},
		Addresses: func() []netip.Addr {
			network, err := discover.DiscoverNetwork()
			if err != nil {
				return nil
			}
			return mdns.LANIPv4(network)
		},
	}
	go func() {
		if err := mdns.ListenAndServe(ctx, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "homestead-server: local discovery off:", err)
		}
	}()
}

// daemonPort extracts the serving port for firewall self-protection.
// A malformed listen address fails loudly: protecting an unknown port
// would be worse than refusing to start.
func daemonPort(listen string) (int, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, fmt.Errorf("homestead-server: bad listen address %q: %w", listen, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("homestead-server: bad listen port %q", port)
	}
	return n, nil
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
