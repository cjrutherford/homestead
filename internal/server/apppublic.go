package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/router"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// Public app names on the tunnel (S290, ADR-041 §1): status, a read-only
// plan, and apply by digest as an operation. A public name is a tunnel
// name on one of the app's local edge routes, its ingress entry and its
// CNAME, added or removed together. All three routes are session class.

// MountAppPublic registers the public app name routes.
func (s *Server) MountAppPublic(st *store.Store, secrets *secret.Store, catalogDir string) {
	p := &appPublic{s: s, st: st, secrets: secrets, catalogDir: catalogDir}
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/public", p.status)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/public/plan", p.plan)
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/public/apply", p.apply)
}

type appPublic struct {
	s          *Server
	st         *store.Store
	secrets    *secret.Store
	catalogDir string
}

type appPublicStatus struct {
	App       string           `json:"app"`
	Available bool             `json:"available"`
	Reason    string           `json:"reason,omitempty"`
	TunnelID  string           `json:"tunnel_id,omitempty"`
	Tunnel    string           `json:"tunnel,omitempty"`
	Routes    []appPublicRoute `json:"routes"`
	Proposed  string           `json:"proposed,omitempty"`
	SignIn    bool             `json:"sign_in"`
}

// appPublicRoute is one of the app's local edge routes and the public
// names the tunnel delivers to it.
type appPublicRoute struct {
	Route  string   `json:"route"`
	Public []string `json:"public"`
}

// appPublicFacts is what status and plan read about the app.
type appPublicFacts struct {
	manifest   catalog.Manifest
	routes     []docker.Route
	all        []docker.Route
	tunnelID   string
	tunnelName string
	signIn     bool
	reason     string
}

func (p *appPublic) facts(name string) (appPublicFacts, error) {
	entries, err := catalog.Load(p.catalogDir)
	if err != nil {
		return appPublicFacts{}, fmt.Errorf("catalog unavailable: %w", err)
	}
	manifest, err := entries.Get(name)
	if err != nil {
		return appPublicFacts{}, errUnknownApp
	}
	f := appPublicFacts{manifest: manifest}
	installed, err := catalog.Installed(p.st, manifest.Name, stackDirFor(manifest.Name))
	if err != nil {
		return f, err
	}
	set, err := loadEdgeRoutes(p.st)
	if err != nil {
		return f, err
	}
	f.all = set.Routes
	f.routes = appsso.AppRoutes(manifest, set.Routes)
	if f.tunnelID, f.tunnelName, err = router.RecordedTunnel(p.st); err != nil {
		return f, err
	}
	if _, ok, err := (appsso.Records{DB: p.st.DB()}).Get(manifest.Name); err != nil {
		return f, err
	} else {
		f.signIn = ok
	}
	switch {
	case manifest.Name == authentikPackage:
		f.reason = "Authentik's public name belongs to the tunnel setup (Settings, Tunnel)."
	case !installed:
		f.reason = fmt.Sprintf("Install %s first.", manifest.Title)
	case f.tunnelID == "":
		f.reason = "No tunnel is set up yet. Set one up, or adopt yours, on the Tunnel page."
	case len(f.routes) == 0:
		f.reason = fmt.Sprintf("%s has no address on the edge yet.", manifest.Title)
	}
	return f, nil
}

func (p *appPublic) writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUnknownApp) {
		http.Error(w, "unknown app", http.StatusNotFound)
		return
	}
	writePlanError(w, err)
}

func (p *appPublic) status(w http.ResponseWriter, r *http.Request) {
	f, err := p.facts(r.PathValue("name"))
	if err != nil {
		p.writeError(w, err)
		return
	}
	out := appPublicStatus{App: f.manifest.Name, Available: f.reason == "", Reason: f.reason,
		TunnelID: f.tunnelID, Tunnel: f.tunnelName, Routes: []appPublicRoute{}, SignIn: f.signIn}
	for _, route := range f.routes {
		entry := appPublicRoute{Route: string(route.Host), Public: []string{}}
		for _, n := range route.Tunnel {
			entry.Public = append(entry.Public, string(n))
		}
		out.Routes = append(out.Routes, entry)
	}
	if out.Available {
		domain, err := p.proposedDomain(f.tunnelID)
		if err != nil {
			p.writeError(w, err)
			return
		}
		if domain != "" {
			out.Proposed = f.manifest.Name + "." + domain
		}
	}
	writeJSON(w, out)
}

