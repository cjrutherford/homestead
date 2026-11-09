package craftfiles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/store"
)

type memSecrets struct{ m map[string]string }

func (s *memSecrets) SetSystem(n, v string) error { s.m[n] = v; return nil }
func (s *memSecrets) Get(n string) (string, error) {
	v, ok := s.m[n]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
func (s *memSecrets) Delete(n string) error { delete(s.m, n); return nil }

type mgrEnv struct {
	*fakeDAV
	m       *Manager
	secrets *memSecrets
	yaml    string
	audits  []string
	routes  []CraftRoute
	clock   *testClock
	baseURL string
}

func newMgr(t *testing.T) *mgrEnv {
	t.Helper()
	dav := newFakeDAV()
	srv := newDAVServer(t, dav)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &mgrEnv{fakeDAV: dav, secrets: &memSecrets{m: map[string]string{}}, clock: newClock()}
	e.yaml = filepath.Join(t.TempDir(), "craft-files.yaml")
	e.routes = []CraftRoute{{App: "gridcraft", Host: "gridcraft.lan", TLS: true}}
	e.m = &Manager{
		DB: st.DB(), Secrets: e.secrets, Client: func() *http.Client { return NewClient(nil) },
		YAMLPath: e.yaml, Now: e.clock.Now,
		Routes: func() ([]CraftRoute, error) { return e.routes, nil },
		Audit: func(actor, action, outcome, detail string) {
			e.audits = append(e.audits, actor+"|"+action+"|"+outcome+"|"+detail)
		},
	}
	e.baseURL = srv
	return e
}

func link(t *testing.T, e *mgrEnv, req PlanRequest, ap ApproveRequest) (Plan, Status, error) {
	t.Helper()
	p, err := e.m.Plan(req, "owner")
	if err != nil {
		return Plan{}, Status{}, err
	}
	ap.PlanID = p.ID
	st, err := e.m.Approve(context.Background(), ap, "owner")
	return p, st, err
}

func TestLinkWithAppPassword(t *testing.T) {
	e := newMgr(t)
	p, st, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL + "/", Account: "alice", Root: "/Homestead/"}, ApproveRequest{AppPassword: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != "Homestead" || strings.HasSuffix(p.BaseURL, "/") {
		t.Fatalf("plan not normalised: %+v", p)
	}
	if !st.Linked || !st.CredentialStored || st.Account != "alice" || st.DaemonCreated {
		t.Fatalf("status: %+v", st)
	}
	if e.secrets.m[SecretName] != testPassword {
		t.Fatal("app password not sealed in the secret store")
	}
	if !e.dirs["Homestead"] {
		t.Fatal("root folder not created")
	}
	// Nothing observable carries the password.
	b, _ := json.Marshal(st)
	pj, _ := json.Marshal(p)
	yaml, _ := os.ReadFile(e.yaml)
	all := string(b) + string(pj) + string(yaml) + strings.Join(e.audits, "\n")
	if strings.Contains(all, testPassword) {
		t.Fatalf("password leaked into status, plan, yaml or audit: %s", all)
	}
	if !strings.Contains(string(yaml), "linked: true") || !strings.Contains(string(yaml), "credential: sealed") {
		t.Fatalf("yaml mirror: %s", yaml)
	}
	// A second link needs rotate or unlink first.
	if _, err := e.m.Plan(PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "x"}, "owner"); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second link: %v", err)
	}
}

