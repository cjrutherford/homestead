package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/router"
	"homestead/internal/secret"
	"homestead/internal/sso"
	"homestead/internal/store"
)

// The Doctor's live probes (S316). Each reads and never acts: it asks a
// service, Cloudflare, Authentik or an app a question and reports the
// answer in the owner's words, with the fix as text, a page and a command
// to run. They run only for GET /api/v1/doctor?live=1, each bounded by
// its own timeout inside the report's budget (doctor.RunLive). No detail
// carries a token, key or password.

// connectorMetricsURLs are where cloudflared serves its metrics and
// readiness: it binds the first free port from 20241 to 20245 on
// 127.0.0.1 (Cloudflare tunnel metrics documentation). A seam for tests.
var connectorMetricsURLs = func() []string {
	urls := []string{}
	for port := 20241; port <= 20245; port++ {
		urls = append(urls, "http://127.0.0.1:"+strconv.Itoa(port))
	}
	return urls
}

// DoctorLiveConfig wires the probes.
type DoctorLiveConfig struct {
	Store      *store.Store
	Secrets    *secret.Store
	SSO        *sso.Manager
	Names      *LocalNames
	Local      *cert.LocalTLS
	CatalogDir string
}

func liveFix(text, link, command string) *doctor.Fix {
	return &doctor.Fix{Text: text, Link: link, Command: command}
}

func unknownCheck(name, detail string) doctor.Check {
	return doctor.Check{Name: name, Status: doctor.StatusUnknown, Detail: detail}
}

// timedOut is true when a probe's own context ended: its answers are
// then unknown, not failures.
func timedOut(ctx context.Context) bool { return ctx.Err() != nil }

// doctorProbes builds the probes for one request. Installed apps are read
// when it runs, so a new app is covered at once.
func doctorLiveProbes(c DoctorLiveConfig) func() []doctor.Probe {
	return func() []doctor.Probe {
		probes := []doctor.Probe{
			{Name: "tunnel-connector", Group: doctor.GroupTunnel, Run: func(ctx context.Context) []doctor.Check { return probeTunnelConnector(ctx, c) }},
			{Name: "tunnel-exists", Group: doctor.GroupTunnel, Timeout: 8 * time.Second, Run: func(ctx context.Context) []doctor.Check { return probeTunnelExists(ctx, c) }},
			{Name: "tunnel-dns", Group: doctor.GroupTunnel, Timeout: 10 * time.Second, Run: func(ctx context.Context) []doctor.Check { return probeTunnelDNS(ctx, c) }},
			{Name: "edge-certificate", Group: doctor.GroupCertificates, Timeout: 10 * time.Second, Run: func(ctx context.Context) []doctor.Check { return probeEdgeCertificates(ctx, c) }},
			{Name: "authentik", Group: doctor.GroupSignIn, Run: func(ctx context.Context) []doctor.Check { return probeAuthentik(ctx, c) }},
			{Name: "authentik-outpost", Group: doctor.GroupSignIn, Timeout: 12 * time.Second, Run: func(ctx context.Context) []doctor.Check { return gatedAppChecks(ctx, c) }},
			{Name: "gated-app-host-block", Group: doctor.GroupSignIn, Timeout: 12 * time.Second, Run: func(ctx context.Context) []doctor.Check { return gateBlockChecks(ctx, c) }},
		}
		entries, err := catalog.Load(c.CatalogDir)
		if err != nil {
			return probes
		}
		lister := &lazyContainers{}
		for _, name := range entries.Names() {
			if installed, _ := catalog.Installed(c.Store, name, stackDirFor(name)); !installed {
				continue
			}
			manifest, err := entries.Get(name)
			if err != nil {
				continue
			}
			probes = append(probes,
				doctor.Probe{Name: "app-containers:" + name, Group: doctor.GroupApps, Run: func(ctx context.Context) []doctor.Check {
					return probeAppContainers(ctx, lister, manifest)
				}},
				doctor.Probe{Name: "app-reach:" + name, Group: doctor.GroupApps, Timeout: 12 * time.Second, Run: func(ctx context.Context) []doctor.Check {
					return probeAppReach(ctx, c, manifest)
				}},
			)
			if manifest.SSO.OIDC != nil && manifest.SSO.OIDC.Adapter == catalog.AdapterNextcloud {
				probes = append(probes, doctor.Probe{Name: "app-setup:" + name, Group: doctor.GroupApps, Run: func(ctx context.Context) []doctor.Check {
					return probeAppSetUp(ctx, manifest)
				}})
			}
		}
		return probes
	}
}