// proposedDomain is the setup domain, else the tunnel's stored zone name
// (an adopted tunnel without a setup domain, S302).
func (p *appPublic) proposedDomain(tunnelID string) (string, error) {
	if domain := p.s.tunnelFacts().Domain; domain != "" {
		return domain, nil
	}
	return router.TunnelZoneName(p.st, tunnelID)
}

type appPublicRequest struct {
	Enabled  *bool  `json:"enabled"`
	Hostname string `json:"hostname,omitempty"`
	Route    string `json:"route,omitempty"`
	Digest   string `json:"digest,omitempty"`
}

// appPublicPlan is one public name added to or removed from an app.
type appPublicPlan struct {
	App      string             `json:"app"`
	Action   string             `json:"action"`
	Hostname string             `json:"hostname"`
	Route    string             `json:"route"`
	Tunnel   string             `json:"tunnel"`
	TunnelID string             `json:"tunnel_id"`
	Zone     string             `json:"zone"`
	DNS      []tunnelPlanChange `json:"dns"`
	// Before and After are the connector's public names around the change.
	Before   []string `json:"connector_before"`
	After    []string `json:"connector_after"`
	SignIn   string   `json:"sign_in,omitempty"`
	Warnings []string `json:"warnings"`
	Digest   string   `json:"digest"`

	zoneID   string
	replaces []router.DNSRecord
	keep     *router.DNSRecord
	owned    []router.OwnedRecord
	restores []router.ReplacedRecord
}

