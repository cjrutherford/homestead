package router

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"homestead/internal/store"
)

// RecordRoute stores a served hostname, replacing any earlier route
// for it: one hostname, one current tunnel. The route validates
// before persisting, so stored rows always decode cleanly.
func RecordRoute(st *store.Store, route TunnelRoute, tunnelName string) error {
	if st == nil {
		return fmt.Errorf("router: record route needs a store")
	}
	if err := route.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(
		`INSERT INTO tunnel_routes (hostname, tunnel_id, tunnel_name, service, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(hostname) DO UPDATE SET tunnel_id = excluded.tunnel_id,
		 tunnel_name = excluded.tunnel_name, service = excluded.service,
		 updated_at = excluded.updated_at`,
		route.Hostname, route.TunnelID, tunnelName, route.Service, now,
	); err != nil {
		return fmt.Errorf("router: record route: %w", err)
	}
	return nil
}

// DeleteRoute forgets a served hostname. Unknown hostnames delete to
// success: teardown converges instead of arguing about leftovers.
func DeleteRoute(st *store.Store, hostname string) error {
	if st == nil {
		return fmt.Errorf("router: delete route needs a store")
	}
	if _, err := st.DB().Exec(`DELETE FROM tunnel_routes WHERE hostname = ?`, hostname); err != nil {
		return fmt.Errorf("router: delete route: %w", err)
	}
	return nil
}

// ListRoutes returns recorded routes in hostname order. Empty reads
// as empty, never null.
func ListRoutes(st *store.Store) ([]TunnelRoute, error) {
	if st == nil {
		return nil, fmt.Errorf("router: list routes needs a store")
	}
	rows, err := st.DB().Query(
		`SELECT hostname, tunnel_id, service FROM tunnel_routes ORDER BY hostname`)
	if err != nil {
		return nil, fmt.Errorf("router: list routes: %w", err)
	}
	defer rows.Close()
	routes := []TunnelRoute{}
	for rows.Next() {
		var route TunnelRoute
		if err := rows.Scan(&route.Hostname, &route.TunnelID, &route.Service); err != nil {
			return nil, fmt.Errorf("router: scan route: %w", err)
		}
		if err := route.Validate(); err != nil {
			return nil, fmt.Errorf("router: stored route invalid: %w", err)
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("router: list routes: %w", err)
	}
	return routes, nil
}

// RecordedTunnel returns the one tunnel Homestead manages, from its
// recorded routes; empty when none is recorded.
func RecordedTunnel(st *store.Store) (id, name string, err error) {
	if st == nil {
		return "", "", fmt.Errorf("router: recorded tunnel needs a store")
	}
	err = st.DB().QueryRow(`SELECT tunnel_id, tunnel_name FROM tunnel_routes ORDER BY hostname LIMIT 1`).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("router: recorded tunnel: %w", err)
	}
	return id, name, nil
}