// ---- tunnel ----

var readyConnectionsPattern = regexp.MustCompile(`(?m)^cloudflared_tunnel_ha_connections\s+([0-9.]+)\s*$`)

// connectorConnections asks cloudflared how many edge connections it
// holds: /ready answers {"readyConnections": n} (200 when n > 0, else
// 503), and the metric cloudflared_tunnel_ha_connections is the fallback.
func connectorConnections(ctx context.Context) (int, bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	get := func(url string) (int, []byte, bool) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, nil, false
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, false
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, body, true
	}
	for _, base := range connectorMetricsURLs() {
		if _, body, ok := get(base + "/ready"); ok {
			var ready struct {
				ReadyConnections *int `json:"readyConnections"`
			}
			if json.Unmarshal(body, &ready) == nil && ready.ReadyConnections != nil {
				return *ready.ReadyConnections, true
			}
		}
		if code, body, ok := get(base + "/metrics"); ok && code == http.StatusOK {
			if m := readyConnectionsPattern.FindSubmatch(body); m != nil {
				if f, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
					return int(f), true
				}
			}
		}
	}
	return 0, false
}

func probeTunnelConnector(ctx context.Context, c DoctorLiveConfig) []doctor.Check {
	const name = "tunnel-connector"
	routes, err := router.ListRoutes(c.Store)
	if err != nil {
		return []doctor.Check{unknownCheck(name, "the tunnel routes could not be read: "+err.Error())}
	}
	if len(routes) == 0 {
		return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "no tunnel is set up"}}
	}
	if err := tunnelSystemctl("is-active", "--quiet", tunnelUnitName); err != nil {
		return []doctor.Check{{Name: name, Status: doctor.StatusFail,
			Detail: "the connector service (" + tunnelUnitName + ") is not running, so no public name reaches the appliance",
			Fix:    liveFix("Start the connector. If it stops again, read its log.", "/admin/tunnel", "sudo journalctl -u "+tunnelUnitName+" -n 50")}}
	}
	n, ok := connectorConnections(ctx)
	switch {
	case timedOut(ctx):
		return []doctor.Check{unknownCheck(name, "the connector did not answer in time")}
	case !ok:
		return []doctor.Check{{Name: name, Status: doctor.StatusUnknown,
			Detail: "the connector service is running, but its metrics did not answer on 127.0.0.1 (ports 20241 to 20245)"}}
	case n == 0:
		return []doctor.Check{{Name: name, Status: doctor.StatusFail,
			Detail: "the connector is running but holds no connection to Cloudflare",
			Fix:    liveFix("Check that the appliance reaches the internet and that the tunnel still exists in Cloudflare.", "/admin/tunnel", "sudo journalctl -u "+tunnelUnitName+" -n 50")}}
	default:
		return []doctor.Check{{Name: name, Status: doctor.StatusOK,
			Detail: fmt.Sprintf("the connector is running with %d connection(s) to Cloudflare", n)}}
	}
}

// tunnelFacts reads what the Cloudflare probes need; ok false carries the
// unknown check to return.
func tunnelCloudflare(c DoctorLiveConfig, name string) (routes []router.TunnelRoute, token string, client *router.TunnelClient, skip *doctor.Check) {
	routes, err := router.ListRoutes(c.Store)
	if err != nil {
		u := unknownCheck(name, "the tunnel routes could not be read: "+err.Error())
		return nil, "", nil, &u
	}
	if len(routes) == 0 {
		return nil, "", nil, &doctor.Check{Name: name, Status: doctor.StatusOK, Detail: "no tunnel is set up"}
	}
	token, err = tunnelToken(c.Secrets)
	if err != nil {
		u := unknownCheck(name, "no Cloudflare token is kept, so Cloudflare was not asked")
		return nil, "", nil, &u
	}
	client, err = tunnelClient()
	if err != nil {
		u := unknownCheck(name, "the Cloudflare client could not be made")
		return nil, "", nil, &u
	}
	return routes, token, client, nil
}

