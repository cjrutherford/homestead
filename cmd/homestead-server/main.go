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
	craftSocket := flag.String("craft-socket", docker.CraftSocketPath, "craft file socket the edge forwards to (S254, ADR-032)")
	catalogDir := flag.String("catalog-dir", "catalog", "curated package directory (S160; packaging installs it)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	if err := run(*listen, *configPath, *dataDir, *socketPath, *craftSocket, *catalogDir); err != nil {
		fmt.Fprintln(os.Stderr, "homestead-server:", err)
		os.Exit(1)
	}
}

func run(listen, configPath, dataDir, socketPath, craftSocket, catalogDir string) error {
	if err := server.ValidateCraftSocketPath(craftSocket); err != nil {
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
	if err := srv.Run(ctx); err != nil {
		return err
	}
	<-schedDone
	<-craftDone
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
