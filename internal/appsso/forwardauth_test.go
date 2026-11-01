package appsso

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/sso"
	"homestead/internal/store"

	_ "modernc.org/sqlite"
)

func faInputs(t *testing.T) Inputs {
	m := manifest(t, "navidrome")
	table := []docker.Route{route("navidrome.lan", "navidrome"), route("authentik.lan", "server")}
	return Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, table), OwnerSub: "owner-sub", EdgeSource: "172.30.0.5/32",
		Accounts:        &AppAccounts{Names: []string{"admin", "kid"}, Admins: 1},
		PeopleUsernames: []string{"alice", "bob"}}
}

func TestForwardPlanOn(t *testing.T) {
	in := faInputs(t)
	p, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Adapter != ForwardAdapter || p.Gate == nil || p.Gate.Canonical != "navidrome.lan" || p.Provider != "Homestead: Navidrome" {
		t.Fatalf("plan: %+v", p)
	}
	if len(p.Gate.Bypass) != 3 || p.Gate.Bypass[0].Path != `^/rest/.*` || p.Gate.Bypass[0].Reason == "" {
		t.Fatalf("bypass: %+v", p.Gate.Bypass)
	}
	if p.Gate.Env["ND_EXTAUTH_USERHEADER"] != "X-Authentik-Username" || p.Gate.Env["ND_EXTAUTH_TRUSTEDSOURCES"] != "172.30.0.5/32" {
		t.Fatalf("env: %v", p.Gate.Env)
	}
	if p.Gate.AuthentikHost != "https://authentik.lan/" || strings.Join(p.Gate.ExistingUsers, ",") != "admin,kid" {
		t.Fatalf("gate: %+v", p.Gate)
	}
	if len(p.Access.Bindings) != 3 || p.Access.ClaimMapping != "" || p.Access.OwnerSub != "owner-sub" {
		t.Fatalf("access: %+v", p.Access)
	}
	all := strings.Join(append(p.Changes, p.Warnings...), "\n")
	for _, want := range []string{"/rest/", "/share/", "fails closed", "kid", "chosen in Navidrome itself"} {
		if !strings.Contains(all+p.AdminRoles, want) {
			t.Errorf("plan lacks %q:\n%s", want, all)
		}
	}
	if q, _ := BuildPlan(in, true); q.Digest != p.Digest || p.Digest == "" {
		t.Fatal("digest not stable")
	}
	in.Accounts = &AppAccounts{Names: []string{"admin", "kid", "new"}, Admins: 1}
	if q, _ := BuildPlan(in, true); q.Digest == p.Digest {
		t.Fatal("digest ignores the app's accounts")
	}
}

func TestForwardPlanPublishedUsesPublicNameAndStatesLANConsequence(t *testing.T) {
	in := faInputs(t)
	m := in.Manifest
	in.Routes = AppRoutes(m, []docker.Route{route("navidrome.lan", "navidrome", "music.example.com")})
	if _, err := BuildPlan(in, true); err == nil || !strings.Contains(err.Error(), "public name music.example.com") {
		t.Fatalf("published without a public Authentik must refuse: %v", err)
	}
	in.PublicAuthentikURL = "https://home.example.com/apps/auth"
	p, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Gate.Canonical != "music.example.com" || p.RedirectURIs[0] != "https://music.example.com" {
		t.Fatalf("canonical: %+v", p.Gate)
	}
	if !strings.Contains(strings.Join(p.Gate.Redirects, ","), "navidrome.lan") {
		t.Fatalf("redirects: %v", p.Gate.Redirects)
	}
	if p.Gate.AuthentikHost != "https://home.example.com/apps/auth/" || !strings.Contains(strings.Join(p.Warnings, "\n"), "must resolve and answer from this network") {
		t.Fatalf("host/warnings: %s / %v", p.Gate.AuthentikHost, p.Warnings)
	}
	// Unpublished with a public Authentik still uses it, and the LAN name is canonical.
	in.Routes = AppRoutes(m, []docker.Route{route("navidrome.lan", "navidrome")})
	if p, err := BuildPlan(in, true); err != nil || p.Gate.Canonical != "navidrome.lan" {
		t.Fatalf("%v %+v", err, p.Gate)
	}
}