func probeTunnelExists(ctx context.Context, c DoctorLiveConfig) []doctor.Check {
	const name = "tunnel-exists"
	routes, token, client, skip := tunnelCloudflare(c, name)
	if skip != nil {
		return []doctor.Check{*skip}
	}
	var out []doctor.Check
	seen := map[string]bool{}
	for _, route := range routes {
		if seen[route.TunnelID] {
			continue
		}
		seen[route.TunnelID] = true
		account, err := router.TunnelAccount(c.Store, route.TunnelID)
		if err != nil || account == "" {
			accounts, lerr := client.ListAccounts(ctx, token)
			if lerr != nil || len(accounts) != 1 {
				out = append(out, unknownCheck(name, "the Cloudflare account of the tunnel is not known"))
				continue
			}
			account = accounts[0]
		}
		status, err := client.GetTunnel(ctx, token, account, route.TunnelID)
		var apiErr *router.TunnelError
		switch {
		case timedOut(ctx):
			return []doctor.Check{unknownCheck(name, "Cloudflare did not answer in time")}
		case errors.As(err, &apiErr) && (apiErr.Code == http.StatusNotFound || apiErr.Code == 1003):
			out = append(out, doctor.Check{Name: name, Status: doctor.StatusFail,
				Detail: "the tunnel no longer exists in Cloudflare, so no public name works",
				Fix:    liveFix("Plan the tunnel again; a name that is already taken is explained in the plan.", "/admin/tunnel", "homesteadctl tunnel plan")})
		case err != nil:
			out = append(out, unknownCheck(name, "Cloudflare could not be asked: "+err.Error()))
		case status.Status == "healthy" || status.Status == "degraded" && len(status.Connections) > 0:
			out = append(out, doctor.Check{Name: name, Status: doctor.StatusOK,
				Detail: fmt.Sprintf("the tunnel %q exists and Cloudflare sees %d connection(s) (%s)", status.Name, len(status.Connections), status.Status)})
		default:
			out = append(out, doctor.Check{Name: name, Status: doctor.StatusWarn,
				Detail: fmt.Sprintf("the tunnel %q exists, but Cloudflare reports it %s", status.Name, status.Status),
				Fix:    liveFix("Check that the connector is running and connected.", "/admin/tunnel", "sudo systemctl status "+tunnelUnitName)})
		}
	}
	return out
}

