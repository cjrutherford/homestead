package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/platformapply"
	"homestead/internal/release"
	"homestead/internal/store"
	"homestead/internal/version"
)

// probeTimeout bounds how long finalization waits for the listeners; a
// new binary that cannot serve within it fails its health check.
const probeTimeout = 60 * time.Second

// finalizePlatformApply runs the post-restart health check for a
// platform update left pending by the previous process (S265). With no
// marker it returns at once. Failure is reported on stderr (journal);
// the operation record, audit trail, and failure message carry the
// manual restore command.
func finalizePlatformApply(ctx context.Context, st *store.Store, listen, socketPath string) {
	markers := platformapply.NewStore(st)
	if _, ok, err := markers.Pending(); err != nil || !ok {
		if err != nil {
			fmt.Fprintln(os.Stderr, "homestead-server: platform apply marker:", err)
		}
		return
	}
	f := &platformapply.Finalizer{
		Releases: release.NewStore(st), Markers: markers, Ops: operation.New(st), Audit: audit.New(st),
		Version: version.Version, Unit: "homestead-server.service",
		StagingDir: stagingDir(), Probe: selfProbe(listen, socketPath),
	}
	done, err := f.Run(ctx)
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "homestead-server: platform update failed:", err)
	case done:
		fmt.Fprintln(os.Stderr, "homestead-server: platform update finalized; running", version.Version)
	}
}

// selfProbe waits for the TCP listener to answer /healthz with this
// process's version and the recovery socket to accept connections.
func selfProbe(listen, socketPath string) func(context.Context) error {
	return func(ctx context.Context) error {
		url, err := probeURL(listen)
		if err != nil {
			return err
		}
		deadline := time.Now().Add(probeTimeout)
		var last error
		for {
			if last = probeOnce(ctx, url, socketPath); last == nil {
				return nil
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				return last
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

func probeOnce(ctx context.Context, url, socketPath string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
		return fmt.Errorf("healthz answered %d", resp.StatusCode)
	}
	if body.Version != version.Version {
		return fmt.Errorf("healthz reports version %q, want %q", body.Version, version.Version)
	}
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("recovery socket: %w", err)
	}
	return conn.Close()
}

// probeURL turns the listen address into a loopback probe URL.
func probeURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", err
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}
