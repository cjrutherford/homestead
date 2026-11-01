package server

import (
	"context"
	"fmt"
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

// edgeSourceCIDR is the edge network's address range, the only source a
// gated app trusts identity headers from. A var so tests skip Docker.
var edgeSourceCIDR = func(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "network", "inspect", docker.NetworkEdge,
		"--format", "{{range .IPAM.Config}}{{.Subnet}} {{end}}").Output()
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", docker.NetworkEdge, err)
	}
	for _, f := range strings.Fields(string(out)) {
		if !strings.Contains(f, ":") { // IPv4 only
			return f, nil
		}
	}
	return "", fmt.Errorf("%s has no IPv4 subnet", docker.NetworkEdge)
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
	if cidr, err := edgeSourceCIDR(ctx); err == nil {
		in.EdgeSource = cidr
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
	err = outpostPing(ctx)
	switch {
	case timedOut(ctx):
		return []doctor.Check{unknownCheck(name, "the embedded outpost did not answer in time")}
	case err != nil:
		return []doctor.Check{{Name: name, Status: doctor.StatusFail,
			Detail: "Authentik's embedded outpost does not answer, so " + strings.Join(gated, ", ") + " cannot be reached (the gate fails closed): " + shortError(err),
			Fix: liveFix("Start Authentik's server container. To open the app again meanwhile, turn its Authentik gate off with its sign-in plan.",
				"/admin/catalog", "homesteadctl docker health")}}
	}
	return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "Authentik's embedded outpost answers for " + strings.Join(gated, ", ")}}
}
