package sso

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

const apiToken = "ak-admin-token-do-not-leak"

// fakeAuthentik is the slice of Authentik's admin API Homestead uses.
type fakeAuthentik struct {
	mu          sync.Mutex
	srv         *httptest.Server
	provider    map[string]any
	app         map[string]any
	posts       int
	patches     int
	badToken    bool
	noKey       bool
	requests    []string
	version     string
	nextPK      int
	secretSet   string
	noDiscovery bool
	// access state (S314)
	groups   map[string]map[string]any // name -> group
	bindings []map[string]any
	mappings map[string]map[string]any // name -> custom scope mapping
	users    []map[string]any
	deleted  []string
	// recovery state (S333): the default brand's recovery flow, and the
	// flows, stages, prompts and flow bindings created for it. Their posts
	// count in recoveryPosts, not posts.
	recovery      string
	noBrand       bool
	flows         map[string]map[string]any // slug -> flow
	named         map[string]map[string]any // path+name -> stage or prompt
	flowBindings  []map[string]any
	recoveryPosts int
	// owner-links-only policy (S466): expression policies by name.
	policies      map[string]map[string]any
	policyPatches int
	policyBinds   []map[string]any
	brandPatches  int
	// login hint state (S336): the default authentication flow's title.
	authTitle   string
	noAuthFlow  bool
	titlePatchs int
	// proxy state (S418): the proxy provider and the embedded outpost.
	proxy     map[string]any
	outpost   map[string]any
	noOutpost bool
}

