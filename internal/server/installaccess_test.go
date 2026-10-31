package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/appaccess"
	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/people"
	"homestead/internal/sso"
)

func TestInstallAccessRemoteIdentityRejectsBeforeApprovalOrGrant(t *testing.T) {
	a := installHandlerRig(t)
	const pk = 100
	if _, err := a.st.DB().Exec(`INSERT INTO people (id, display_name, username, authentik_pk, authentik_uid, state, created_by, created_at) VALUES (1, 'Ada', 'ada', ?, 'u100', 'active', 'owner', '2026-01-01T00:00:00Z')`, pk); err != nil {
		t.Fatal(err)
	}
	a.w.mu.Lock()
	a.w.users = map[int]map[string]any{pk: {"pk": pk, "username": "ada", "name": "Ada", "uid": "u100", "is_active": true, "type": "internal", "attributes": map[string]any{sso.ManagedAttribute: true}}}
	for _, group := range []string{"homestead-immich-users", "homestead-immich-admins"} {
		a.w.groups[group] = map[string]any{"pk": "g-" + group, "name": group, "users": []any{}}
	}
	a.w.mu.Unlock()
	// The install review requires fresh app groups. The setup grant path
	// below uses these groups, so remove them while reviewing.
	a.w.mu.Lock()
	delete(a.w.groups, "homestead-immich-users")
	delete(a.w.groups, "homestead-immich-admins")
	a.w.mu.Unlock()
	post := func(path string, body map[string]any) (int, string) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := a.req(http.MethodPost, path, string(raw), a.owner.token)
		return r.Code, r.Body.String()
	}
	choices := []appaccess.Choice{{PersonID: 1, Role: appaccess.User}}
	base := installMatrixBody()
	base["choices"] = choices
	code, body := post("/api/v1/catalog/immich/access-plan", base)
	if code != http.StatusOK {
		t.Fatalf("valid access review = %d %s", code, body)
	}
	var reviewed struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(body), &reviewed); err != nil {
		t.Fatal(err)
	}
	if reviewed.Digest == "" {
		t.Fatal("missing access digest")
	}
	for _, tc := range []struct {
		name   string
		change func()
	}{
		{"disabled", func() { a.w.users[pk]["is_active"] = false }},
		{"missing", func() { delete(a.w.users, pk) }},
		{"uid mismatch", func() { a.w.users[pk]["uid"] = "different-uid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.w.mu.Lock()
			tc.change()
			a.w.mu.Unlock()
			if code, body := post("/api/v1/catalog/immich/access-plan", base); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Cannot verify") && !strings.Contains(body, "cannot verify") {
				t.Fatalf("remote drift review = %d %s", code, body)
			}
			if n := installOperationCount(t, a); n != 0 {
				t.Fatalf("install operation after drift: %d", n)
			}
			a.w.mu.Lock()
			a.w.users[pk] = map[string]any{"pk": pk, "username": "ada", "name": "Ada", "uid": "u100", "is_active": true, "type": "internal", "attributes": map[string]any{sso.ManagedAttribute: true}}
			a.w.mu.Unlock()
		})
	}
	// A changed remote UID after a valid review also invalidates the digest
	// if the local link is deliberately updated to that new identity.
	a.w.mu.Lock()
	a.w.users[pk]["uid"] = "new-uid"
	a.w.mu.Unlock()
	if _, err := a.st.DB().Exec(`UPDATE people SET authentik_uid = 'new-uid' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	code, body = post("/api/v1/catalog/immich/access-plan", base)
	if code != http.StatusOK {
		t.Fatalf("new linked identity = %d %s", code, body)
	}
	var changed struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(body), &changed); err != nil {
		t.Fatal(err)
	}
	if changed.Digest == reviewed.Digest {
		t.Fatal("remote identity did not change approval")
	}
	code, body = post("/api/v1/catalog/immich/plan", installMatrixBody())
	if code != http.StatusOK {
		t.Fatalf("install review = %d %s", code, body)
	}
	var installReview struct {
		Digest string `json:"digest"`
		SignIn struct {
			Plan struct {
				Digest string `json:"digest"`
			} `json:"plan"`
		} `json:"sign_in"`
	}
	if err := json.Unmarshal([]byte(body), &installReview); err != nil {
		t.Fatal(err)
	}
	stale := installMatrixBody()
	stale["digest"] = installReview.Digest
	stale["setup"] = map[string]any{"sign_in": map[string]any{"digest": installReview.SignIn.Plan.Digest}, "access": map[string]any{"digest": reviewed.Digest, "choices": choices}}
	if code, body := post("/api/v1/catalog/immich/install", stale); code != http.StatusConflict || !strings.Contains(body, "decision changed") {
		t.Fatalf("stale access approval = %d %s", code, body)
	}

	// Setup uses the actual grant step. Restore the reviewed identity and
	// simulate each remote change after its digest has been prepared.
	a.w.mu.Lock()
	a.w.users[pk]["uid"] = "u100"
	a.w.mu.Unlock()
	if _, err := a.st.DB().Exec(`UPDATE people SET authentik_uid = 'u100' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	// A second selected person makes the roster check prove that a bad later
	// entry blocks the first grant before any group changes.
	if _, err := a.st.DB().Exec(`INSERT INTO people (id, display_name, username, authentik_pk, authentik_uid, state, created_by, created_at) VALUES (2, 'Ben', 'ben', 101, 'u101', 'active', 'owner', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	a.w.mu.Lock()
	a.w.users[101] = map[string]any{"pk": 101, "username": "ben", "name": "Ben", "uid": "u101", "is_active": true, "type": "internal", "attributes": map[string]any{sso.ManagedAttribute: true}}
	a.w.mu.Unlock()
	grantChoices := []appaccess.Choice{{PersonID: 1, Role: appaccess.User}, {PersonID: 2, Role: appaccess.User}}
	plan, err := a.srv.appSetup.verifiedAccessGrantPlan(context.Background(), "immich", grantChoices)
	if err != nil {
		t.Fatal(err)
	}
	if err := (appsso.Records{DB: a.st.DB()}).Save(appsso.Record{Package: "immich", Adapter: catalog.AdapterImmich, ClientID: "homestead-immich", Issuer: "x"}); err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"homestead-immich-users", "homestead-immich-admins"} {
		a.w.mu.Lock()
		a.w.groups[group] = map[string]any{"pk": "g-" + group, "name": group, "users": []any{}}
		a.w.mu.Unlock()
	}
	for _, tc := range []struct {
		name   string
		change func()
	}{
		{"disabled", func() { a.w.users[101]["is_active"] = false }},
		{"missing", func() { delete(a.w.users, 101) }},
		{"uid mismatch", func() { a.w.users[101]["uid"] = "different-uid" }},
	} {
		t.Run("setup "+tc.name, func(t *testing.T) {
			a.w.mu.Lock()
			tc.change()
			a.w.mu.Unlock()
			rec := setupRecord{App: "immich", AccessManaged: true, AccessChoices: grantChoices, Actor: "owner"}
			if _, err := a.srv.appSetup.startStep(context.Background(), rec, setupPeople, plan.Digest, ""); err == nil {
				t.Fatal("grant step accepted remote drift")
			}
			if _, err := a.srv.people.Grant(context.Background(), "owner", 2, people.Grant{App: "immich", Role: people.RoleUser}); err == nil {
				t.Fatal("individual grant accepted remote drift")
			}
			var grants int
			if err := a.st.DB().QueryRow(`SELECT COUNT(*) FROM people_grants WHERE app = 'immich'`).Scan(&grants); err != nil {
				t.Fatal(err)
			}
			a.w.mu.Lock()
			if grants != 0 || len(a.w.groups["homestead-immich-users"]["users"].([]any)) != 0 {
				t.Fatal("grant mutation before rejection")
			}
			a.w.users[101] = map[string]any{"pk": 101, "username": "ben", "name": "Ben", "uid": "u101", "is_active": true, "type": "internal", "attributes": map[string]any{sso.ManagedAttribute: true}}
			a.w.mu.Unlock()
		})
	}
	// Unrelated profile drift does not block a linked, active account.
	a.w.mu.Lock()
	a.w.users[pk]["name"] = "New display name"
	a.w.mu.Unlock()
	rec := setupRecord{App: "immich", AccessManaged: true, AccessChoices: grantChoices, Actor: "owner"}
	if _, err := a.srv.appSetup.startStep(context.Background(), rec, setupPeople, plan.Digest, ""); err != nil {
		t.Fatalf("valid linked grant: %v", err)
	}
}

func TestInstallAccessAllowsExplicitNoneForLocallyDisabledPerson(t *testing.T) {
	a := installHandlerRig(t)
	if _, err := a.st.DB().Exec(`INSERT INTO people (id, display_name, username, authentik_pk, authentik_uid, state, created_by, created_at) VALUES (1, 'Ada', 'ada', 100, 'u100', 'disabled', 'owner', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// Removal in Authentik does not turn a local disabled person into an
	// implicit grant or erase their explicit none choice.
	body := installMatrixBody()
	body["choices"] = []appaccess.Choice{{PersonID: 1, Role: appaccess.None}}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp := a.req(http.MethodPost, "/api/v1/catalog/immich/access-plan", string(raw), a.owner.token)
	if resp.Code != http.StatusOK {
		t.Fatalf("disabled none review = %d %s", resp.Code, resp.Body)
	}
	var reviewed struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &reviewed); err != nil || reviewed.Digest == "" {
		t.Fatalf("disabled review: %v %s", err, resp.Body)
	}
	if _, err := a.srv.appSetup.verifiedAccessGrantPlan(context.Background(), "immich", []appaccess.Choice{{PersonID: 1, Role: appaccess.None}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.DB().Exec(`UPDATE people SET state = 'active' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	resp = a.req(http.MethodPost, "/api/v1/catalog/immich/access-plan", string(raw), a.owner.token)
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("active person without remote identity = %d %s", resp.Code, resp.Body)
	}
}

func TestInstallAccessClassificationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		oidc       *catalog.OIDCAdapter
	}{
		{"wordcraft", "open", nil},
		{"authentik", "bootstrap", nil},
		{"audiobookshelf", "self-managed", nil},
		{"unknown", "self-managed", nil},
		{"immich", "managed", &catalog.OIDCAdapter{Adapter: catalog.AdapterImmich}},
		{"unknown", "unavailable", &catalog.OIDCAdapter{Adapter: "unknown"}},
	} {
		m := catalog.Manifest{Name: tc.name}
		m.SSO.OIDC = tc.oidc
		if got := installAccessMode(m); got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestAccessGrantPlanBindsEntireRosterAndRole(t *testing.T) {
	a := installHandlerRig(t)
	_, err := a.st.DB().Exec(`INSERT INTO people (id, display_name, username, authentik_pk, authentik_uid, state, created_by, created_at) VALUES (1, 'Person', 'person', 100, 'u100', 'active', 'owner', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	a.w.mu.Lock()
	a.w.users = map[int]map[string]any{100: {"pk": 100, "username": "person", "uid": "u100", "is_active": true, "type": "internal", "attributes": map[string]any{sso.ManagedAttribute: true}}}
	a.w.mu.Unlock()
	user, err := a.srv.appSetup.verifiedAccessGrantPlan(context.Background(), "immich", []appaccess.Choice{{PersonID: 1, Role: appaccess.User}})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := a.srv.appSetup.verifiedAccessGrantPlan(context.Background(), "immich", []appaccess.Choice{{PersonID: 1, Role: appaccess.Admin}})
	if err != nil || user.Digest == admin.Digest {
		t.Fatalf("role not bound: %v", err)
	}
	_, err = a.st.DB().Exec(`INSERT INTO people (id, display_name, username, authentik_pk, authentik_uid, state, created_by, created_at) VALUES (2, 'Other', 'other', 101, 'u101', 'active', 'owner', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	a.w.mu.Lock()
	a.w.users[101] = map[string]any{"pk": 101, "username": "other", "uid": "u101", "is_active": true, "type": "internal", "attributes": map[string]any{sso.ManagedAttribute: true}}
	a.w.mu.Unlock()
	if _, err := a.srv.appSetup.verifiedAccessGrantPlan(context.Background(), "immich", []appaccess.Choice{{PersonID: 1, Role: appaccess.User}}); err == nil {
		t.Fatal("new person must invalidate selection")
	}
}

func TestAccessSetupSurvivesRestartWithOwnerOnly(t *testing.T) {
	rec := setupRecord{App: "immich", AccessManaged: true, AccessChoices: []appaccess.Choice{}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var restored setupRecord
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.AccessManaged {
		t.Fatal("owner-only access must remain a managed setup after restart")
	}
	var legacy setupRecord
	if err := json.Unmarshal([]byte(`{"app":"immich","people":[1]}`), &legacy); err != nil || legacy.AccessManaged || len(legacy.People) != 1 {
		t.Fatalf("legacy setup changed: %#v %v", legacy, err)
	}
}

func TestAdminInstallUsesStepUpAndCannotUseOrdinaryPath(t *testing.T) {
	pattern := "POST /api/v1/catalog/{name}/install-admin"
	if stepUpRoutes[pattern] != stepUpBoundary || !publicStepUpRoutes[pattern] || routeClasses[pattern] != ClassSession {
		t.Fatal("administrator install must be an owner-session step-up route on both local and public dashboard")
	}
	choices := []appaccess.Choice{{PersonID: 1, Role: appaccess.Admin}}
	if err := validateInstallAdmin(choices, false); err == nil {
		t.Fatal("ordinary install accepted an admin")
	}
	if err := validateInstallAdmin(choices, true); err != nil {
		t.Fatal(err)
	}
}

func TestInstallHandlersRejectAdminChoiceForOpenAppBeforeEffects(t *testing.T) {
	srv, _ := catalogTestServer(t)
	body := installTestBody()
	body["setup"] = map[string]any{"access": map[string]any{"digest": "stale", "choices": []map[string]any{{"person_id": 1, "role": "admin"}}}}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/install", body); rec.Code != http.StatusForbidden {
		t.Fatalf("ordinary install accepted admin choice: %d %s", rec.Code, rec.Body.String())
	}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/install-admin", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("open app accepted managed access choice: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminInstallRequiresFreshAuthentication(t *testing.T) {
	a := peopleRig(t, true)
	a.linkOwner(t)
	token, csrf := a.ssoSession(t)
	cookie := cookieAuth(token, csrf)
	path := "/api/v1/catalog/immich/install-admin"
	if rec := a.req("POST", path, `{}`, "", cookie); rec.Code != 403 || rec.Header().Get(ErrorCodeHeader) != ErrorCodeReauthRequired {
		t.Fatalf("without step-up = %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("POST", path, `{}`, "", cookie, reauth(ownerPassword)); rec.Code == 403 {
		t.Fatalf("valid local step-up was not accepted: %d %s", rec.Code, rec.Body)
	}
}

func TestInstallAccessRefusesExistingAuthentikGroups(t *testing.T) {
	a := newSSOApp(t)
	a.configureKeeping(t, true)
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	p := catalog.InstallPlan{Package: "jellyfin", Routes: []docker.Route{{Host: "jellyfin.lan", Target: docker.Target{Host: "jellyfin", Port: 8096}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}}}
	v, _, err := a.srv.installAccess(context.Background(), a.st, m, p, "")
	if err != nil || v.Mode != "unavailable" {
		t.Fatalf("unlinked owner = %#v %v", v, err)
	}
	a.linkOwner(t)
	v, _, err = a.srv.installAccess(context.Background(), a.st, m, p, "")
	if err != nil || v.Mode != "managed" {
		t.Fatalf("fresh = %#v %v", v, err)
	}
	a.w.mu.Lock()
	a.w.groups["homestead-jellyfin-users"] = map[string]any{"pk": "existing", "name": "homestead-jellyfin-users", "users": []any{42}}
	a.w.mu.Unlock()
	v, _, err = a.srv.installAccess(context.Background(), a.st, m, p, "")
	if err != nil || v.Mode != "unavailable" {
		t.Fatalf("old group = %#v %v", v, err)
	}
}
