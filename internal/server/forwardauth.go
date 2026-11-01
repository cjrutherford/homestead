package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/store"
)

// Forward-auth glue (S418, ADR-055): what the server adds to the app
// sign-in plan for a gated app, the gate's edge reconcile, and the
// doctor check that the embedded outpost answers.

// edgeSourceAddr is the edge container's own address on the edge network
// as a /32, the only source a gated app trusts identity headers from. The
// whole subnet would include the bridge gateway, which is how the host
// reaches containers, so any local user could forge the header (ADR-055
// §5, 2026-11-01). A var so tests skip Docker.
var edgeSourceAddr = func(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format",
		"{{.State.Running}} {{json .NetworkSettings.Networks}}", docker.CaddyContainerName).Output()
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", docker.CaddyContainerName, err)
	}
	return parseEdgeAddr(string(out))
}

// parseEdgeAddr reads `docker inspect` output of the form
// "<running> <networks json>" into the edge container's IPv4 /32.
func parseEdgeAddr(out string) (string, error) {
	running, networks, _ := strings.Cut(strings.TrimSpace(out), " ")
	if running != "true" {
		return "", fmt.Errorf("the edge container %s is not running; start the edge and plan again", docker.CaddyContainerName)
	}
	var nets map[string]struct{ IPAddress string }
	if err := json.Unmarshal([]byte(networks), &nets); err != nil {
		return "", fmt.Errorf("read %s networks: %w", docker.CaddyContainerName, err)
	}
	ip := net.ParseIP(nets[docker.NetworkEdge].IPAddress)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("the edge container %s has no IPv4 address on %s", docker.CaddyContainerName, docker.NetworkEdge)
	}
	return ip.To4().String() + "/32", nil
}