func TestForwardPlanRefusals(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Inputs)
		want   string
	}{
		"collision":     {func(in *Inputs) { in.PeopleUsernames = []string{"Kid"} }, "account named kid"},
		"no admin":      {func(in *Inputs) { in.Accounts = &AppAccounts{Names: []string{}, Admins: 0} }, "no administrator yet"},
		"unreadable":    {func(in *Inputs) { in.Accounts = &AppAccounts{Err: "boom"} }, "could not be read"},
		"not read":      {func(in *Inputs) { in.Accounts = nil }, "could not be read"},
		"no authentik":  {func(in *Inputs) { in.AuthentikInstalled = false }, "Install Authentik"},
		"no dashboard":  {func(in *Inputs) { in.DashboardOK = false }, "dashboard sign-in"},
		"no owner":      {func(in *Inputs) { in.OwnerSub = "" }, "owner's Authentik account"},
		"no edge":       {func(in *Inputs) { in.EdgeSource = "" }, "edge network"},
		"no route":      {func(in *Inputs) { in.Routes = nil }, "no address"},
		"not installed": {func(in *Inputs) { in.Installed = false }, "not installed"},
	}
	for name, c := range cases {
		in := faInputs(t)
		c.mutate(&in)
		_, err := BuildPlan(in, true)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestForwardCollisionOnlyChecksTheFirstPlan(t *testing.T) {
	in := faInputs(t)
	first, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	// Once on, people's accounts are made by the gate, so their names exist.
	in.PeopleUsernames = []string{"kid"}
	in.Applied = &Record{Package: "navidrome", Adapter: ForwardAdapter, Issuer: first.Issuer, RedirectURIs: first.RedirectURIs, PluginVersion: first.Gate.Revision, Access: first.Access}
	p, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Changes, "\n"), "Nothing changes") {
		t.Fatalf("an applied, unchanged gate must re-plan as no change:\n%s", strings.Join(p.Changes, "\n"))
	}
	// But an admin must still exist.
	in.Accounts = &AppAccounts{Names: []string{"kid"}}
	if _, err := BuildPlan(in, true); err == nil {
		t.Fatal("no admin must refuse on a re-plan too")
	}
}

func TestForwardStatusReplansOnPublish(t *testing.T) {
	in := faInputs(t)
	first, _ := BuildPlan(in, true)
	in.Applied = &Record{Package: "navidrome", Adapter: ForwardAdapter, Issuer: first.Issuer, RedirectURIs: first.RedirectURIs, PluginVersion: first.Gate.Revision, Access: first.Access}
	st := BuildStatus(in, true)
	if !st.Offered || !st.Enabled || st.Replan || st.Adapter != ForwardAdapter {
		t.Fatalf("status: %+v", st)
	}
	in.Routes = AppRoutes(in.Manifest, []docker.Route{route("navidrome.lan", "navidrome", "music.example.com")})
	in.PublicAuthentikURL = "https://home.example.com/apps/auth"
	if st := BuildStatus(in, true); !st.Replan {
		t.Fatalf("publishing must re-plan the canonical name: %+v", st)
	}
}

func TestForwardPlanOff(t *testing.T) {
	in := faInputs(t)
	on, _ := BuildPlan(in, true)
	in.Applied = &Record{Package: "navidrome", Adapter: ForwardAdapter, Issuer: on.Issuer, RedirectURIs: on.RedirectURIs, Access: on.Access}
	in.OwnerEnv = map[string]string{"ND_EXTAUTH_USERHEADER": "X-Authentik-Username", "ND_EXTAUTH_TRUSTEDSOURCES": "172.30.0.5/32", "TZ": "UTC"}
	// Off needs neither the accounts nor the edge address.
	in.Accounts, in.EdgeSource = nil, ""
	off, err := BuildPlan(in, false)
	if err != nil {
		t.Fatal(err)
	}
	if off.Enabled || off.Gate == nil || off.Gate.Env["ND_EXTAUTH_USERHEADER"] == "" || off.Gate.Env["TZ"] != "" {
		t.Fatalf("off: %+v", off.Gate)
	}
	if len(off.Access.Groups) != 2 || off.Access.ClaimMapping != "" {
		t.Fatalf("off access: %+v", off.Access)
	}
	if !strings.Contains(strings.Join(off.Changes, "\n"), "own login again") {
		t.Fatalf("off must say the app is open again:\n%s", strings.Join(off.Changes, "\n"))
	}
	in.Applied = nil
	if p, _ := BuildPlan(in, false); !strings.Contains(strings.Join(p.Changes, "\n"), "already off") {
		t.Fatal("off when off")
	}
}

