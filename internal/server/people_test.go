package server

import (
	"encoding/json"
	"strings"
	"testing"

	"homestead/internal/appsso"
	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/people"
)

// peopleRig is the SSO rig with the Authentik token kept and Immich signed
// in, so its groups exist.
func peopleRig(t *testing.T, keep bool) *ssoApp {
	t.Helper()
	a := newSSOApp(t)
	a.configureKeeping(t, keep)
	if err := (appsso.Records{DB: a.st.DB()}).Save(appsso.Record{Package: "immich", Adapter: catalog.AdapterImmich, ClientID: "homestead-immich", Issuer: "x"}); err != nil {
		t.Fatal(err)
	}
	a.w.mu.Lock()
	for _, n := range []string{"homestead-immich-users", "homestead-immich-admins", "homestead-owner"} {
		a.w.groups[n] = map[string]any{"pk": "g-" + n, "name": n, "users": []any{}}
	}
	a.w.mu.Unlock()
	return a
}

func TestPeopleWithoutAKeptTokenExplainsAndOffersTheOptIn(t *testing.T) {
	a := peopleRig(t, false)
	rec := a.req("GET", "/api/v1/people", "", a.owner.token)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	v := decodeAs[peopleView](t, rec.Body.Bytes())
	if v.TokenKept || v.Reason != "no-token" || !strings.Contains(v.TokenWords, "can do anything an Authentik administrator can") {
		t.Fatalf("view: %+v", v)
	}
	rec = a.req("POST", "/api/v1/people", `{"confirm":true,"display_name":"Ada","username":"ada"}`, a.owner.token)
	if rec.Code != 422 || rec.Header().Get(ErrorCodeHeader) != "people-no-token" ||
		!strings.Contains(rec.Body.String(), "keep-token") || !strings.Contains(rec.Body.String(), "anything an Authentik administrator can") {
		t.Fatalf("add without token: %d %s", rec.Code, rec.Body)
	}
	if len(a.w.users) != 0 {
		t.Fatal("created a user without a token")
	}
}

func TestPeopleBeforeSignInIsSetUpSaysSo(t *testing.T) {
	a := newSSOApp(t)
	v := decodeAs[peopleView](t, a.req("GET", "/api/v1/people", "", a.owner.token).Body.Bytes())
	if v.Reason != "no-sign-in" {
		t.Fatalf("reason: %q", v.Reason)
	}
}

func TestPeopleEveryChangeNeedsConfirm(t *testing.T) {
	a := peopleRig(t, true)
	for _, rt := range [][2]string{
		{"POST", "/api/v1/people"}, {"POST", "/api/v1/people/adopt"}, {"PATCH", "/api/v1/people/1"},
		{"POST", "/api/v1/people/1/password-reset"}, {"POST", "/api/v1/people/1/mfa-reset"},
		{"POST", "/api/v1/people/1/disable"}, {"POST", "/api/v1/people/1/enable"}, {"POST", "/api/v1/people/1/remove"},
		{"PUT", "/api/v1/people/1/apps/immich"}, {"PUT", "/api/v1/people/1/apps/immich/admin"},
		{"POST", "/api/v1/people/1/apps/immich/revoke"}, {"POST", "/api/v1/people/1/resolve"},
	} {
		if rec := a.req(rt[0], rt[1], `{"confirm":false}`, a.owner.token); rec.Code != 400 || !strings.Contains(rec.Body.String(), "confirm") {
			t.Errorf("%s %s without confirm = %d %s", rt[0], rt[1], rec.Code, rec.Body)
		}
	}
	if len(a.w.users) != 0 {
		t.Fatal("an unconfirmed request changed Authentik")
	}
}

func TestPeopleRoutesNeedASession(t *testing.T) {
	a := peopleRig(t, true)
	if rec := a.req("GET", "/api/v1/people", "", ""); rec.Code != 401 {
		t.Fatalf("list without a session = %d", rec.Code)
	}
	if rec := a.req("POST", "/api/v1/people", `{"confirm":true}`, ""); rec.Code != 401 {
		t.Fatalf("add without a session = %d", rec.Code)
	}
}