func probeTunnelDNS(ctx context.Context, c DoctorLiveConfig) []doctor.Check {
	const name = "tunnel-dns"
	routes, token, client, skip := tunnelCloudflare(c, name)
	if skip != nil {
		return []doctor.Check{*skip}
	}
	var out []doctor.Check
	zones := map[string]router.Zone{}
	for _, route := range routes {
		check := doctor.Check{Name: name + ":" + route.Hostname}
		parts := strings.Split(route.Hostname, ".")
		var zone router.Zone
		for i := 0; i+2 <= len(parts) && zone.ID == ""; i++ {
			zone = zones[strings.Join(parts[i:], ".")]
		}
		if zone.ID == "" {
			z, err := client.ZoneFor(ctx, token, route.Hostname)
			if timedOut(ctx) {
				break
			}
			if err != nil {
				check.Status, check.Detail = doctor.StatusUnknown, "the zone of "+route.Hostname+" could not be found with the Cloudflare token"
				out = append(out, check)
				continue
			}
			zone = z
			zones[z.Name] = z
		}
		records, err := client.RecordsAt(ctx, token, zone.ID, route.Hostname)
		if timedOut(ctx) {
			break
		}
		if err != nil {
			check.Status, check.Detail = doctor.StatusUnknown, "the DNS records of "+route.Hostname+" could not be read"
			out = append(out, check)
			continue
		}
		want := router.TunnelTarget(route.TunnelID)
		switch found := firstCNAME(records); {
		case found == nil:
			check.Status = doctor.StatusFail
			check.Detail = route.Hostname + " has no DNS record pointing at the tunnel"
			check.Fix = liveFix("Plan the tunnel again to create the record.", "/admin/tunnel", "homesteadctl tunnel plan")
		case !strings.EqualFold(found.Content, want):
			check.Status = doctor.StatusFail
			check.Detail = route.Hostname + " points at " + found.Content + ", not at this tunnel (" + want + ")"
			check.Fix = liveFix("Another tunnel or service owns the name. Plan again to see what would change; nothing is replaced until you approve.", "/admin/tunnel", "homesteadctl tunnel plan")
		case !found.Proxied:
			check.Status = doctor.StatusWarn
			check.Detail = route.Hostname + " points at the tunnel but is not proxied by Cloudflare"
			check.Fix = liveFix("Turn the proxy on for the record in Cloudflare.", "/admin/tunnel", "")
		default:
			check.Status = doctor.StatusOK
			check.Detail = route.Hostname + " points at the tunnel"
		}
		out = append(out, check)
	}
	if timedOut(ctx) {
		return []doctor.Check{unknownCheck(name, "Cloudflare did not answer in time")}
	}
	return out
}

func firstCNAME(records []router.DNSRecord) *router.DNSRecord {
	for i := range records {
		if strings.EqualFold(records[i].Type, "CNAME") {
			return &records[i]
		}
	}
	return nil
}

func probeEdgeCertificates(ctx context.Context, c DoctorLiveConfig) []doctor.Check {
	const name = "edge-certificate"
	routes, err := router.ListRoutes(c.Store)
	if err != nil {
		return []doctor.Check{unknownCheck(name, "the tunnel routes could not be read: "+err.Error())}
	}
	if len(routes) == 0 {
		return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "no public names"}}
	}
	if _, err := tunnelToken(c.Secrets); err != nil {
		return []doctor.Check{unknownCheck(name, "no Cloudflare token is kept, so certificate coverage was not read")}
	}
	hosts := []string{}
	for _, r := range routes {
		hosts = append(hosts, r.Hostname)
	}
	coverage := statusCoverage(ctx, c.Secrets, hosts)
	if timedOut(ctx) || len(coverage) == 0 {
		return []doctor.Check{unknownCheck(name, "Cloudflare did not say which certificates cover the public names")}
	}
	var out []doctor.Check
	for _, cov := range coverage {
		check := doctor.Check{Name: name + ":" + cov.Hostname}
		switch {
		case cov.Covered:
			check.Status = doctor.StatusOK
			check.Detail = cov.Hostname + " is covered by Cloudflare's " + cov.By + " certificate"
		default:
			check.Status = doctor.StatusWarn
			check.Detail = router.UncoveredText(cov.Hostname, zoneGuess(c.Store, cov.Hostname))
			check.Fix = liveFix("Add a certificate for it in Cloudflare (Advanced Certificate Manager), or publish a name one level below your domain.", "/admin/tunnel", "")
		}
		out = append(out, check)
	}
	return out
}

// ---- sign-in ----