func newFakeAuthentik(t *testing.T) *fakeAuthentik {
	f := &fakeAuthentik{version: "2026.8.3", nextPK: 41, groups: map[string]map[string]any{}, mappings: map[string]map[string]any{},
		authTitle: "Welcome to authentik!", flows: map[string]map[string]any{}, named: map[string]map[string]any{},
		users: []map[string]any{
			{"pk": 7, "uid": "user-1", "username": "owner", "type": "internal", "is_active": true},
			{"pk": 8, "uid": "user-2", "username": "guest", "type": "internal", "is_active": true},
		}}
	f.outpost = map[string]any{"pk": "op-1", "name": "authentik Embedded Outpost", "providers": []any{float64(5)},
		"config": map[string]any{"authentik_host": "", "log_level": "info"}}
	f.groups["outsider"] = map[string]any{"pk": "g-outsider", "name": "outsider", "users": []any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func list(items ...any) map[string]any { return map[string]any{"results": items} }

func (f *fakeAuthentik) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
		if f.noDiscovery {
			http.NotFound(w, r)
			return
		}
		iss := f.srv.URL + "/application/o/homestead/"
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": iss, "authorization_endpoint": f.srv.URL + "/a",
			"token_endpoint": f.srv.URL + "/t", "jwks_uri": f.srv.URL + "/j"})
		return
	}
	if f.badToken || r.Header.Get("Authorization") != "Bearer "+apiToken {
		http.Error(w, `{"detail":"Token invalid/expired"}`, http.StatusForbidden)
		return
	}
	var body map[string]any
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPatch) {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	enc := json.NewEncoder(w)
	switch {
	case r.URL.Path == "/api/v3/admin/version/":
		_ = enc.Encode(map[string]any{"version_current": f.version})
	case r.URL.Path == "/api/v3/flows/instances/" && r.Method == http.MethodPost:
		slug := body["slug"].(string)
		body["pk"] = "flow-" + slug
		body["policybindingmodel_ptr_id"] = "pbm-" + slug
		f.flows[slug] = body
		f.recoveryPosts++
		_ = enc.Encode(body)
	case r.URL.Path == "/api/v3/flows/instances/default-authentication-flow/" && r.Method == http.MethodPatch:
		f.authTitle, _ = body["title"].(string)
		f.titlePatchs++
		_ = enc.Encode(body)
	case r.URL.Path == "/api/v3/flows/instances/" && r.URL.Query().Get("slug") == "default-authentication-flow":
		if f.noAuthFlow {
			_ = enc.Encode(list())
			return
		}
		_ = enc.Encode(list(map[string]any{"pk": "flow-auth", "slug": "default-authentication-flow", "title": f.authTitle}))
	case r.URL.Path == "/api/v3/flows/instances/":
		slug := r.URL.Query().Get("slug")
		if strings.HasPrefix(slug, "default-provider-") {
			_ = enc.Encode(list(map[string]any{"pk": "flow-" + slug, "slug": slug}))
			return
		}
		if fl, ok := f.flows[slug]; ok {
			_ = enc.Encode(list(fl))
			return
		}
		_ = enc.Encode(list())
	case r.URL.Path == "/api/v3/core/brands/":
		if f.noBrand {
			_ = enc.Encode(list(map[string]any{"brand_uuid": "b-other", "default": false, "flow_recovery": nil}))
			return
		}
		var rec any
		if f.recovery != "" {
			rec = f.recovery
		}
		_ = enc.Encode(list(map[string]any{"brand_uuid": "b-default", "domain": "authentik-default", "default": true, "flow_recovery": rec}))
	case r.URL.Path == "/api/v3/core/brands/b-default/" && r.Method == http.MethodPatch:
		f.recovery, _ = body["flow_recovery"].(string)
		f.brandPatches++
		_ = enc.Encode(body)
	case r.URL.Path == "/api/v3/flows/bindings/" && r.Method == http.MethodGet:
		var out []any
		for _, b := range f.flowBindings {
			if b["target"] == r.URL.Query().Get("target") {
				out = append(out, b)
			}
		}
		_ = enc.Encode(list(out...))
	case r.URL.Path == "/api/v3/flows/bindings/" && r.Method == http.MethodPost:
		f.flowBindings = append(f.flowBindings, body)
		f.recoveryPosts++
		_ = enc.Encode(body)
	case r.URL.Path == "/api/v3/policies/expression/" && r.Method == http.MethodGet:
		if o, ok := f.policies[r.URL.Query().Get("name")]; ok {
			_ = enc.Encode(list(o))
			return
		}
		_ = enc.Encode(list())
	case r.URL.Path == "/api/v3/policies/expression/" && r.Method == http.MethodPost:
		if f.policies == nil {
			f.policies = map[string]map[string]any{}
		}
		body["pk"] = "policy-" + body["name"].(string)
		f.policies[body["name"].(string)] = body
		_ = enc.Encode(body)
	case strings.HasPrefix(r.URL.Path, "/api/v3/policies/expression/") && r.Method == http.MethodPatch:
		for _, o := range f.policies {
			if "/api/v3/policies/expression/"+o["pk"].(string)+"/" == r.URL.Path {
				o["expression"] = body["expression"]
				f.policyPatches++
			}
		}
		_ = enc.Encode(body)
	case strings.HasPrefix(r.URL.Path, "/api/v3/stages/") && r.Method == http.MethodGet:
		if o, ok := f.named[r.URL.Path+r.URL.Query().Get("name")]; ok {
			_ = enc.Encode(list(o))
			return
		}
		_ = enc.Encode(list())
	case strings.HasPrefix(r.URL.Path, "/api/v3/stages/") && r.Method == http.MethodPost:
		body["pk"] = "pk-" + body["name"].(string)
		f.named[r.URL.Path+body["name"].(string)] = body
		f.recoveryPosts++
		_ = enc.Encode(body)
	case r.URL.Path == "/api/v3/crypto/certificatekeypairs/":
		if f.noKey {
			_ = enc.Encode(list())
			return
		}
		_ = enc.Encode(list(map[string]any{"pk": "key-1", "name": r.URL.Query().Get("name")}))
	case r.URL.Path == "/api/v3/propertymappings/provider/scope/" && r.Method == http.MethodGet:
		if name := r.URL.Query().Get("name"); name != "" {
			if m, ok := f.mappings[name]; ok {
				_ = enc.Encode(list(m))
				return
			}
			_ = enc.Encode(list())
			return
		}
		_ = enc.Encode(list(
			map[string]any{"pk": "m-openid", "scope_name": "openid"},
			map[string]any{"pk": "m-email", "scope_name": "email"},
			map[string]any{"pk": "m-profile", "scope_name": "profile"},
			map[string]any{"pk": "m-other", "scope_name": "offline_access"},
		))
	case r.URL.Path == "/api/v3/propertymappings/provider/scope/" && r.Method == http.MethodPost:
		f.posts++
		body["pk"] = "m-" + body["name"].(string)
		f.mappings[body["name"].(string)] = body
		w.WriteHeader(http.StatusCreated)
		_ = enc.Encode(body)
	case strings.HasPrefix(r.URL.Path, "/api/v3/propertymappings/provider/scope/") && r.Method == http.MethodPatch:
		f.patches++
		for _, m := range f.mappings {
			if "/api/v3/propertymappings/provider/scope/"+m["pk"].(string)+"/" == r.URL.Path {
				for k, v := range body {
					m[k] = v
				}
			}
		}
		_ = enc.Encode(body)
	case strings.HasPrefix(r.URL.Path, "/api/v3/propertymappings/provider/scope/") && r.Method == http.MethodDelete:
		for n, m := range f.mappings {
			if "/api/v3/propertymappings/provider/scope/"+m["pk"].(string)+"/" == r.URL.Path {
				delete(f.mappings, n)
				f.deleted = append(f.deleted, "mapping "+n)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/api/v3/core/groups/" && r.Method == http.MethodGet:
		var out []any
		for _, g := range f.groups {
			if n := r.URL.Query().Get("name"); n == "" || n == g["name"] {
				out = append(out, g)
			}
		}
		_ = enc.Encode(map[string]any{"results": out, "pagination": map[string]any{"next": 0}})
	case r.URL.Path == "/api/v3/core/groups/" && r.Method == http.MethodPost:
		f.posts++
		n := body["name"].(string)
		g := map[string]any{"pk": "g-" + n, "name": n, "users": []any{}}
		f.groups[n] = g
		w.WriteHeader(http.StatusCreated)
		_ = enc.Encode(g)
	case strings.HasSuffix(r.URL.Path, "/add_user/") && r.Method == http.MethodPost:
		f.patches++
		for _, g := range f.groups {
			if "/api/v3/core/groups/"+g["pk"].(string)+"/add_user/" == r.URL.Path {
				g["users"] = append(g["users"].([]any), body["pk"])
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(r.URL.Path, "/api/v3/core/groups/") && r.Method == http.MethodDelete:
		for n, g := range f.groups {
			if "/api/v3/core/groups/"+g["pk"].(string)+"/" == r.URL.Path {
				delete(f.groups, n)
				f.deleted = append(f.deleted, "group "+n)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/api/v3/core/users/":
		var out []any
		for _, u := range f.users {
			out = append(out, u)
		}
		_ = enc.Encode(map[string]any{"results": out, "pagination": map[string]any{"next": 0}})
	case r.URL.Path == "/api/v3/policies/bindings/" && r.Method == http.MethodGet:
		var out []any
		for _, b := range append(append([]map[string]any{}, f.bindings...), f.policyBinds...) {
			if b["target"] == r.URL.Query().Get("target") {
				out = append(out, b)
			}
		}
		_ = enc.Encode(map[string]any{"results": out, "pagination": map[string]any{"next": 0}})
	case r.URL.Path == "/api/v3/policies/bindings/" && r.Method == http.MethodPost && body["policy"] != nil:
		f.policyBinds = append(f.policyBinds, body)
		w.WriteHeader(http.StatusCreated)
		_ = enc.Encode(body)
	case r.URL.Path == "/api/v3/policies/bindings/" && r.Method == http.MethodPost:
		f.posts++
		body["pk"] = fmt.Sprintf("b-%d", len(f.bindings))
		f.bindings = append(f.bindings, body)
		w.WriteHeader(http.StatusCreated)
		_ = enc.Encode(body)
	case r.URL.Path == "/api/v3/providers/oauth2/" && r.Method == http.MethodGet:
		if f.provider != nil && f.provider["name"] == r.URL.Query().Get("name") {
			_ = enc.Encode(list(f.provider))
			return
		}
		_ = enc.Encode(list())
	case r.URL.Path == "/api/v3/providers/oauth2/" && r.Method == http.MethodPost:
		f.posts++
		f.nextPK++
		body["pk"] = f.nextPK
		f.provider = body
		f.secretSet, _ = body["client_secret"].(string)
		w.WriteHeader(http.StatusCreated)
		_ = enc.Encode(body)
	case strings.HasPrefix(r.URL.Path, "/api/v3/providers/oauth2/") && r.Method == http.MethodPatch:
		f.patches++
		for k, v := range body {
			f.provider[k] = v
		}
		f.secretSet, _ = f.provider["client_secret"].(string)
		_ = enc.Encode(f.provider)
	case strings.HasPrefix(r.URL.Path, "/api/v3/core/applications/") && r.Method == http.MethodGet:
		if f.app == nil {
			http.NotFound(w, r)
			return
		}
		_ = enc.Encode(f.app)
	case r.URL.Path == "/api/v3/core/applications/" && r.Method == http.MethodPost:
		f.posts++
		body["pk"] = "app-" + fmt.Sprint(body["slug"])
		f.app = body
		w.WriteHeader(http.StatusCreated)
		_ = enc.Encode(body)
	case strings.HasPrefix(r.URL.Path, "/api/v3/core/applications/") && r.Method == http.MethodPatch:
		f.patches++
		for k, v := range body {
			f.app[k] = v
		}
		_ = enc.Encode(f.app)
	case strings.HasPrefix(r.URL.Path, "/api/v3/core/applications/") && r.Method == http.MethodDelete:
		f.app = nil
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(r.URL.Path, "/api/v3/providers/oauth2/") && r.Method == http.MethodDelete:
		f.provider = nil
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/api/v3/providers/proxy/" && r.Method == http.MethodGet:
		if f.proxy != nil && f.proxy["name"] == r.URL.Query().Get("name") {
			_ = enc.Encode(list(f.proxy))
			return
		}
		_ = enc.Encode(list())
	case r.URL.Path == "/api/v3/providers/proxy/" && r.Method == http.MethodPost:
		f.posts++
		f.nextPK++
		body["pk"] = f.nextPK
		f.proxy = body
		w.WriteHeader(http.StatusCreated)
		_ = enc.Encode(body)
	case strings.HasPrefix(r.URL.Path, "/api/v3/providers/proxy/") && r.Method == http.MethodPatch:
		f.patches++
		for k, v := range body {
			f.proxy[k] = v
		}
		_ = enc.Encode(f.proxy)
	case strings.HasPrefix(r.URL.Path, "/api/v3/providers/proxy/") && r.Method == http.MethodDelete:
		f.proxy = nil
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/api/v3/outposts/instances/" && r.Method == http.MethodGet:
		if f.noOutpost || r.URL.Query().Get("managed") != "goauthentik.io/outposts/embedded" {
			_ = enc.Encode(list())
			return
		}
		_ = enc.Encode(list(f.outpost))
	case strings.HasPrefix(r.URL.Path, "/api/v3/outposts/instances/") && r.Method == http.MethodPatch:
		f.patches++
		for k, v := range body {
			f.outpost[k] = v
		}
		_ = enc.Encode(f.outpost)
	default:
		http.NotFound(w, r)
	}
}

// planHarness points a manager at the fake Authentik and a fake IDP that
// shares its address (the issuer lives on the same server).
func planHarness(t *testing.T) (*harness, *fakeAuthentik) {
	h := newHarness(t)
	ak := newFakeAuthentik(t)
	h.m.Defaults = func() Defaults {
		return Defaults{AuthentikURL: ak.srv.URL, Origins: []string{"http://127.0.0.1:8080", "https://homestead.example.test"}}
	}
	return h, ak
}

func runDef(t *testing.T, h *harness) error {
	t.Helper()
	def, err := h.m.ApplyDefinition("owner")
	if err != nil {
		return err
	}
	if def.Type != "sso.apply" {
		t.Fatalf("operation type = %q", def.Type)
	}
	for _, step := range def.Steps {
		if err := step.Run(ctxb()); err != nil {
			return err
		}
	}
	return nil
}

func TestPlanListsObjectsRedirectURIsAndLeavesTheRest(t *testing.T) {
	h, ak := planHarness(t)
	plan, err := h.m.Plan(ctxb(), PlanRequest{}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Inspected || len(plan.Objects) != 4 || plan.Objects[0].Action != ActionUnknown || plan.Objects[2].Kind != "Recovery flow" || plan.Objects[2].Action != ActionUnknown {
		t.Fatalf("plan without a token must not claim to have read Authentik: %+v", plan.Objects)
	}
	want := []string{"http://127.0.0.1:8080/api/v1/sso/callback", "https://homestead.example.test/api/v1/sso/callback"}
	if strings.Join(plan.Spec.RedirectURIs, ",") != strings.Join(want, ",") {
		t.Fatalf("redirect URIs = %v", plan.Spec.RedirectURIs)
	}
	if plan.Spec.Issuer != ak.srv.URL+"/application/o/homestead/" || plan.Spec.AuthentikVersion != "2026.8.3" {
		t.Fatalf("spec = %+v", plan.Spec)
	}
	if len(plan.Untouched) == 0 || len(plan.Warnings) < 3 {
		t.Fatalf("the plan must say what it leaves alone and what the token can do: %+v", plan)
	}
	if len(ak.requests) != 0 {
		t.Fatalf("planning without a token contacted Authentik: %v", ak.requests)
	}
}

func TestPlanWithATokenInspectsAndIsIdempotent(t *testing.T) {
	h, ak := planHarness(t)
	plan, err := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Inspected || plan.Objects[0].Action != ActionCreate || plan.Objects[1].Action != ActionCreate {
		t.Fatalf("fresh Authentik must plan two creates: %+v", plan.Objects)
	}
	if h.secrets.has(APITokenName) {
		t.Fatal("planning stored the API token")
	}
	if err := h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := runDef(t, h); err != nil {
		t.Fatal(err)
	}
	if ak.posts != 4 || ak.patches != 0 {
		t.Fatalf("first apply: posts=%d patches=%d, want two groups, provider, application (no binding before a link)", ak.posts, ak.patches)
	}
	body, _ := json.Marshal(ak.provider)
	for _, must := range []string{`"client_type":"confidential"`, `"grant_types":["authorization_code"]`, `"matching_mode":"strict"`, `"signing_key":"key-1"`, `"sub_mode":"hashed_user_id"`,
		"http://127.0.0.1:8080/api/v1/sso/callback", `"client_id":"homestead"`} {
		if !strings.Contains(string(body), must) {
			t.Errorf("provider body missing %s: %s", must, body)
		}
	}
	if ak.app["slug"] != "homestead" || ak.app["provider"] == nil {
		t.Fatalf("application = %v", ak.app)
	}
	if ak.secretSet == "" || ak.secretSet != h.secretValue(ClientSecretName) {
		t.Fatal("the provider must carry the sealed client secret")
	}
	if h.secrets.has(APITokenName) {
		t.Fatal("the API token must be forgotten after apply by default")
	}
	st, _ := h.m.Status()
	if !st.Configured || st.State != "configured" || st.Spec.ClientID != ClientID {
		t.Fatalf("status = %+v", st)
	}
	if _, err := os.Stat(h.m.YAMLPath); err != nil {
		t.Fatalf("YAML mirror missing: %v", err)
	}

	// Re-plan: the existing objects are detected and nothing would change.
	plan2, err := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range plan2.Objects {
		if o.Action != ActionUnchanged {
			t.Fatalf("re-plan %s = %s %v, want unchanged", o.Kind, o.Action, o.Changes)
		}
	}
	if err := h.m.Approve(ApproveRequest{Digest: plan2.Digest, APIToken: apiToken}, "owner"); err != nil {
		t.Fatal(err)
	}
	posts := ak.posts
	if err := runDef(t, h); err != nil {
		t.Fatal(err)
	}
	if ak.posts != posts || ak.patches != 0 {
		t.Fatalf("re-apply changed Authentik: posts %d->%d patches=%d", posts, ak.posts, ak.patches)
	}
}

func (h *harness) secretValue(name string) string { v, _ := h.secrets.Get(name); return v }

func TestReplanDetectsDriftAndApplyRepairsIt(t *testing.T) {
	h, ak := planHarness(t)
	plan, _ := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner")
	_ = h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner")
	if err := runDef(t, h); err != nil {
		t.Fatal(err)
	}
	// Someone edits the provider in Authentik: a loose redirect URI and a
	// public client.
	ak.provider["redirect_uris"] = []any{map[string]any{"matching_mode": "regex", "url": ".*"}}
	ak.provider["client_type"] = "public"
	ak.provider["grant_types"] = []any{}
	plan, err := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	o := plan.Objects[0]
	if o.Action != ActionUpdate || !contains(o.Changes, "redirect URIs") || !contains(o.Changes, "client type becomes confidential") || !contains(o.Changes, "grant types") {
		t.Fatalf("drift not shown: %+v", o)
	}
	for _, c := range o.Changes {
		if strings.Contains(c, h.secretValue(ClientSecretName)) {
			t.Fatal("the secret leaked into the plan")
		}
	}
	_ = h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner")
	if err := runDef(t, h); err != nil {
		t.Fatal(err)
	}
	if ak.patches != 1 || ak.provider["client_type"] != "confidential" || fmt.Sprint(ak.provider["grant_types"]) != "[authorization_code]" {
		t.Fatalf("apply did not repair: patches=%d %v %v", ak.patches, ak.provider["client_type"], ak.provider["grant_types"])
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestApprovalIsBoundToTheDigest(t *testing.T) {
	h, _ := planHarness(t)
	plan, _ := h.m.Plan(ctxb(), PlanRequest{}, "owner")
	if err := h.m.Approve(ApproveRequest{Digest: "deadbeef", APIToken: apiToken}, "owner"); !errors.Is(err, ErrDigest) {
		t.Fatalf("wrong digest = %v, want ErrDigest", err)
	}
	if _, err := h.m.ApplyDefinition("owner"); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("apply before approval = %v", err)
	}
	if err := h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner"); err != nil {
		t.Fatal(err)
	}
	// A new plan (different origins) voids the approval of the old one.
	plan2, err := h.m.Plan(ctxb(), PlanRequest{Origins: []string{"https://other.example.test"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if plan2.Digest == plan.Digest {
		t.Fatal("different origins must change the digest")
	}
	if _, err := h.m.ApplyDefinition("owner"); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("apply after re-plan = %v, want ErrNotApproved", err)
	}
	if err := h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner"); !errors.Is(err, ErrDigest) {
		t.Fatalf("approving the superseded digest = %v", err)
	}
}

func TestApprovalNeedsAToken(t *testing.T) {
	h, _ := planHarness(t)
	plan, _ := h.m.Plan(ctxb(), PlanRequest{}, "owner")
	if err := h.m.Approve(ApproveRequest{Digest: plan.Digest}, "owner"); !errors.Is(err, ErrInput) {
		t.Fatalf("approve without token = %v", err)
	}
}

func TestPlanExpires(t *testing.T) {
	h, _ := planHarness(t)
	plan, _ := h.m.Plan(ctxb(), PlanRequest{}, "owner")
	h.now = h.now.Add(PlanTTL + 1)
	if err := h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner"); !errors.Is(err, ErrNoPlan) {
		t.Fatalf("expired plan = %v", err)
	}
}

func TestKeepTokenIsAnOptIn(t *testing.T) {
	h, _ := planHarness(t)
	plan, _ := h.m.Plan(ctxb(), PlanRequest{}, "owner")
	if err := h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken, KeepToken: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := runDef(t, h); err != nil {
		t.Fatal(err)
	}
	if !h.secrets.has(APITokenName) {
		t.Fatal("the token was not kept though the owner opted in")
	}
	st, _ := h.m.Status()
	if !st.TokenKept {
		t.Fatalf("status must say the token is kept: %+v", st)
	}
}

func TestFailedApplyKeepsTheTokenForARetry(t *testing.T) {
	h, ak := planHarness(t)
	ak.noKey = true
	plan, _ := h.m.Plan(ctxb(), PlanRequest{}, "owner")
	_ = h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner")
	if err := runDef(t, h); err == nil || !strings.Contains(err.Error(), "signing key") {
		t.Fatalf("apply with no signing key = %v", err)
	}
	if !h.secrets.has(APITokenName) {
		t.Fatal("a failed apply must keep the sealed token so the owner can retry")
	}
	if st, _ := h.m.Status(); st.Configured {
		t.Fatal("a failed apply must not mark SSO configured")
	}
}

func TestBadTokenIsExplainedAndNothingIsStored(t *testing.T) {
	h, ak := planHarness(t)
	ak.badToken = true
	_, err := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner")
	if err == nil || !strings.Contains(err.Error(), "refused the API token") {
		t.Fatalf("bad token plan = %v", err)
	}
	if strings.Contains(err.Error(), apiToken) {
		t.Fatal("error leaked the token")
	}
}

func TestSecretsNeverAppearInPlansAuditOrStatus(t *testing.T) {
	h, _ := planHarness(t)
	plan, _ := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner")
	_ = h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner")
	if err := runDef(t, h); err != nil {
		t.Fatal(err)
	}
	secret := h.secretValue(ClientSecretName)
	if secret == "" {
		t.Fatal("no client secret sealed")
	}
	planJSON, _ := json.Marshal(plan)
	st, _ := h.m.Status()
	stJSON, _ := json.Marshal(st)
	yaml, _ := os.ReadFile(h.m.YAMLPath)
	row := ""
	_ = h.st.DB().QueryRow(`SELECT spec FROM sso_config`).Scan(&row)
	blobs := map[string]string{
		"plan": string(planJSON), "status": string(stJSON), "audit": strings.Join(h.audits, "\n"),
		"yaml": string(yaml), "config row": row,
	}
	for name, blob := range blobs {
		for _, s := range []string{secret, apiToken} {
			if strings.Contains(blob, s) {
				t.Errorf("%s contains a secret", name)
			}
		}
	}
	if len(h.audits) == 0 {
		t.Fatal("expected audit entries")
	}
}

func TestVersionMismatchIsNotedNotFatal(t *testing.T) {
	h, ak := planHarness(t)
	ak.version = "2027.2.1"
	plan, _ := h.m.Plan(ctxb(), PlanRequest{}, "owner")
	_ = h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner")
	if err := runDef(t, h); err != nil {
		t.Fatalf("a newer Authentik must still apply: %v", err)
	}
	if !strings.Contains(strings.Join(h.audits, "\n"), "catalog pins 2026.8.3") {
		t.Fatalf("version mismatch not noted: %v", h.audits)
	}
}

func TestPlanNeedsAnAddress(t *testing.T) {
	h := newHarness(t)
	if _, err := h.m.Plan(ctxb(), PlanRequest{}, "owner"); !errors.Is(err, ErrInput) {
		t.Fatalf("plan without an address = %v", err)
	}
	if _, err := h.m.Plan(ctxb(), PlanRequest{AuthentikURL: "http://authentik.lan", Origins: []string{"http://127.0.0.1:8080"}}, "owner"); !errors.Is(err, ErrInput) {
		t.Fatalf("plain http Authentik = %v", err)
	}
	if _, err := h.m.Plan(ctxb(), PlanRequest{AuthentikURL: "https://authentik.lan", Origins: []string{"http://homestead.lan"}}, "owner"); !errors.Is(err, ErrInput) {
		t.Fatalf("plain http dashboard origin = %v", err)
	}
}

func TestApplyThenLoginEndToEnd(t *testing.T) {
	// The issuer the plan computes is where the fake provider answers when
	// Authentik and the IDP share a host: apply verifies discovery.
	h := newHarness(t)
	ak := newFakeAuthentik(t)
	// Serve the IDP routes on the Authentik fake's host by proxying: simplest
	// is to point both at the IDP server and let the admin API live on it
	// too, so give the IDP server the Authentik handler for /api/v3.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/", ak.serve)
	mux.HandleFunc("/", h.idp.serve)
	combined := httptest.NewServer(mux)
	t.Cleanup(combined.Close)
	h.idp.srv = combined
	h.m.Defaults = func() Defaults {
		return Defaults{AuthentikURL: combined.URL, Origins: []string{"http://127.0.0.1:8080"}}
	}
	plan, err := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := runDef(t, h); err != nil {
		t.Fatal(err)
	}
	// The generated secret is what the provider was configured with; the
	// fake token endpoint expects a fixed secret, so adopt the generated one.
	h.idp.secretOvr = h.secretValue(ClientSecretName)
	if h.idp.secretOvr == "" {
		t.Fatal("apply sealed no client secret")
	}
	h.link()
	code, state := h.startLogin()
	out, err := h.m.Callback(ctxb(), state, code, "", state)
	if err != nil || out.IdentityID != 7 {
		t.Fatalf("login with the generated secret: %+v %v", out, err)
	}
}

func TestApplyFailsWhenTheIssuerDoesNotAnswer(t *testing.T) {
	h, ak := planHarness(t)
	ak.noDiscovery = true
	plan, _ := h.m.Plan(ctxb(), PlanRequest{}, "owner")
	_ = h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner")
	if err := runDef(t, h); err == nil {
		t.Fatal("apply must fail when the configured issuer does not publish discovery")
	}
	if st, _ := h.m.Status(); st.Configured {
		t.Fatal("a failed verification must not mark SSO configured")
	}
}

func TestRemoveDeletesOnlyWhatReconcileCreated(t *testing.T) {
	ak := newFakeAuthentik(t)
	api := &API{Base: ak.srv.URL, Token: apiToken, Client: func() *http.Client { return ak.srv.Client() }}
	spec := Spec{ProviderName: "Homestead: Immich", ClientID: "homestead-immich", AppName: "Immich", AppSlug: "homestead-immich",
		RedirectURIs: []string{"https://immich.lan/auth/login"}, AuthorizationFlow: DefaultAuthorizationFlow,
		InvalidationFlow: DefaultInvalidationFlow, SigningKey: DefaultSigningKey, Scopes: Scopes, SubMode: "hashed_user_id"}
	noop := func(string) {}
	if err := api.Reconcile(t.Context(), spec, testSecret, noop); err != nil {
		t.Fatal(err)
	}
	var steps []string
	if err := api.Remove(t.Context(), spec, func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if ak.app != nil || ak.provider != nil || strings.Join(steps, ",") != "deleted the application,deleted the provider" {
		t.Fatalf("after remove: app=%v provider=%v steps=%v", ak.app, ak.provider, steps)
	}
	steps = nil
	if err := api.Remove(t.Context(), spec, func(s string) { steps = append(steps, s) }); err != nil || len(steps) != 2 {
		t.Fatalf("second remove: %v %v", steps, err)
	}
	// A provider with the same name but another client is not ours.
	ak.provider = map[string]any{"pk": 7, "name": spec.ProviderName, "client_id": "someone-else"}
	if err := api.Remove(t.Context(), spec, noop); err == nil || ak.provider == nil {
		t.Fatalf("foreign provider removed: %v", err)
	}
}
