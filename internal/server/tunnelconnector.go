package server

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"homestead/internal/router"
	"homestead/internal/store"
)

// The connector follows the recorded tunnel routes (S290, ADR-041 §2): its
// config is rendered from them, written atomically, and the connector is
// restarted only when the file changed.

func connectorConfigPath() string { return filepath.Join(tunnelConnectorDir, "config.yml") }
func connectorTokenPath() string  { return filepath.Join(tunnelConnectorDir, "token.env") }

// recordedIngress is a tunnel's ingress from its recorded routes, in
// hostname order.
func recordedIngress(st *store.Store, tunnelID string) ([]router.Ingress, error) {
	routes, err := router.ListRoutes(st)
	if err != nil {
		return nil, err
	}
	ingress := []router.Ingress{}
	for _, r := range routes {
		if r.TunnelID == tunnelID {
			ingress = append(ingress, router.Ingress{Hostname: r.Hostname, Service: r.Service})
		}
	}
	return ingress, nil
}

// writeConnector writes the connector config for ingress, and the run
// token when one is given, each atomically. It reports whether either
// file changed.
func writeConnector(tunnelID string, ingress []router.Ingress, runToken string) (bool, error) {
	if err := os.MkdirAll(tunnelConnectorDir, 0o755); err != nil {
		return false, fmt.Errorf("tunnel: connector dir: %w", err)
	}
	changed, err := writeIfChanged(connectorConfigPath(), []byte(router.RenderConnectorIngress(tunnelID, ingress)), 0o644)
	if err != nil {
		return false, fmt.Errorf("tunnel: connector config: %w", err)
	}
	if runToken != "" {
		tokenChanged, err := writeIfChanged(connectorTokenPath(), []byte("TUNNEL_TOKEN="+runToken+"\n"), 0o600)
		if err != nil {
			return false, fmt.Errorf("tunnel: connector token: %w", err)
		}
		changed = changed || tokenChanged
	}
	return changed, nil
}

// writeIfChanged replaces path with data through a temporary file and a
// rename, unless it already holds exactly data.
func writeIfChanged(path string, data []byte, mode os.FileMode) (bool, error) {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return false, nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return false, err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}

// startConnector starts the connector when it is not running, and
// restarts it when its files changed; an unchanged running connector is
// left alone.
func startConnector(changed bool) error {
	if tunnelSystemctl("is-active", "--quiet", tunnelUnitName) != nil {
		if err := tunnelSystemctl("enable", "--now", tunnelUnitName); err != nil {
			return fmt.Errorf("tunnel: enable connector: %w (then run: sudo systemctl enable --now %s)", err, tunnelUnitName)
		}
		return nil
	}
	if !changed {
		return nil
	}
	if err := tunnelSystemctl("restart", tunnelUnitName); err != nil {
		return fmt.Errorf("tunnel: restart connector: %w (then run: sudo systemctl restart %s)", err, tunnelUnitName)
	}
	return nil
}

// syncConnector renders a tunnel's recorded routes into the connector and
// restarts it if that changed anything.
func syncConnector(st *store.Store, tunnelID string) error {
	ingress, err := recordedIngress(st, tunnelID)
	if err != nil {
		return err
	}
	changed, err := writeConnector(tunnelID, ingress, "")
	if err != nil {
		return err
	}
	return startConnector(changed)
}