func probeAuthentik(ctx context.Context, c DoctorLiveConfig) []doctor.Check {
	const name = "authentik"
	if c.SSO == nil {
		return nil
	}
	base := ""
	if spec, ok, err := c.SSO.Config(); err == nil && ok {
		base = spec.AuthentikURL
	} else if authentikInstalled(c.Store) && c.SSO.Defaults != nil {
		base = c.SSO.Defaults().AuthentikURL
	}
	if base == "" {
		if !authentikInstalled(c.Store) {
			return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "Authentik is not installed"}}
		}
		return []doctor.Check{unknownCheck(name, "Authentik's address is not known yet")}
	}
	url := strings.TrimRight(base, "/") + "/-/health/live/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return []doctor.Check{unknownCheck(name, "Authentik's address is not valid")}
	}
	client := *c.SSO.Client()
	client.CheckRedirect = noRedirect
	resp, err := client.Do(req)
	switch {
	case timedOut(ctx):
		return []doctor.Check{unknownCheck(name, "Authentik did not answer in time")}
	case err != nil:
		return []doctor.Check{{Name: name, Status: doctor.StatusFail,
			Detail: "Authentik does not answer at " + base + ": " + shortError(err),
			Fix:    liveFix("Start Authentik's containers; sign-in works again once it answers.", "/admin/catalog", "homesteadctl docker health")}}
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode >= 500:
		return []doctor.Check{{Name: name, Status: doctor.StatusFail,
			Detail: fmt.Sprintf("Authentik answers at %s but is not healthy (%d)", base, resp.StatusCode),
			Fix:    liveFix("Wait a minute if it just started. If it stays, restart Authentik.", "/admin/catalog", "homesteadctl docker health")}}
	case resp.StatusCode == http.StatusNotFound:
		return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
			Detail: "something answers at " + base + ", but not as Authentik",
			Fix:    liveFix("Check the Authentik route on the Edge page.", "/admin/edge", "")}}
	default:
		return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "Authentik answers at " + base}}
	}
}

// shortError keeps a transport error to its last clause, which names the
// cause without the request URL.
func shortError(err error) string {
	text := err.Error()
	if i := strings.LastIndex(text, ": "); i >= 0 {
		text = text[i+2:]
	}
	return text
}

// signInDoctorChecks reads the dashboard sign-in plan from local state.
func signInDoctorChecks(m *sso.Manager) func() []doctor.Check {
	return func() []doctor.Check {
		const name = "sign-in"
		st, err := m.Status()
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "sign-in state could not be read: " + err.Error()}}
		}
		switch {
		case st.Dormant:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "dashboard sign-in is set up, but Authentik is not installed"}}
		case st.Offered:
			return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "dashboard sign-in with Authentik is applied and linked"}}
		case st.Configured:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "dashboard sign-in is applied, but no owner is linked yet"}}
		case st.Plan != nil:
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "a dashboard sign-in plan is waiting; it is not applied"}}
		default:
			return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "dashboard sign-in with Authentik is not set up (optional)"}}
		}
	}
}

// appAccessDoctorChecks warns for each installed app whose sign-in is open
// to every Authentik user (S314). It reads local records only.
func appAccessDoctorChecks(st *store.Store, catalogDir string) func() []doctor.Check {
	return func() []doctor.Check {
		entries, err := catalog.Load(catalogDir)
		if err != nil {
			return nil
		}
		var out []doctor.Check
		for _, name := range entries.Names() {
			if installed, _ := catalog.Installed(st, name, stackDirFor(name)); !installed {
				continue
			}
			rec, ok, err := (appsso.Records{DB: st.DB()}).Get(name)
			if err != nil || !ok {
				continue
			}
			check := doctor.Check{Name: "app-access:" + name, Status: doctor.StatusOK, Detail: name + " sign-in is limited to its Authentik groups"}
			if rec.Access == nil {
				check.Status = doctor.StatusWarn
				check.Detail = name + " is open to every Authentik user"
			}
			out = append(out, check)
		}
		return out
	}
}

// ---- apps ----

type lazyContainers struct {
	mu   sync.Mutex
	done bool
	list []docker.Container
	err  error
}

func (l *lazyContainers) get(ctx context.Context) ([]docker.Container, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.done {
		l.list, l.err = migrationContainers(ctx)
		l.done = true
	}
	return l.list, l.err
}