type fakeProxy struct {
	fakeAuthentik
	events *[]string
}

func (f *fakeProxy) ReconcileProxy(_ context.Context, spec sso.ProxySpec, step func(string)) error {
	*f.events = append(*f.events, "authentik-on:"+spec.ExternalHost+":"+strings.Join(spec.Bypass, "|")+":"+spec.AuthentikHost)
	step("created the proxy provider")
	return nil
}
func (f *fakeProxy) RemoveProxy(_ context.Context, spec sso.ProxySpec, step func(string)) error {
	*f.events = append(*f.events, "authentik-off")
	step("deleted the proxy provider")
	return nil
}

func TestForwardApplyOrderOnAndOff(t *testing.T) {
	stubDocker(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, catalog.OverrideFileName), []byte("services:\n  navidrome:\n    environment:\n      TZ: UTC\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var events []string
	up = func(context.Context, string, string) error { events = append(events, "up"); return nil }
	var gates []*docker.Gate
	rt := Runtime{Records: Records{DB: st.DB()}, StackDir: func(string) string { return dir },
		SetGate: func(_ context.Context, _ catalog.Manifest, g *docker.Gate) error {
			gates = append(gates, g)
			if g == nil {
				events = append(events, "gate-off")
			} else {
				events = append(events, "gate-on:"+string(g.Canonical))
			}
			return nil
		}}
	in := faInputs(t)
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	ak := &fakeProxy{events: &events}
	exec := func(p Plan) {
		t.Helper()
		def, err := Definition(rt, p, in.Manifest, dashboard, ak, "owner")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range def.Steps {
			if err := s.Run(context.Background()); err != nil {
				t.Fatalf("%s: %v", s.Name, err)
			}
		}
	}
	exec(plan)
	if got := strings.Join(events, " "); !strings.HasPrefix(got, "authentik-on:https://navidrome.lan:") || !strings.Contains(got, "gate-on:navidrome.lan up") {
		t.Fatalf("on order (Authentik, gate, then the app's trust settings): %s", got)
	}
	if i, j := strings.Index(strings.Join(events, " "), "gate-on"), strings.Index(strings.Join(events, " "), " up"); i > j {
		t.Fatal("the trust settings must come after the gate")
	}
	if g := gates[0]; len(g.Bypass) != 3 || g.CopyHeaders[0] != "X-Authentik-Username" {
		t.Fatalf("gate: %+v", g)
	}
	override, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	for _, want := range []string{"TZ: UTC", "ND_EXTAUTH_USERHEADER: X-Authentik-Username", "ND_EXTAUTH_TRUSTEDSOURCES: 172.30.0.5/32"} {
		if !strings.Contains(string(override), want) {
			t.Fatalf("override lacks %q:\n%s", want, override)
		}
	}
	if strings.Contains(string(override), "host-gateway") || strings.Contains(string(override), "homestead-ca") {
		t.Fatalf("a gate adds no extra hosts or CA mount:\n%s", override)
	}
	rec, ok, _ := Records{DB: st.DB()}.Get("navidrome")
	if !ok || rec.Adapter != ForwardAdapter || rec.PluginVersion != plan.Gate.Revision || rec.RedirectURIs[0] != "https://navidrome.lan" {
		t.Fatalf("record: %+v", rec)
	}
	// Again changes nothing.
	exec(plan)
	again, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if string(again) != string(override) {
		t.Fatalf("wiring duplicated:\n%s", again)
	}

	// Off: trust settings first, then the gate, then Authentik, then the record.
	in.Applied = &rec
	in.OwnerEnv = map[string]string{"ND_EXTAUTH_USERHEADER": "X-Authentik-Username", "ND_EXTAUTH_TRUSTEDSOURCES": "172.30.0.5/32"}
	off, err := BuildPlan(in, false)
	if err != nil {
		t.Fatal(err)
	}
	events = nil
	exec(off)
	if got := strings.Join(events, " "); got != "up gate-off authentik-off" {
		t.Fatalf("off order: %s", got)
	}
	gone, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(gone), "EXTAUTH") || !strings.Contains(string(gone), "TZ: UTC") {
		t.Fatalf("off must remove only the trust settings:\n%s", gone)
	}
	if _, ok, _ := (Records{DB: st.DB()}).Get("navidrome"); ok {
		t.Fatal("record left")
	}
}