func TestPeopleLifecycleThroughTheAPI(t *testing.T) {
	a := peopleRig(t, true)
	a.linkOwner(t)
	rec := a.req("POST", "/api/v1/people", `{"confirm":true,"display_name":"Ada","username":"ada","email":"ada@example.test","method":"link"}`, a.owner.token)
	if rec.Code != 200 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var added struct {
		Person   people.Person
		Revealed *people.Revealed
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil || added.Revealed == nil || added.Revealed.Kind != "link" {
		t.Fatalf("add body: %v %s", err, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the reveal may be cached")
	}
	id := added.Person.ID
	base := "/api/v1/people/" + itoa(id)
	// A user grant, then an admin grant; the owner is in the user's own view.
	if rec := a.req("PUT", base+"/apps/immich", `{"confirm":true}`, a.owner.token); rec.Code != 200 {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("PUT", base+"/apps/immich/admin", `{"confirm":true}`, a.owner.token); rec.Code != 200 {
		t.Fatalf("admin grant: %d %s", rec.Code, rec.Body)
	}
	v := decodeAs[peopleView](t, a.req("GET", "/api/v1/people", "", a.owner.token).Body.Bytes())
	if !v.TokenKept || v.Reason != "" || len(v.People) != 1 || v.People[0].Status != "ok" || v.People[0].Grants[0].Role != "admin" || v.Owner == "" {
		t.Fatalf("view: %+v", v)
	}
	if len(v.Unmanaged) != 0 {
		t.Fatalf("the owner's Authentik user is listed as unmanaged: %+v", v.Unmanaged)
	}
	if rec := a.req("POST", base+"/password-reset", `{"confirm":true,"method":"generated"}`, a.owner.token); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"password"`) {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("POST", base+"/apps/immich/revoke", `{"confirm":true}`, a.owner.token); rec.Code != 200 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("POST", base+"/remove", `{"confirm":true}`, a.owner.token); rec.Code != 200 || !strings.Contains(rec.Body.String(), "App data stays") {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("POST", base+"/remove", `{"confirm":true}`, a.owner.token); rec.Code != 404 {
		t.Fatalf("remove again: %d", rec.Code)
	}
	if rec := a.req("POST", "/api/v1/people/abc/remove", `{"confirm":true}`, a.owner.token); rec.Code != 404 {
		t.Fatalf("bad id: %d", rec.Code)
	}
	// Audited by name, with no secret, link or token.
	entries, err := audit.New(a.st).List(500)
	if err != nil {
		t.Fatal(err)
	}
	var dump strings.Builder
	seen := map[string]bool{}
	for _, e := range entries {
		dump.WriteString(e.Action + " " + e.Detail + "\n")
		seen[e.Action] = true
	}
	for _, act := range []string{"people.add", "people.grant", "people.password-reset", "people.revoke", "people.remove"} {
		if !seen[act] {
			t.Errorf("no audit entry for %s", act)
		}
	}
	for _, leak := range []string{"one-time-token", ssoToken, "recover/"} {
		if strings.Contains(dump.String(), leak) {
			t.Fatalf("audit holds %q", leak)
		}
	}
}

func TestPeopleStepUpForOIDCSessionsButNotPasswordSessions(t *testing.T) {
	a := peopleRig(t, true)
	a.linkOwner(t)
	add := a.req("POST", "/api/v1/people", `{"confirm":true,"display_name":"Ada","username":"ada"}`, a.owner.token)
	if add.Code != 200 {
		t.Fatalf("add: %d %s", add.Code, add.Body)
	}
	token, csrf := a.ssoSession(t)
	cookie := cookieAuth(token, csrf)
	// A plain user grant needs no fresh password; the admin grant does.
	if rec := a.req("PUT", "/api/v1/people/1/apps/immich", `{"confirm":true}`, "", cookie); rec.Code != 200 {
		t.Fatalf("user grant from an oidc session: %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("PUT", "/api/v1/people/1/apps/immich/admin", `{"confirm":true}`, "", cookie); rec.Code != 403 || rec.Header().Get(ErrorCodeHeader) != ErrorCodeReauthRequired {
		t.Fatalf("admin grant without the password: %d", rec.Code)
	}
	if rec := a.req("PUT", "/api/v1/people/1/apps/immich/admin", `{"confirm":true}`, "", cookie, reauth(ownerPassword)); rec.Code != 200 {
		t.Fatalf("admin grant with the password: %d %s", rec.Code, rec.Body)
	}
	// A password session is not stepped up.
	if rec := a.req("POST", "/api/v1/people/1/mfa-reset", `{"confirm":true}`, a.owner.token); rec.Code == 403 {
		t.Fatalf("password session was stepped up: %d", rec.Code)
	}
}

func TestPeopleNeverTouchesAGroupOutsideThePrefix(t *testing.T) {
	a := peopleRig(t, true)
	a.w.groups["family-photos"] = map[string]any{"pk": "g-family-photos", "name": "family-photos", "users": []any{}}
	a.req("POST", "/api/v1/people", `{"confirm":true,"display_name":"Ada","username":"ada","grants":[{"app":"immich","role":"admin"}]}`, a.owner.token)
	a.req("POST", "/api/v1/people/1/remove", `{"confirm":true}`, a.owner.token)
	for _, c := range a.w.calls {
		if strings.Contains(c, "family-photos") {
			t.Fatalf("touched %s", c)
		}
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
