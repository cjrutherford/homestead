// Probes used by the privileged apply unit (ADR-027).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"homestead/internal/platformapply"
)

// listenerProbe makes one attempt to prove the daemon the apply unit just
// restarted serves: /healthz on the marker's listen address answers with
// the target version, and the recovery socket accepts connections.
func listenerProbe(ctx context.Context, m platformapply.Marker) error {
	url, err := probeURL(m.Listen)
	if err != nil {
		return err
	}
	return probeOnce(ctx, url, m.SocketPath, m.ToVersion)
}

func probeOnce(ctx context.Context, url, socketPath, wantVersion string) error {
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
	if body.Version != wantVersion {
		return fmt.Errorf("healthz reports version %q, want %q", body.Version, wantVersion)
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