func TestForwardDefinitionNeedsProxyClientAndEdge(t *testing.T) {
	in := faInputs(t)
	plan, _ := BuildPlan(in, true)
	if _, err := Definition(Runtime{StackDir: func(string) string { return "" }}, plan, in.Manifest, dashboard, &fakeAuthentik{}, "o"); err == nil {
		t.Fatal("a client without proxy support must refuse")
	}
	if _, err := Definition(Runtime{StackDir: func(string) string { return "" }}, plan, in.Manifest, dashboard, &fakeProxy{events: new([]string)}, "o"); err == nil {
		t.Fatal("no edge gate must refuse")
	}
}

func TestReadNavidromeAccounts(t *testing.T) {
	dbfile := filepath.Join(t.TempDir(), "src.db")
	db, err := sql.Open("sqlite", dbfile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE user (id TEXT, user_name TEXT, is_admin BOOL); INSERT INTO user VALUES ('1','Admin',1),('2','Kid',0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	old := copyFromContainer
	t.Cleanup(func() { copyFromContainer = old })
	copyFromContainer = func(_ context.Context, c, src, dst string) error {
		if c != "navidrome-navidrome" {
			t.Errorf("container %s", c)
		}
		if strings.HasSuffix(src, "-wal") || strings.HasSuffix(src, "-shm") {
			return os.ErrNotExist
		}
		data, err := os.ReadFile(dbfile)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o600)
	}
	got := ReadAccounts(context.Background(), manifest(t, "navidrome"))
	if got.Err != "" || got.Admins != 1 || strings.Join(got.Names, ",") != "admin,kid" {
		t.Fatalf("accounts: %+v", got)
	}
	copyFromContainer = func(context.Context, string, string, string) error { return os.ErrNotExist }
	if got := ReadAccounts(context.Background(), manifest(t, "navidrome")); got.Err == "" {
		t.Fatal("an unreadable database must report an error")
	}
}

// A recreated edge container gets a new address: the applied trust no
// longer matches, so status asks for a re-plan (S420).
func TestForwardStatusReplansWhenEdgeAddressMoves(t *testing.T) {
	in := faInputs(t)
	first, _ := BuildPlan(in, true)
	in.Applied = &Record{Package: "navidrome", Adapter: ForwardAdapter, Issuer: first.Issuer, RedirectURIs: first.RedirectURIs, PluginVersion: first.Gate.Revision, Access: first.Access}
	if BuildStatus(in, true).Replan {
		t.Fatal("same address must not replan")
	}
	in.EdgeSource = "172.30.0.9/32"
	if !BuildStatus(in, true).Replan {
		t.Fatal("a new edge address must replan")
	}
}

func kumaInputs(t *testing.T) Inputs {
	m := manifest(t, "uptime-kuma")
	table := []docker.Route{route("uptime-kuma.lan", "uptime-kuma"), route("authentik.lan", "server")}
	return Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, table), OwnerSub: "owner-sub", EdgeSource: "172.30.0.5/32"}
}