// outpostPing asks the embedded outpost, from inside the edge container
// and over the Docker network, whether it answers. A var for tests.
var outpostPing = func(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	url := "http://" + docker.OutpostUpstream + docker.OutpostPrefix + "ping"
	if out, err := exec.CommandContext(ctx, "docker", "exec", docker.CaddyContainerName,
		"wget", "-q", "-O", "/dev/null", "-T", "8", url).CombinedOutput(); err != nil {
		return fmt.Errorf("the embedded outpost does not answer: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// addForwardInputs fills the inputs only a gated app needs.
func addForwardInputs(st *store.Store, in *appsso.Inputs, manifest catalog.Manifest) error {
	fa := manifest.SSO.ForwardAuth
	if fa == nil || !in.Installed {
		return nil
	}
	if env, err := catalog.OverrideEnv(stackDirFor(manifest.Name), fa.Service); err == nil {
		in.OwnerEnv = env
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if addr, err := edgeSourceAddr(ctx); err == nil {
		in.EdgeSource = addr
	}
	if fa.RequiresHostBlock {
		in.HostBlockRequired = true
		in.HostBlockProblem = hostBlockProblem()
	}
	rows, err := st.DB().Query(`SELECT username FROM people WHERE state = 'active'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		in.PeopleUsernames = append(in.PeopleUsernames, name)
	}
	in.AccountsFn = func() *appsso.AppAccounts {
		a := appsso.ReadAccounts(context.Background(), manifest)
		return &a
	}
	return rows.Err()
}

// setGate puts the gate on the app's edge routes (nil takes it off), then
// reconciles the edge and, with a gate on, checks the outpost answers. It
// is the appsso.Runtime.SetGate of the sign-in operation.
func (a *appSignIn) setGate(ctx context.Context, m catalog.Manifest, gate *docker.Gate) error {
	set, err := loadEdgeRoutes(a.cfg.Store)
	if err != nil {
		return err
	}
	routes := append([]docker.Route(nil), set.Routes...)
	changed := 0
	for i, r := range routes {
		if r.Dashboard || r.IsPath() {
			continue
		}
		mine := false
		for _, app := range appsso.AppRoutes(m, []docker.Route{r}) {
			mine = mine || app.Host == r.Host
		}
		if !mine {
			continue
		}
		if gate == nil {
			r.Gate = nil
		} else {
			g := *gate
			r.Gate = &g
		}
		routes[i] = r
		changed++
	}
	if gate != nil && changed == 0 {
		return fmt.Errorf("%s has no route to gate", m.Title)
	}
	set.Routes = routes
	if err := docker.ValidateRenderable(set); err != nil {
		return err
	}
	if err := reconcileEdgeSet(ctx, a.cfg.Store, set); err != nil {
		return err
	}
	// The host block follows the gates (S421). An app that requires it
	// must not be left gated without it; for the others the Doctor warns.
	if err := reconcileGateBlock(ctx, a.cfg.Store); err != nil && requiresHostBlock(m) && gate != nil {
		return fmt.Errorf("the host block that keeps other users off %s's port is not in place: %w", m.Title, err)
	}
	if gate != nil {
		return outpostPing(ctx)
	}
	return nil
}

// gatedAppChecks is the doctor check that the embedded outpost answers
// while any gate is on: without it every gated app is unreachable (it
// fails closed, ADR-055 §9).
func gatedAppChecks(ctx context.Context, c DoctorLiveConfig) []doctor.Check {
	const name = "authentik-outpost"
	set, err := loadEdgeRoutes(c.Store)
	if err != nil {
		return []doctor.Check{unknownCheck(name, "the edge routes could not be read")}
	}
	var gated []string
	for _, r := range set.Routes {
		if r.Gate != nil {
			gated = append(gated, string(r.Gate.Canonical))
		}
	}
	if len(gated) == 0 {
		return nil
	}
	drift := gatedTrustDrift(c)
	err = outpostPing(ctx)
	switch {
	case timedOut(ctx):
		return append([]doctor.Check{unknownCheck(name, "the embedded outpost did not answer in time")}, drift...)
	case err != nil:
		return append([]doctor.Check{{Name: name, Status: doctor.StatusFail,
			Detail: "Authentik's embedded outpost does not answer, so " + strings.Join(gated, ", ") + " cannot be reached (the gate fails closed): " + shortError(err),
			Fix: liveFix("Start Authentik's server container. To open the app again meanwhile, turn its Authentik gate off with its sign-in plan.",
				"/admin/catalog", "homesteadctl docker health")}}, drift...)
	}
	return append([]doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "Authentik's embedded outpost answers for " + strings.Join(gated, ", ")}}, drift...)
}

// gatedTrustDrift warns for each signed-in gated app whose plan no longer
// matches what was applied, typically because the edge container was
// recreated and got a new address. The app fails safe (it ignores the
// header and shows its own login); nothing is rewired automatically.
func gatedTrustDrift(c DoctorLiveConfig) []doctor.Check {
	if c.SSO == nil {
		return nil
	}
	entries, err := catalog.Load(c.CatalogDir)
	if err != nil {
		return nil
	}
	set, err := loadEdgeRoutes(c.Store)
	if err != nil {
		return nil
	}
	var out []doctor.Check
	for _, name := range entries.Names() {
		m, err := entries.Get(name)
		if err != nil || m.SSO.ForwardAuth == nil {
			continue
		}
		in, err := appSignInInputsFrom(c.Store, c.SSO, m, set)
		if err != nil || in.Applied == nil || !appsso.BuildStatus(in, false).Replan {
			continue
		}
		out = append(out, doctor.Check{Name: "authentik-gate-" + m.Name, Status: doctor.StatusWarn,
			Detail: m.Title + " trusts an edge address that is out of date (the edge was recreated), so it shows its own login page until sign-in is re-applied.",
			Fix:    liveFix("Re-plan and apply sign-in for "+m.Name+".", "/admin/catalog", "homesteadctl apps sign-in plan "+m.Name)})
	}
	return out
}