func probeAppContainers(ctx context.Context, l *lazyContainers, m catalog.Manifest) []doctor.Check {
	name := "app-containers:" + m.Name
	all, err := l.get(ctx)
	if timedOut(ctx) {
		return []doctor.Check{unknownCheck(name, "the container engine did not answer in time")}
	}
	if err != nil {
		return []doctor.Check{unknownCheck(name, "the container engine could not be asked")}
	}
	mine := catalog.ProjectContainers(all, m.Name)
	var problems, notes []string
	worst := doctor.StatusOK
	raise := func(level string) {
		if level == doctor.StatusFail || worst == doctor.StatusOK {
			worst = level
		}
	}
	for _, svc := range m.Services {
		var found *docker.Container
		for i := range mine {
			if mine[i].Labels[docker.LabelService] == svc.Name {
				found = &mine[i]
			}
		}
		switch {
		case found == nil:
			problems = append(problems, svc.Name+" does not exist")
			raise(doctor.StatusFail)
		case found.State == "running" && strings.Contains(found.Status, "unhealthy"):
			problems = append(problems, svc.Name+" is running but unhealthy")
			raise(doctor.StatusFail)
		case found.State == "running" && strings.Contains(found.Status, "starting"):
			problems = append(problems, svc.Name+" is still starting")
			raise(doctor.StatusWarn)
		case found.State == "running":
			notes = append(notes, svc.Name)
		case strings.HasPrefix(found.Status, "Exited (0)"):
			// A one-shot job that finished is not a fault.
		default:
			problems = append(problems, fmt.Sprintf("%s is %s", svc.Name, found.State))
			raise(doctor.StatusFail)
		}
	}
	check := doctor.Check{Name: name, Title: m.Title + " containers"}
	if worst == doctor.StatusOK {
		check.Status = doctor.StatusOK
		check.Detail = fmt.Sprintf("%d service(s) running", len(notes))
		return []doctor.Check{check}
	}
	check.Status = worst
	check.Detail = strings.Join(problems, "; ")
	check.Fix = liveFix("Start or restart the app. If it stops again, read its container log.", "/admin/catalog", "docker compose -p "+m.Name+" ps")
	return []doctor.Check{check}
}

func probeAppReach(ctx context.Context, c DoctorLiveConfig, m catalog.Manifest) []doctor.Check {
	name := "app-reach:" + m.Name
	status, err := appReach(ctx, c.Store, c.Names, c.Local, m)
	if timedOut(ctx) {
		return []doctor.Check{unknownCheck(name, "the app's addresses did not answer in time")}
	}
	if err != nil {
		return []doctor.Check{unknownCheck(name, "the app's addresses could not be read: "+err.Error())}
	}
	if len(status.Addresses) == 0 {
		return []doctor.Check{{Name: name, Status: doctor.StatusOK, Title: m.Title + " addresses", Detail: m.Title + " has no address to ask yet"}}
	}
	var out []doctor.Check
	for _, a := range status.Addresses {
		check := doctor.Check{Name: name + ":" + a.Host, Title: m.Title + " at " + a.Address}
		switch {
		case a.Answers && a.Reason == "":
			check.Status, check.Detail = doctor.StatusOK, a.Kind+" address answers"
		case a.Answers:
			check.Status, check.Detail = doctor.StatusWarn, a.Reason
		default:
			check.Status, check.Detail = doctor.StatusFail, a.Reason
		}
		if check.Status != doctor.StatusOK && a.Fix != "" {
			check.Fix = liveFix(a.Fix, "/admin/catalog", "")
		}
		out = append(out, check)
	}
	return out
}

func probeAppSetUp(ctx context.Context, m catalog.Manifest) []doctor.Check {
	name := "app-setup:" + m.Name
	setUp, known, err := appsso.SetUpState(ctx, m)
	switch {
	case timedOut(ctx):
		return []doctor.Check{unknownCheck(name, "the app did not answer in time")}
	case err != nil:
		return []doctor.Check{unknownCheck(name, m.Title+" did not answer (it may still be starting)")}
	case !known:
		return nil
	case !setUp:
		return []doctor.Check{{Name: name, Title: m.Title + " first run", Status: doctor.StatusWarn,
			Detail: m.Title + " has not finished its own first-run setup",
			Fix:    liveFix("Wait a few minutes if it just installed. If it stays, open it in a browser and finish its setup.", "/admin/catalog", "")}}
	default:
		return []doctor.Check{{Name: name, Title: m.Title + " first run", Status: doctor.StatusOK, Detail: m.Title + " has finished its first-run setup"}}
	}
}
