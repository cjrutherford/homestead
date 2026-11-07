// Command homestead-server is the Homestead control daemon.
//
// Native static Go daemon, systemd-managed (S011), root-owned. See ADR-001.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"homestead/internal/audit"
	"homestead/internal/claim"
	"homestead/internal/dashboard"
	"homestead/internal/desired"
	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/layout"
	"homestead/internal/mdns"
	"homestead/internal/operation"
	"homestead/internal/platformapply"
	"homestead/internal/secret"
	"homestead/internal/server"
	"homestead/internal/store"
	"homestead/internal/version"
)

func main() {
	// The privileged apply unit runs this binary in apply mode (ADR-027).
	if len(os.Args) > 1 && os.Args[1] == "apply-platform" {
		if err := runApplyPlatform(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "homestead-apply:", err)
			os.Exit(1)
		}
		return
	}
	listen := flag.String("listen", server.DefaultAddr, "address to bind (loopback by default; see ADR-006)")
	configPath := flag.String("config", desired.DefaultPath(), "desired-state file (S014)")
	dataDir := flag.String("data-dir", layout.LibDir.Path, "operational state directory (S012)")
	socketPath := flag.String("socket", server.DefaultSocketPath, "root-only recovery socket (S021)")
	dashboardSocket := flag.String("dashboard-socket", docker.DashboardSocketPath, "dashboard socket the edge forwards to (S289, ADR-035)")
	craftSocket := flag.String("craft-socket", docker.CraftSocketPath, "craft file socket the edge forwards to (S254, ADR-032)")
	catalogDir := flag.String("catalog-dir", "catalog", "curated package directory (S160; packaging installs it)")
	claimTokenFile := flag.String("claim-token-file", claim.DefaultTokenPath, "root-only file holding the live claim token while unclaimed (S288)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	if err := run(*listen, *configPath, *dataDir, *socketPath, *craftSocket, *dashboardSocket, *catalogDir, *claimTokenFile); err != nil {
		fmt.Fprintln(os.Stderr, "homestead-server:", err)
		os.Exit(1)
	}
}