func TestLinkRejectsBadCredentialAndInput(t *testing.T) {
	e := newMgr(t)
	_, _, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead"}, ApproveRequest{AppPassword: "wrong"})
	if !errors.Is(err, ErrUpstream) || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("bad password: %v", err)
	}
	if len(e.secrets.m) != 0 {
		t.Fatal("rejected credential was stored")
	}
	for _, req := range []PlanRequest{
		{Action: ActionLink, BaseURL: "ftp://x", Account: "alice", Root: "r"},
		{Action: ActionLink, BaseURL: "https://u:p@x.lan", Account: "alice", Root: "r"},
		{Action: ActionLink, BaseURL: "https://x.lan?q=1", Account: "alice", Root: "r"},
		{Action: ActionLink, BaseURL: "https://x.lan", Account: "", Root: "r"},
		{Action: ActionLink, BaseURL: "https://x.lan", Account: "a/b", Root: "r"},
		{Action: ActionLink, BaseURL: "https://x.lan", Account: "alice", Root: ""},
		{Action: ActionLink, BaseURL: "https://x.lan", Account: "alice", Root: "../up"},
		{Action: "weird"},
		{Action: ActionRotate},
		{Action: ActionUnlink},
	} {
		if _, err := e.m.Plan(req, "owner"); err == nil {
			t.Errorf("plan accepted: %+v", req)
		}
	}
	// No password, and an unknown plan.
	if _, _, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead"}, ApproveRequest{}); !errors.Is(err, ErrInput) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := e.m.Approve(context.Background(), ApproveRequest{PlanID: "nope"}, "owner"); !errors.Is(err, ErrNoPlan) {
		t.Fatalf("unknown plan: %v", err)
	}
}

func TestPlanIsOneShotAndExpires(t *testing.T) {
	e := newMgr(t)
	p, _ := e.m.Plan(PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead"}, "owner")
	if _, err := e.m.Approve(context.Background(), ApproveRequest{PlanID: p.ID, AppPassword: testPassword}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Approve(context.Background(), ApproveRequest{PlanID: p.ID, AppPassword: testPassword}, "owner"); !errors.Is(err, ErrNoPlan) {
		t.Fatalf("replayed plan: %v", err)
	}
	p2, _ := e.m.Plan(PlanRequest{Action: ActionUnlink}, "owner")
	e.clock.Advance(planTTL + time.Second)
	if _, err := e.m.Approve(context.Background(), ApproveRequest{PlanID: p2.ID}, "owner"); !errors.Is(err, ErrNoPlan) {
		t.Fatalf("expired plan: %v", err)
	}
}

func TestLinkCreatesAppPasswordAndUnlinkRevokesIt(t *testing.T) {
	e := newMgr(t)
	_, st, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead", CreateAppPassword: true},
		ApproveRequest{AccountPassword: "account-login-password"})
	if err != nil {
		t.Fatal(err)
	}
	if !st.DaemonCreated || e.secrets.m[SecretName] != testPassword {
		t.Fatalf("daemon-created link: %+v", st)
	}
	if strings.Contains(strings.Join(e.audits, "\n"), "account-login-password") {
		t.Fatal("account password audited")
	}
	if _, err := e.m.Approve(context.Background(), ApproveRequest{}, "owner"); !errors.Is(err, ErrNoPlan) {
		t.Fatal(err)
	}
	// Wrong login password fails cleanly.
	if _, err := e.m.Plan(PlanRequest{Action: ActionLink}, "owner"); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatal(err)
	}
	p, _ := e.m.Plan(PlanRequest{Action: ActionUnlink}, "owner")
	st, err = e.m.Approve(context.Background(), ApproveRequest{PlanID: p.ID}, "owner")
	if err != nil || st.Linked {
		t.Fatalf("unlink: %+v %v", st, err)
	}
	if _, ok := e.secrets.m[SecretName]; ok {
		t.Fatal("credential survived unlink")
	}
	if e.revokes != 1 {
		t.Fatalf("daemon-created app password revoked %d times, want 1", e.revokes)
	}
	yaml, _ := os.ReadFile(e.yaml)
	if !strings.Contains(string(yaml), "linked: false") {
		t.Fatalf("yaml after unlink: %s", yaml)
	}
}

func TestCreateAppPasswordWrongLogin(t *testing.T) {
	e := newMgr(t)
	_, _, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead", CreateAppPassword: true},
		ApproveRequest{AccountPassword: "nope"})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("wrong login password: %v", err)
	}
	if len(e.secrets.m) != 0 {
		t.Fatal("stored something for a failed link")
	}
}