// Kuma (S419): admins only, no header, no trust settings, no account reader.
func TestKumaForwardPlan(t *testing.T) {
	in := kumaInputs(t)
	p, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	g := p.Gate
	if g == nil || g.Canonical != "uptime-kuma.lan" || len(g.CopyHeaders) != 0 || len(g.Env) != 0 || len(p.AppSettings) != 0 || len(g.ExistingUsers) != 0 {
		t.Fatalf("gate: %+v settings %v", g, p.AppSettings)
	}
	if len(g.Bypass) != 7 || g.Bypass[0].Path != `^/api/push/.*` {
		t.Fatalf("bypass: %+v", g.Bypass)
	}
	for _, b := range g.Bypass {
		if b.Reason == "" || strings.Contains(b.Path, "metrics") || strings.Contains(b.Path, "dashboard") || strings.Contains(b.Path, "socket") {
			t.Errorf("bad bypass %+v", b)
		}
	}
	// Admin-only bindings: the admins and owner groups, no users group.
	if len(p.Access.Bindings) != 2 || len(p.Access.Groups) != 2 || len(p.Access.OwnerOf) != 2 {
		t.Fatalf("access: %+v", p.Access)
	}
	for _, name := range append(append([]string{}, p.Access.Groups...), p.Access.Bindings...) {
		if strings.HasSuffix(name, "-users") {
			t.Fatalf("a users group is bound for Kuma: %+v", p.Access)
		}
	}
	all := strings.Join(p.Changes, "\n")
	for _, want := range []string{"admins group only", "keeps its own login", "/api/push/"} {
		if !strings.Contains(all, want) {
			t.Errorf("plan lacks %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "Trust settings written") {
		t.Fatalf("Kuma has no trust settings:\n%s", all)
	}
	// Needs no accounts reader and no admin check.
	in.Accounts, in.AccountsFn = nil, nil
	if q, err := BuildPlan(in, true); err != nil || q.Digest != p.Digest {
		t.Fatalf("plan needs no account reader: %v", err)
	}
}

func TestKumaForwardApplyHasNoWiringSteps(t *testing.T) {
	stubDocker(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var events []string
	up = func(context.Context, string, string) error { events = append(events, "up"); return nil }
	rt := Runtime{Records: Records{DB: st.DB()}, StackDir: func(string) string { return t.TempDir() },
		SetGate: func(_ context.Context, _ catalog.Manifest, g *docker.Gate) error {
			if g == nil {
				events = append(events, "gate-off")
			} else {
				events = append(events, "gate-on")
			}
			return nil
		}}
	in := kumaInputs(t)
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	ak := &fakeProxy{events: &events}
	exec := func(p Plan) {
		t.Helper()
		def, err := Definition(rt, p, in.Manifest, dashboard, ak, "owner")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range def.Steps {
			if err := s.Run(context.Background()); err != nil {
				t.Fatalf("%s: %v", s.Name, err)
			}
		}
	}
	exec(plan)
	if got := strings.Join(events, " "); !strings.HasPrefix(got, "authentik-on:") || !strings.HasSuffix(got, "gate-on") || strings.Contains(" "+got+" ", " up ") {
		t.Fatalf("on: Authentik then gate, and Kuma is not recreated: %s", got)
	}
	rec, ok, _ := Records{DB: st.DB()}.Get("uptime-kuma")
	if !ok {
		t.Fatal("no record")
	}
	in.Applied = &rec
	off, err := BuildPlan(in, false)
	if err != nil {
		t.Fatal(err)
	}
	events = nil
	exec(off)
	if got := strings.Join(events, " "); got != "gate-off authentik-off" {
		t.Fatalf("off: %s", got)
	}
}

func TestForwardPlanRequiresHostBlock(t *testing.T) {
	in := faInputs(t)
	in.Manifest.SSO.ForwardAuth.RequiresHostBlock = true
	in.HostBlockRequired = true
	in.HostBlockProblem = "nftables is not available on this host."
	if _, err := BuildPlan(in, true); err == nil || !strings.Contains(err.Error(), "nothing but the edge") || !strings.Contains(err.Error(), "nftables is not available") {
		t.Fatalf("a missing host block must refuse the plan: %v", err)
	}
	in.HostBlockProblem = ""
	if _, err := BuildPlan(in, true); err != nil {
		t.Fatalf("block available: %v", err)
	}
	in.HostBlockProblem = "x"
	in.Manifest.SSO.ForwardAuth.RequiresHostBlock = false
	if _, err := BuildPlan(in, true); err != nil {
		t.Fatalf("an app that does not require it is never refused: %v", err)
	}
}