func (p appPublicPlan) digest() string {
	h := sha256.New()
	visible := p
	visible.Digest = ""
	data, _ := json.Marshal(visible)
	h.Write(data)
	fmt.Fprintf(h, "\nzone=%s", p.zoneID)
	for _, record := range p.replaces {
		fmt.Fprintf(h, "\nreplace=%s", record.ID)
	}
	if p.keep != nil {
		fmt.Fprintf(h, "\nkeep=%s", p.keep.ID)
	}
	for _, record := range p.owned {
		fmt.Fprintf(h, "\nowned=%s", record.RecordID)
	}
	for _, record := range p.restores {
		fmt.Fprintf(h, "\nrestore=%s", record.Record.ID)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// tunnelNameMap is every edge route's current tunnel names.
func tunnelNameMap(routes []docker.Route) map[docker.Hostname][]docker.Hostname {
	names := map[docker.Hostname][]docker.Hostname{}
	for _, r := range routes {
		if len(r.Tunnel) > 0 {
			names[r.Host] = append([]docker.Hostname(nil), r.Tunnel...)
		}
	}
	return names
}

// buildPlan reads Cloudflare and the store and changes nothing.
func (p *appPublic) buildPlan(ctx context.Context, name string, req appPublicRequest) (appPublicPlan, error) {
	f, err := p.facts(name)
	if err != nil {
		return appPublicPlan{}, err
	}
	if f.reason != "" {
		return appPublicPlan{}, refusePlan(http.StatusUnprocessableEntity, "%s", f.reason)
	}
	hostname := strings.ToLower(strings.TrimSpace(req.Hostname))
	plan := appPublicPlan{App: f.manifest.Name, Tunnel: f.tunnelName, TunnelID: f.tunnelID,
		DNS: []tunnelPlanChange{}, Warnings: []string{}}
	recorded, err := recordedIngress(p.st, f.tunnelID)
	if err != nil {
		return appPublicPlan{}, err
	}
	for _, in := range recorded {
		plan.Before = append(plan.Before, in.Hostname)
	}
	token, err := tunnelToken(p.secrets)
	if err != nil {
		return appPublicPlan{}, refusePlan(http.StatusUnprocessableEntity, "%v", err)
	}
	client, err := tunnelClient()
	if err != nil {
		return appPublicPlan{}, err
	}
	if *req.Enabled {
		err = p.planAdd(ctx, client, token, f, hostname, req.Route, &plan)
	} else {
		err = p.planRemove(f, hostname, &plan)
		if err == nil {
			err = p.removeZone(ctx, client, token, &plan)
		}
	}
	if err != nil {
		return appPublicPlan{}, err
	}
	for _, h := range plan.Before {
		if plan.Action == "remove" && h == plan.Hostname {
			continue
		}
		plan.After = append(plan.After, h)
	}
	if plan.Action == "add" {
		plan.After = append(plan.After, plan.Hostname)
		sort.Strings(plan.After)
	}
	if plan.Before == nil {
		plan.Before = []string{}
	}
	if plan.After == nil {
		plan.After = []string{}
	}
	if f.signIn {
		verb := "accepts"
		if plan.Action == "remove" {
			verb = "stops accepting"
		}
		plan.SignIn = "App sign-in is on: afterwards, re-plan it on this page and approve it, so sign-in " +
			verb + " https://" + plan.Hostname + "."
	}
	plan.Digest = plan.digest()
	return plan, nil
}

func (p *appPublic) planAdd(ctx context.Context, client *router.TunnelClient, token string, f appPublicFacts, hostname, route string, plan *appPublicPlan) error {
	plan.Action = "add"
	if hostname == "" {
		domain, err := p.proposedDomain(f.tunnelID)
		if err != nil {
			return err
		}
		if domain == "" {
			return refusePlan(http.StatusUnprocessableEntity, "no domain is chosen in setup: choose one on the Domain step, or name the hostname")
		}
		hostname = f.manifest.Name + "." + domain
	}
	plan.Hostname = hostname
	if err := (router.TunnelRoute{Hostname: hostname, TunnelID: f.tunnelID, Service: tunnelEntryService}).Validate(); err != nil {
		return refusePlan(http.StatusBadRequest, "%v", err)
	}
	if docker.IsPrivateName(docker.Hostname(hostname)) {
		return refusePlan(http.StatusBadRequest, "%s is a local name; the tunnel carries public names", hostname)
	}
	target := -1
	for i, r := range f.routes {
		if route == "" || strings.EqualFold(string(r.Host), route) {
			target = i
			break
		}
	}
	if target < 0 {
		return refusePlan(http.StatusBadRequest, "%s is not one of %s's edge routes", route, f.manifest.Title)
	}
	plan.Route = string(f.routes[target].Host)
	for _, r := range f.all {
		for _, n := range r.AllNames() {
			if strings.EqualFold(string(n), hostname) {
				return refusePlan(http.StatusUnprocessableEntity, "%s is already a name of the edge route %s", hostname, r.Host)
			}
		}
	}
	for _, h := range plan.Before {
		if h == hostname {
			return refusePlan(http.StatusUnprocessableEntity, "%s is already on the tunnel", hostname)
		}
	}
	zone, err := client.ZoneFor(ctx, token, hostname)
	if errors.Is(err, router.ErrNoZone) {
		return refusePlan(http.StatusUnprocessableEntity, "no Cloudflare zone the token can see contains %s", hostname)
	}
	if err != nil {
		return err
	}
	tunnelZone, err := router.TunnelZone(p.st, f.tunnelID)
	if err != nil {
		return err
	}
	if tunnelZone != "" && zone.ID != tunnelZone {
		return refusePlan(http.StatusUnprocessableEntity, "%s is not in the tunnel's Cloudflare zone; pick a name in the same zone as its other names", hostname)
	}
	plan.Zone, plan.zoneID = zone.Name, zone.ID
	existing, err := client.RecordsAt(ctx, token, zone.ID, hostname)
	if err != nil {
		return err
	}
	change := tunnelPlanChange{Name: hostname, Type: "CNAME", Content: router.TunnelTarget(f.tunnelID), Proxied: true,
		Action: "create", Replaces: []router.DNSRecord{}}
	for _, record := range existing {
		if record.Type == "CNAME" && strings.EqualFold(record.Content, change.Content) {
			change.Action = "keep"
			kept := record
			plan.keep = &kept
			continue
		}
		switch record.Type {
		case "A", "AAAA", "CNAME":
			change.Replaces = append(change.Replaces, record)
		default:
			return refusePlan(http.StatusUnprocessableEntity,
				"%s already has a %s record, which a tunnel CNAME cannot sit next to; remove it in Cloudflare or pick another hostname",
				hostname, record.Type)
		}
	}
	sort.Slice(change.Replaces, func(i, j int) bool { return change.Replaces[i].ID < change.Replaces[j].ID })
	if len(change.Replaces) > 0 {
		change.Action = "replace"
		plan.Warnings = append(plan.Warnings,
			"Existing records for "+hostname+" are deleted now and put back when this public name is removed, unless something else is at that name by then.")
	}
	plan.replaces = change.Replaces
	plan.DNS = append(plan.DNS, change)
	plan.Warnings = append(plan.Warnings,
		"Anyone on the internet can reach "+f.manifest.Title+" at https://"+hostname+". Homestead puts no Authentik check in front of it: the app's own sign-in is what protects it.")
	if !f.signIn && f.manifest.SSO.OIDC != nil {
		plan.Warnings = append(plan.Warnings, "App sign-in through Authentik is off. Turning it on first is recommended.")
	}
	return nil
}

// removeZone names the zone a removed name lives in: the stored one,
// else the zone Cloudflare finds for the name (adopted tunnels, S302).
func (p *appPublic) removeZone(ctx context.Context, client *router.TunnelClient, token string, plan *appPublicPlan) error {
	name, err := router.TunnelZoneName(p.st, plan.TunnelID)
	if err != nil || name != "" {
		plan.Zone = name
		return err
	}
	zone, err := client.ZoneFor(ctx, token, plan.Hostname)
	if errors.Is(err, router.ErrNoZone) {
		return nil
	}
	if err != nil {
		return err
	}
	plan.Zone = zone.Name
	return nil
}

func (p *appPublic) planRemove(f appPublicFacts, hostname string, plan *appPublicPlan) error {
	plan.Action = "remove"
	var names []string
	for _, r := range f.routes {
		for _, n := range r.Tunnel {
			if hostname == "" || string(n) == hostname {
				names = append(names, string(n))
				plan.Route = string(r.Host)
			}
		}
	}
	switch {
	case len(names) == 0 && hostname == "":
		return refusePlan(http.StatusUnprocessableEntity, "%s has no public name", f.manifest.Title)
	case len(names) == 0:
		return refusePlan(http.StatusUnprocessableEntity, "%s is not one of %s's public names", hostname, f.manifest.Title)
	case len(names) > 1:
		return refusePlan(http.StatusBadRequest, "%s has several public names; name the one to remove", f.manifest.Title)
	}
	plan.Hostname = names[0]
	owned, err := router.OwnedRecords(p.st, f.tunnelID)
	if err != nil {
		return err
	}
	for _, record := range owned {
		if record.Hostname == plan.Hostname {
			plan.owned = append(plan.owned, record)
			plan.zoneID = record.ZoneID
			plan.DNS = append(plan.DNS, tunnelPlanChange{Name: plan.Hostname, Type: "CNAME",
				Content: router.TunnelTarget(f.tunnelID), Proxied: true, Action: "delete", Replaces: []router.DNSRecord{}})
		}
	}
	replaced, err := router.ReplacedRecords(p.st, f.tunnelID)
	if err != nil {
		return err
	}
	for _, r := range replaced {
		if r.Record.Name == plan.Hostname {
			plan.restores = append(plan.restores, r)
			plan.DNS = append(plan.DNS, tunnelPlanChange{Name: r.Record.Name, Type: r.Record.Type,
				Content: r.Record.Content, Proxied: r.Record.Proxied, Action: "restore", Replaces: []router.DNSRecord{}})
		}
	}
	if len(plan.owned) == 0 {
		plan.Warnings = append(plan.Warnings, "Homestead did not create the DNS record for "+plan.Hostname+", so it is left in Cloudflare and answers 404 until you remove it there.")
	} else {
		plan.Warnings = append(plan.Warnings, "The DNS record is deleted only if it still points at the tunnel.")
	}
	return nil
}

func (p *appPublic) decode(w http.ResponseWriter, r *http.Request, apply bool) (appPublicRequest, bool) {
	var req appPublicRequest
	if !decodeResolverRequest(w, r, &req) || !requireEnabled(w, req.Enabled) {
		return req, false
	}
	if !apply && req.Digest != "" {
		http.Error(w, "a plan takes no digest", http.StatusBadRequest)
		return req, false
	}
	if apply && req.Digest == "" {
		http.Error(w, "digest is required: review the plan, then approve it with its digest", http.StatusBadRequest)
		return req, false
	}
	if !*req.Enabled && req.Route != "" {
		http.Error(w, "removing a public name takes no route", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

func (p *appPublic) plan(w http.ResponseWriter, r *http.Request) {
	req, ok := p.decode(w, r, false)
	if !ok {
		return
	}
	plan, err := p.buildPlan(r.Context(), r.PathValue("name"), req)
	if err != nil {
		p.writeError(w, err)
		return
	}
	writeJSON(w, plan)
}

func (p *appPublic) apply(w http.ResponseWriter, r *http.Request) {
	req, ok := p.decode(w, r, true)
	if !ok {
		return
	}
	plan, err := p.buildPlan(r.Context(), r.PathValue("name"), req)
	if err != nil {
		p.writeError(w, err)
		return
	}
	if plan.Digest != req.Digest {
		http.Error(w, "the plan changed since it was reviewed; review it again", http.StatusConflict)
		return
	}
	token, err := tunnelToken(p.secrets)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	def := appPublicAddDefinition(p.st, token, plan)
	action, detail := "app.public.add", fmt.Sprintf("%s: %s -> %s on tunnel %s", plan.App, plan.Hostname, plan.Route, plan.Tunnel)
	if plan.Action == "remove" {
		def = appPublicRemoveDefinition(p.st, token, plan)
		action, detail = "app.public.remove", fmt.Sprintf("%s: %s off tunnel %s", plan.App, plan.Hostname, plan.Tunnel)
	}
	runner := operation.NewRunner(p.st)
	op, err := runner.Start(r.Context(), def, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	p.s.auditOperation(p.st, runner, op.ID, operatorActor(r), action, fmt.Sprintf("operation %d: %s", op.ID, detail))
	writeJSON(w, map[string]any{"id": op.ID})
}

// withTunnelName is the current edge name map with hostname added to, or
// taken from, route.
func withTunnelName(st *store.Store, route, hostname string, add bool) (map[docker.Hostname][]docker.Hostname, error) {
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return nil, err
	}
	names := tunnelNameMap(set.Routes)
	for host, list := range names {
		kept := list[:0]
		for _, n := range list {
			if string(n) != hostname {
				kept = append(kept, n)
			}
		}
		names[host] = kept
	}
	if add {
		names[docker.Hostname(route)] = append(names[docker.Hostname(route)], docker.Hostname(hostname))
	}
	return names, nil
}

// appPublicAddDefinition points the name at the tunnel, records its
// route, gives the app's edge route the name, and rewrites the
// connector. A failure undoes what this run did.
func appPublicAddDefinition(st *store.Store, apiToken string, plan appPublicPlan) operation.Definition {
	var created string
	undoDNS := func(ctx context.Context) {
		if client, err := tunnelClient(); err == nil && created != "" {
			if client.DeleteRecord(ctx, apiToken, plan.zoneID, created) == nil {
				_ = router.ForgetOwned(st, created)
			}
		}
		if plan.keep != nil {
			_ = router.ForgetOwned(st, plan.keep.ID)
		}
		_ = restoreReplacedRecordsAt(ctx, st, apiToken, plan.TunnelID, plan.Hostname)
	}
	undoRoute := func(ctx context.Context) {
		_ = router.DeleteRoute(st, plan.Hostname)
		if names, err := withTunnelName(st, plan.Route, plan.Hostname, false); err == nil {
			_ = applyTunnelNames(ctx, st, names)
		}
		undoDNS(ctx)
	}
	return operation.Definition{Type: "app.public.add", Steps: []operation.StepDef{
		{Name: "dns", Run: func(ctx context.Context) error {
			client, err := tunnelClient()
			if err != nil {
				return err
			}
			for _, record := range plan.replaces {
				if err := router.RecordReplaced(st, router.ReplacedRecord{TunnelID: plan.TunnelID, ZoneID: plan.zoneID, Record: record}); err != nil {
					undoDNS(ctx)
					return err
				}
				if err := client.DeleteRecord(ctx, apiToken, plan.zoneID, record.ID); err != nil {
					_ = router.ForgetReplaced(st, plan.TunnelID, record.ID)
					undoDNS(ctx)
					return fmt.Errorf("tunnel: delete %s record for %s: %w", record.Type, record.Name, err)
				}
			}
			recordID := ""
			if plan.keep != nil {
				recordID = plan.keep.ID
			} else {
				record, err := client.CreateTunnelCNAME(ctx, apiToken, plan.zoneID, plan.Hostname, plan.TunnelID)
				if err != nil {
					undoDNS(ctx)
					return fmt.Errorf("tunnel: create DNS record for %s: %w", plan.Hostname, err)
				}
				created, recordID = record.ID, record.ID
			}
			if err := router.RecordOwned(st, router.OwnedRecord{RecordID: recordID, ZoneID: plan.zoneID,
				Hostname: plan.Hostname, TunnelID: plan.TunnelID}); err != nil {
				undoDNS(ctx)
				return err
			}
			return nil
		}},
		{Name: "record-route", Run: func(ctx context.Context) error {
			account, err := router.TunnelAccount(st, plan.TunnelID)
			if err == nil {
				err = router.RecordRoute(st, router.TunnelRoute{Hostname: plan.Hostname, TunnelID: plan.TunnelID,
					Service: tunnelEntryService}, plan.Tunnel)
			}
			if err == nil && account != "" {
				err = router.SetTunnelAccount(st, plan.TunnelID, account, plan.zoneID, plan.Zone)
			}
			if err != nil {
				undoRoute(ctx)
			}
			return err
		}},
		{Name: "edge", Run: func(ctx context.Context) error {
			names, err := withTunnelName(st, plan.Route, plan.Hostname, true)
			if err == nil {
				err = applyTunnelNames(ctx, st, names)
			}
			if err != nil {
				undoRoute(ctx)
			}
			return err
		}},
		{Name: "connector", Run: func(ctx context.Context) error {
			if err := syncConnector(st, plan.TunnelID); err != nil {
				undoRoute(ctx)
				_ = syncConnector(st, plan.TunnelID)
				return err
			}
			return nil
		}},
	}}
}

// appPublicRemoveDefinition stops delivering the name first, then takes
// it off the edge route, then deletes its CNAME (only while it still
// points at the tunnel) and puts back the records it replaced.
func appPublicRemoveDefinition(st *store.Store, apiToken string, plan appPublicPlan) operation.Definition {
	return operation.Definition{Type: "app.public.remove", Steps: []operation.StepDef{
		{Name: "connector", Run: func(ctx context.Context) error {
			if err := router.DeleteRoute(st, plan.Hostname); err != nil {
				return err
			}
			return syncConnector(st, plan.TunnelID)
		}},
		{Name: "edge", Run: func(ctx context.Context) error {
			names, err := withTunnelName(st, plan.Route, plan.Hostname, false)
			if err != nil {
				return err
			}
			return applyTunnelNames(ctx, st, names)
		}},
		{Name: "delete-dns", Run: func(ctx context.Context) error {
			return deleteOwnedRecordsAt(ctx, st, apiToken, plan.TunnelID, plan.Hostname)
		}},
		{Name: "restore-dns", Run: func(ctx context.Context) error {
			return restoreReplacedRecordsAt(ctx, st, apiToken, plan.TunnelID, plan.Hostname)
		}},
	}}
}
