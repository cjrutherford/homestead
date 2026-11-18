package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"homestead/internal/audit"
	"homestead/internal/debianupdate"
	"homestead/internal/layout"
	"homestead/internal/operation"
	"homestead/internal/platformapply"
	"homestead/internal/release"
	"homestead/internal/store"
)

// runApplyPlatform is `homestead-server apply-platform`, the body of the
// privileged oneshot unit homestead-apply.service (ADR-027). It applies
// the pending marker the daemon wrote: independent re-verification of the
// staged files, previous-binary preservation, atomic replacement, daemon
// restart, bounded health check, and finalization of the operation from
// outside the daemon. It listens on nothing and takes no network input.
func runApplyPlatform(args []string) error {
	fs := flag.NewFlagSet("apply-platform", flag.ContinueOnError)
	dataDir := fs.String("data-dir", layout.LibDir.Path, "operational state directory (S012)")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "how long the restarted daemon has to become healthy")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(*dataDir, store.DBFileName))
	if err != nil {
		return err
	}
	defer st.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	r := &platformapply.Runner{
		Releases: release.NewStore(st), Markers: platformapply.NewStore(st), Ops: operation.New(st), Audit: audit.New(st),
		Debian:  debianupdate.Manager{},
		Systemd: platformapply.ExecSystemd{}, Unit: "homestead-server.service", StagingDir: stagingDirFor(*dataDir),
		HealthTimeout: *healthTimeout, HealthInterval: time.Second, Probe: listenerProbe, RetryWait: 500 * time.Millisecond,
	}
	if err := r.Run(ctx); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "homestead-apply: platform update applied and healthy")
	return nil
}