func run(listen, configPath, dataDir, socketPath, craftSocket, dashboardSocket, catalogDir, claimTokenFile string) error {
	if err := server.ValidateCraftSocketPath(craftSocket); err != nil {
		return err
	}
	if err := server.ValidateDashboardSocketPath(dashboardSocket); err != nil {
		return err
	}
	// The state directory is provisioned by S012 and never created here:
	// a missing data dir fails loudly instead of scattering state.
	st, err := store.Open(filepath.Join(dataDir, store.DBFileName))
	if err != nil {
		return err
	}
	defer st.Close()
	// No operation record may claim to run forever: fail anything a dead
	// daemon left behind (S061).
	// An in-flight platform update (S265) restarted this process on
	// purpose: the privileged apply unit (ADR-027) finalizes its
	// operation from the persisted marker, so recovery must not fail it
	// as interrupted first.
	var keep []int64
	if marker, ok, err := platformapply.NewStore(st).Pending(); err != nil {
		return err
	} else if ok {
		keep = append(keep, marker.OpID)
	}
	if err := operation.NewRunner(st).RecoverExcept(keep...); err != nil {
		return err
	}
	// Domain slices register their routes in server.MountApp, shared with
	// the route-classification test so wiring cannot drift (S255).
	// Their settings fields feed one schema for the config API, CLI, and
	// dashboard (S015/S016).
	port, err := daemonPort(listen)
	if err != nil {
		return err
	}
	srv := server.New(listen)
	srv.SocketPath = socketPath
	app := server.AppConfig{
		Store: st, ConfigPath: configPath, CatalogDir: catalogDir,
		SecretKeyPath: secret.DefaultKeyPath(), Port: port,
	}
	if files, ok := dashboard.Embedded(); ok {
		app.Dashboard = dashboard.New(files)
	} else {
		fmt.Fprintln(os.Stderr, "homestead-server: dashboard UI not embedded (make dashboard-embed, build with -tags embeddash)")
	}
	svcs, err := server.MountApp(srv, app)
	if err != nil {
		return err
	}
	claimSvc := svcs.Claim
	// Session boundary (ADR-020, S255): classifies every TCP request;
	// enforcing since S257. The Unix socket is not wrapped.
	srv.MountSessionBoundary(svcs.Auth, claimSvc, audit.New(st))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := startClaimToken(ctx, claimSvc, claimTokenFile, os.Stdout, os.Stderr); err != nil {
		return err
	}
	// First-run local discovery (S221): answer homestead.local plus
	// the service advertisement on the LAN, with live claim state so
	// owners spot unclaimed boxes. Discovery is convenience, never
	// boot-critical: failures only warn.
	startLocalDiscovery(ctx, claimSvc, listen, svcs.Names.MDNSLabels)
	// The optional `.lan` resolver (S295) runs only when the owner
	// turned it on; a taken port 53 warns and shows in the doctor.
	go svcs.Names.Run(ctx, func(err error) {
		fmt.Fprintln(os.Stderr, "homestead-server: .lan resolver off:", err)
	})
	// Both listeners share ctx and drain together. A socket failure is
	// fatal: silent loss of the recovery channel is worse than no start.
	// Daemon scheduler (S260, ADR-023): enqueues due work through the
	// operation runner and stops with the daemon. Its first pass is the
	// missed-run check.
	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		svcs.Scheduler.Run(ctx, func(err error) {
			fmt.Fprintln(os.Stderr, "homestead-server: scheduler:", err)
		})
	}()
	unixErr := make(chan error, 1)
	go func() {
		unixErr <- srv.RunUnix(ctx, socketPath)
	}()
	// The craft file socket (ADR-032) is not boot-critical: without it the
	// craft apps stay browser-local, so a failure only warns.
	craftDone := make(chan struct{})
	go func() {
		defer close(craftDone)
		if err := srv.RunCraftSocket(ctx, craftSocket); err != nil {
			fmt.Fprintln(os.Stderr, "homestead-server: craft file socket off:", err)
		}
	}()
	// The dashboard socket (ADR-035) is not boot-critical either: without
	// it only the local network address stops working.
	dashDone := make(chan struct{})
	go func() {
		defer close(dashDone)
		if err := srv.RunDashboardSocket(ctx, dashboardSocket); err != nil {
			fmt.Fprintln(os.Stderr, "homestead-server: dashboard socket off:", err)
		}
	}()
	// A fresh appliance gets the default dashboard route and a deployed
	// edge before claim (ADR-035); Docker may arrive later, so retry.
	ensureDone := make(chan struct{})
	go func() {
		defer close(ensureDone)
		svcs.DashboardAddress.RunEnsure(ctx, 5*time.Minute, func(err error) {
			fmt.Fprintln(os.Stderr, "homestead-server: dashboard address not ready, will retry:", err)
		})
		// Craft apps installed on names of their own move to their
		// paths once the edge is up (ADR-045 §5).
		server.RunCraftMigration(ctx, st, catalogDir, 5*time.Minute, func(err error) {
			fmt.Fprintln(os.Stderr, "homestead-server: craft apps not moved to their paths yet, will retry:", err)
		})
		// Published single-origin apps redirect their LAN names to the
		// public name (ADR-060); apps installed earlier take it here.
		server.RunSingleOriginMigration(ctx, st, catalogDir, 5*time.Minute, func(err error) {
			fmt.Fprintln(os.Stderr, "homestead-server: single-origin redirects not applied yet, will retry:", err)
		})
	}()
	// Bring local leaves in line with the routes once now (S295): a new
	// `.local` name is then served after an upgrade, not hours later at
	// the next renewal pass. Failures only warn; the pass retries.
	go func() {
		if _, err := svcs.LocalTLS.Reconcile(ctx, "system", "", false); err != nil {
			fmt.Fprintln(os.Stderr, "homestead-server: local TLS reconcile at start:", err)
		}
	}()
	// Keep the gated-app host block in line with the gates and the edge
	// network's subnet (S421); nft rules do not survive a reboot.
	go server.RunGateBlock(ctx, st, 5*time.Minute, func(err error) {
		fmt.Fprintln(os.Stderr, "homestead-server: gated-app host block:", err)
	})
	// Probe the router once now so the Tunnel page opens without the wait.
	go server.WarmTunnelDiscovery(ctx)
	if err := srv.Run(ctx); err != nil {
		return err
	}
	<-schedDone
	<-craftDone
	<-dashDone
	<-ensureDone
	return <-unixErr
}

// startLocalDiscovery serves mDNS answers in the background:
// homestead.local A records plus the service advertisement carrying
// live claim state and LAN addresses. A bad listen address or a
// taken mDNS port only warns on stderr — discovery must never fail
// the boot it ornaments.
func startLocalDiscovery(ctx context.Context, claimSvc *claim.Service, listen string, apps func() []string) {
	port, err := daemonPort(listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "homestead-server: local discovery off:", err)
		return
	}
	cfg := mdns.Config{
		Hostname: mdns.DefaultHostname,
		Port:     port,
		Version:  version.Version,
		Apps:     apps,
		State: func() string {
			state, err := claimSvc.CurrentState()
			if err != nil {
				return "unknown"
			}
			return state.String()
		},
		Addresses: func() []mdns.LinkAddr {
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

// startClaimToken keeps one live claim token in a root-only file while the
// appliance is unclaimed (S288; ADR-015 unchanged). The plaintext goes to
// the file only: the journal gets one line per start naming the command
// that shows it, so tokens no longer pile up in the system log. A
// background timer mints a fresh token when the current one expires.
func startClaimToken(ctx context.Context, svc *claim.Service, path string, stdout, stderr io.Writer) error {
	state, err := svc.CurrentState()
	if err != nil {
		return err
	}
	if state == claim.StateClaimed {
		return claim.RemoveTokenFile(path)
	}
	// First mint is synchronous so a bad runtime directory fails the boot.
	if _, err := svc.EnsureTokenFile(path, claim.TokenTTL); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "homestead-server: UNCLAIMED — run `sudo homesteadctl claim token` to show the claim token")
	go svc.MaintainTokenFile(ctx, path, claim.TokenTTL, func(err error) {
		if err == nil {
			fmt.Fprintln(stderr, "homestead-server: claim token file written again")
			return
		}
		fmt.Fprintln(stderr, "homestead-server: claim token:", err)
	})
	return nil
}
