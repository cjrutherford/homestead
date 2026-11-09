package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/store"
)

// S479: the public side of the move (ADR-061) is safe to leave and to
// finish. The new address must answer through the public edge before the
// old name is removed, a move that stopped halfway shows as a leftover
// public name, and Doctor flags any public name no approved plan carries.

// Seams for tests: the live request and the wait between tries.
var (
	movePathProbe      = probePublicPath
	movePathProbeTries = 4
	movePathProbeWait  = 2 * time.Second
)

// probePublicPath asks the public edge for the moved app's address. Any
// answer other than "not found" or a server error counts: sign-in
// redirects and 401s are how a gated app answers. A 404 is what the edge
// says for an unpublished path (ADR-044), and 5xx is the tunnel or the app
// failing, so neither lets the old name go.
func probePublicPath(ctx context.Context, url string) error {
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s did not answer through the public edge: %w", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode >= 500 {
		return fmt.Errorf("%s answered %d through the public edge", url, resp.StatusCode)
	}
	return nil
}

// probeStep fails the run, before the old name is touched, unless the new
// public address answers.
func probeStep(plan appMovePlan) operation.StepDef {
	return operation.StepDef{Name: "probe-public", Run: func(ctx context.Context) error {
		var err error
		for i := 0; i < movePathProbeTries; i++ {
			if err = movePathProbe(ctx, plan.PublicTo); err == nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("%w; %s was not removed", err, plan.publicHost)
			case <-time.After(movePathProbeWait):
			}
		}
		return fmt.Errorf("%w; %s was not removed and still answers, so the move can be finished or retried once the new address answers", err, plan.publicHost)
	}}
}

// publicLeftover is a public name Homestead still holds (a connector entry
// or a DNS record it created) that no edge route carries any more.
type publicLeftover struct {
	Host      string
	TunnelID  string
	Connector bool
	DNS       bool
}

// leftoverPublicNames reads local state only: tunnel entries and owned DNS
// records whose name is neither the dashboard's nor any route's public
// name. An approved plan is what puts a name on a route, so a name no
// route carries has none behind it.
func leftoverPublicNames(st *store.Store, routes []docker.Route) ([]publicLeftover, error) {
	carried := map[string]bool{}
	for _, r := range routes {
		for _, n := range r.Tunnel {
			carried[strings.ToLower(string(n))] = true
		}
	}
	found := map[string]*publicLeftover{}
	get := func(host, tunnel string) *publicLeftover {
		key := strings.ToLower(host)
		if found[key] == nil {
			found[key] = &publicLeftover{Host: host, TunnelID: tunnel}
		}
		return found[key]
	}
	entries, err := router.ListRoutes(st)
	if err != nil {
		return nil, err
	}
	tunnels := map[string]bool{}
	for _, e := range entries {
		tunnels[e.TunnelID] = true
		if !carried[strings.ToLower(e.Hostname)] {
			get(e.Hostname, e.TunnelID).Connector = true
		}
	}
	if id, _, err := router.RecordedTunnel(st); err == nil && id != "" {
		tunnels[id] = true
	}
	for id := range tunnels {
		owned, err := router.OwnedRecords(st, id)
		if err != nil {
			return nil, err
		}
		for _, rec := range owned {
			if !carried[strings.ToLower(rec.Hostname)] {
				get(rec.Hostname, id).DNS = true
			}
		}
	}
	out := make([]publicLeftover, 0, len(found))
	for _, l := range found {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

// movePending is the public name a move is removing, kept from the start
// of the move until the name is gone.
type movePending struct {
	App, Host, TunnelID string
}

func setMovePending(st *store.Store, p movePending) error {
	_, err := st.DB().Exec(`INSERT INTO app_move_pending (app, hostname, tunnel_id, started_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(app) DO UPDATE SET hostname = excluded.hostname, tunnel_id = excluded.tunnel_id, started_at = excluded.started_at`,
		p.App, p.Host, p.TunnelID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func clearMovePending(st *store.Store, app string) error {
	_, err := st.DB().Exec(`DELETE FROM app_move_pending WHERE app = ?`, app)
	return err
}

func listMovePending(st *store.Store) ([]movePending, error) {
	rows, err := st.DB().Query(`SELECT app, hostname, tunnel_id FROM app_move_pending ORDER BY app`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []movePending
	for rows.Next() {
		var p movePending
		if err := rows.Scan(&p.App, &p.Host, &p.TunnelID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func getMovePending(st *store.Store, app string) (movePending, bool, error) {
	all, err := listMovePending(st)
	if err != nil {
		return movePending{}, false, err
	}
	for _, p := range all {
		if p.App == app {
			return p, true, nil
		}
	}
	return movePending{}, false, nil
}

// buildFinishPlan is the plan that completes a move which stopped before
// the old public name was removed: no setting of the app changes. The new
// address is checked first and the old name goes only if it answers.
func (m *appMove) buildFinishPlan(f moveFacts) (appMovePlan, error) {
	mf := f.manifest
	if _, err := tunnelToken(m.secrets); err != nil {
		return appMovePlan{}, refusePlan(http.StatusUnprocessableEntity, "%v", err)
	}
	plan := appMovePlan{
		App: mf.Name, Route: string(f.route.Host), Path: mf.AppPath(), To: f.toURLs(), StripPrefix: f.route.StripPrefix,
		Redirects: []string{}, AppSettings: []string{}, SignIn: []string{}, Links: []string{}, Warnings: []string{},
		Removes: []string{}, Stops: []string{}, manifest: mf, names: f.names, Finish: true, publish: true,
		publicHost: f.publicHost, tunnelID: f.tunnelID,
	}
	plan.From = "https://" + f.publicHost + "/"
	plan.PublicFrom, plan.PublicTo = f.publicFromTo()
	plan.Stops = append(plan.Stops, plan.From)
	plan.Removes = append(plan.Removes,
		fmt.Sprintf("Check: %s must answer through the public edge, or nothing is removed", plan.PublicTo),
		fmt.Sprintf("Tunnel: the ingress entry for %s is removed from the connector, if it is still there", f.publicHost),
		fmt.Sprintf("DNS: the proxied CNAME %s Homestead created is deleted, only while it still points at the tunnel", f.publicHost))
	plan.Warnings = append(plan.Warnings,
		"An earlier move of "+mf.Title+" stopped before its old public name was removed. This finishes it; the app's own settings and sign-in are not changed.",
		moveOldNameStops(mf.Title, plan.From, plan.PublicTo))
	plan.Digest = plan.digest()
	return plan, nil
}