func TestRotate(t *testing.T) {
	e := newMgr(t)
	if _, _, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead"}, ApproveRequest{AppPassword: testPassword}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.password = "rotated-app-password"
	e.mu.Unlock()
	if _, err := e.m.Plan(PlanRequest{Action: ActionRotate, Account: "bob"}, "owner"); !errors.Is(err, ErrInput) {
		t.Fatalf("rotate changing account: %v", err)
	}
	_, st, err := link(t, e, PlanRequest{Action: ActionRotate}, ApproveRequest{AppPassword: "rotated-app-password"})
	if err != nil || !st.CredentialStored {
		t.Fatalf("rotate: %+v %v", st, err)
	}
	if e.secrets.m[SecretName] != "rotated-app-password" {
		t.Fatal("password not replaced")
	}
	// A bad replacement leaves the working one in place.
	if _, _, err := link(t, e, PlanRequest{Action: ActionRotate}, ApproveRequest{AppPassword: "bad"}); err == nil {
		t.Fatal("bad rotation accepted")
	}
	if e.secrets.m[SecretName] != "rotated-app-password" {
		t.Fatal("failed rotation replaced the credential")
	}
}

func TestLocalOnlyAndStatus(t *testing.T) {
	e := newMgr(t)
	st, _ := e.m.Status()
	if st.Linked || len(st.Apps) != 5 {
		t.Fatalf("fresh status: %+v", st)
	}
	var names []string
	for _, a := range st.Apps {
		names = append(names, a.Name+":"+a.Mode)
	}
	if strings.Join(names, " ") != "cadcraft:open-save effectcraft:open-only filmcraft:open-only gridcraft:open-save pdfcraft:open-save" {
		t.Fatalf("hooked apps: %v", names)
	}
	if err := e.m.SetLocalOnly("wordcraft", true, "owner"); !errors.Is(err, ErrNoHook) {
		t.Fatalf("unhooked app: %v", err)
	}
	if err := e.m.SetLocalOnly("gridcraft", true, "owner"); err != nil {
		t.Fatal(err)
	}
	yaml, _ := os.ReadFile(e.yaml)
	if !strings.Contains(string(yaml), "- gridcraft") {
		t.Fatalf("opt-out missing from desired config: %s", yaml)
	}
	info, _ := e.m.Info("gridcraft")
	if !info.LocalOnly || info.Linked {
		t.Fatalf("info: %+v", info)
	}
	if err := e.m.SetLocalOnly("gridcraft", false, "owner"); err != nil {
		t.Fatal(err)
	}
	if info, _ = e.m.Info("gridcraft"); info.LocalOnly {
		t.Fatal("opt-out not cleared")
	}
}

func TestLaunch(t *testing.T) {
	e := newMgr(t)
	sessions := NewSessions(&fakeOperator{expires: map[string]time.Time{"op": e.clock.Now().Add(time.Hour)}})
	sessions.Now = e.clock.Now
	launch := func(app string) (LaunchResult, error) {
		return e.m.Launch(sessions, "op", "owner", e.clock.Now().Add(time.Hour), app, "")
	}
	if _, err := launch("gridcraft"); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("unlinked launch: %v", err)
	}
	if _, _, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead"}, ApproveRequest{AppPassword: testPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err := launch("wordcraft"); !errors.Is(err, ErrNoHook) {
		t.Fatalf("unhooked launch: %v", err)
	}
	if _, err := launch("pdfcraft"); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("no route: %v", err)
	}
	res, err := launch("gridcraft")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.URL, "https://gridcraft.lan"+OpenPagePath+"#code=") {
		t.Fatalf("launch url: %s", res.URL)
	}
	code := strings.TrimPrefix(res.URL[strings.Index(res.URL, "#"):], "#code=")
	if strings.Contains(strings.Join(e.audits, "\n"), code) {
		t.Fatal("launch code audited")
	}
	if _, _, err := sessions.Exchange(code, "gridcraft", "gridcraft.lan"); err != nil {
		t.Fatalf("minted code does not exchange: %v", err)
	}
	e.m.SetLocalOnly("gridcraft", true, "owner")
	if _, err := launch("gridcraft"); !errors.Is(err, ErrLocalOnly) {
		t.Fatalf("local-only launch: %v", err)
	}
	e.m.SetLocalOnly("gridcraft", false, "owner")
	e.routes[0].TLS = false
	if res, _ = launch("gridcraft"); !strings.HasPrefix(res.URL, "http://gridcraft.lan") {
		t.Fatalf("plain route url: %s", res.URL)
	}
}
