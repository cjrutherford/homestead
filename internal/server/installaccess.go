package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"homestead/internal/appaccess"
	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/sso"
	"homestead/internal/store"
)

// accessRoster reads Authentik without reconciling local records. The digest
// binds the reviewed decision to the remote identity and active state, but
// never exposes Authentik UIDs in a response or setup record.
func accessRoster(ctx context.Context, st *store.Store, api *sso.API, ownerUID string) ([]installAccessPerson, string, error) {
	accounts, err := api.ListAccounts(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("cannot verify Authentik people")
	}
	byPK := make(map[int]sso.RemoteAccount, len(accounts))
	for _, account := range accounts {
		byPK[account.PK] = account
	}
	rows, err := st.DB().Query(`SELECT id, display_name, username, state, authentik_pk, authentik_uid FROM people ORDER BY id`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	roster := []installAccessPerson{}
	known := map[int]bool{}
	h := sha256.New()
	for rows.Next() {
		var p installAccessPerson
		var pk int
		var uid string
		if err := rows.Scan(&p.ID, &p.DisplayName, &p.Username, &p.State, &pk, &uid); err != nil {
			return nil, "", err
		}
		account, exists := byPK[pk]
		known[pk] = true
		if p.State == "active" && (pk == 0 || uid == "" || !exists || account.UID == "" || account.UID != uid || !account.IsActive) {
			return nil, "", fmt.Errorf("cannot verify active Authentik account for person %d; review People before installing", p.ID)
		}
		// Disabled people remain explicit none decisions even when their remote
		// account has been disabled or removed. Record that state as drift.
		fmt.Fprintf(h, "%d:%s:%d:%q:%t:%d:%q:%t;", p.ID, p.State, pk, uid, exists, account.PK, account.UID, account.IsActive)
		roster = append(roster, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	for _, account := range accounts {
		if known[account.PK] || account.Managed() || account.UID == ownerUID || account.Username == "akadmin" {
			continue
		}
		return nil, "", fmt.Errorf("adopt existing Authentik users on the People page before choosing app access")
	}
	return roster, fmt.Sprintf("%x", h.Sum(nil)), nil
}

// The verified Craft packages have no application account or permission
// system. A missing SSO declaration alone does not mean an app is open.
var accountlessInstallApps = map[string]bool{
	"cadcraft": true, "deckcraft": true, "designcraft": true,
	"effectcraft": true, "figcraft": true, "filmcraft": true,
	"gridcraft": true, "lightcraft": true, "pdfcraft": true,
	"photocraft": true, "soundcraft": true, "vectorcraft": true,
	"wordcraft": true,
}

func installAccessMode(m catalog.Manifest) string {
	if m.Name == "authentik" {
		return "bootstrap"
	}
	if accountlessInstallApps[m.Name] {
		return "open"
	}
	if m.SSO.OIDC != nil {
		if _, err := appsso.AdapterFor(*m.SSO.OIDC); err == nil {
			return "managed"
		}
		// A declared adapter Homestead does not know is a catalog fault.
		return "unavailable"
	}
	// No OIDC declaration: the app keeps its own accounts (S398).
	return "self-managed"
}

// selfManagedNotice is the plain statement shown wherever the install
// access review is shown for a self-managed app (ADR-054, S398).
func selfManagedNotice(m catalog.Manifest) string {
	title := m.Title
	if title == "" {
		title = m.Name
	}
	if m.SSO.ForwardAuth != nil && m.FirstRun != nil {
		// A gated app (ADR-055, ADR-059): the install makes the
		// administrator, so nothing waits in the app for a first visitor.
		return title + " keeps its own accounts. Homestead creates its administrator, " + m.FirstRun.Account + ", during install (its password is under Secrets once installed), so no setup screen is ever open. With Authentik sign-in approved, the gate is set up with the install; who can pass it is chosen on the People page."
	}
	return title + " keeps its own accounts. Homestead does not control who can sign in to it; create accounts in the app itself."
}

// selfManagedIntegration binds the mode and the reviewed install plan into the
// access digest so a self-managed review cannot be replayed as another mode.
func selfManagedIntegration(plan catalog.InstallPlan) string {
	return "self-managed-v1:" + installDigest(plan)
}

func validateInstallAdmin(choices []appaccess.Choice, adminAuthorized bool) error {
	for _, choice := range choices {
		if choice.Role == appaccess.Admin && !adminAuthorized {
			return fmt.Errorf("administrator grants require fresh authentication")
		}
	}
	return nil
}

type installAccessPerson struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username"`
	State       string `json:"state"`
}

type installAccessView struct {
	Mode           string                `json:"mode"`
	People         []installAccessPerson `json:"people"`
	AdminSupported bool                  `json:"admin_supported"`
	Reason         string                `json:"reason,omitempty"`
	// Notice states plainly that the app keeps its own accounts (self-managed).
	Notice string `json:"notice,omitempty"`
}

type installAccessApproval struct {
	Digest  string             `json:"digest"`
	Choices []appaccess.Choice `json:"choices"`
}

func (s *Server) installAccess(ctx context.Context, st *store.Store, m catalog.Manifest, plan catalog.InstallPlan, public string) (installAccessView, string, error) {
	v := installAccessView{Mode: installAccessMode(m), People: []installAccessPerson{}}
	if v.Mode == "open" || v.Mode == "bootstrap" {
		return v, "", nil
	}
	if v.Mode == "self-managed" {
		v.Notice = selfManagedNotice(m)
		return v, selfManagedIntegration(plan), nil
	}
	if v.Mode == "unavailable" {
		v.Reason = "Homestead cannot yet configure this app's accounts and permissions."
		return v, "", nil
	}
	// Previously retained grants/provider state could make a "none" choice
	// ineffective. Require the owner to clear that old setup before a fresh
	// install rather than silently leaving access in place.
	var previousGrants int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM people_grants WHERE app = ?`, m.Name).Scan(&previousGrants); err != nil {
		return v, "", err
	}
	_, previousSignIn, err := (appsso.Records{DB: st.DB()}).Get(m.Name)
	if err != nil {
		return v, "", err
	}
	if previousGrants != 0 || previousSignIn {
		v.Mode = "unavailable"
		v.Reason = "Previous app sign-in or grants remain; remove or reconcile them before a new installation."
		return v, "", nil
	}
	if s.appSignIn == nil {
		v.Mode = "unavailable"
		v.Reason = "Authentik sign-in is not available."
		return v, "", nil
	}
	si := s.appSignIn.preview(m, plan, strings.ToLower(strings.TrimSpace(public)))
	if si == nil || !si.Offered || si.Plan == nil || !si.TokenKept {
		v.Mode = "unavailable"
		v.Reason = "Set up Authentik and retain its API token before installing this app."
		if si != nil && si.Reason != "" {
			v.Reason = si.Reason
		}
		return v, "", nil
	}
	if si.Plan.Access == nil || si.Plan.Access.OwnerSub == "" {
		v.Mode = "unavailable"
		v.Reason = "Link the appliance owner's Authentik account before installing this app."
		return v, "", nil
	}
	dashboard, configured, err := s.appSignIn.m.Config()
	if err != nil {
		return v, "", err
	}
	if !configured {
		v.Mode = "unavailable"
		v.Reason = "Authentik sign-in is not configured."
		return v, "", nil
	}
	token, err := s.appSignIn.cfg.Secrets.Get(sso.APITokenName)
	if err != nil || token == "" {
		v.Mode = "unavailable"
		v.Reason = "The retained Authentik API token is unavailable."
		return v, "", nil
	}
	api := &sso.API{Base: dashboard.AuthentikURL, Token: token, Client: s.appSignIn.client}
	owner, err := api.OwnerUser(ctx, si.Plan.Access.OwnerSub)
	if err != nil || owner == nil || owner.IsActive == nil || !*owner.IsActive {
		v.Mode = "unavailable"
		v.Reason = "Cannot verify the linked owner's active Authentik account."
		return v, "", nil
	}
	users, admins := sso.AppGroups(m.Name)
	for _, name := range []string{users, admins} {
		group, err := api.FindGroup(ctx, name)
		if err != nil {
			v.Mode = "unavailable"
			v.Reason = "Cannot verify existing Authentik app groups."
			return v, "", nil
		}
		if group != nil {
			v.Mode = "unavailable"
			v.Reason = "Previous Authentik app groups remain; review and remove them before a new installation."
			return v, "", nil
		}
	}
	if s.people == nil {
		v.Mode = "unavailable"
		v.Reason = "Homestead cannot review its people yet."
		return v, "", nil
	}
	roster, remoteDigest, err := accessRoster(ctx, st, api, si.Plan.Access.OwnerSub)
	if err != nil {
		v.Mode = "unavailable"
		v.Reason = err.Error()
		return v, "", nil
	}
	adapter, err := appsso.AdapterFor(*m.SSO.OIDC)
	if err != nil {
		return v, "", err
	}
	v.AdminSupported = adapter.Roles(appsso.GroupNames{}).Mapped
	v.People = roster
	return v, installDigest(plan) + ":" + si.Plan.Digest + ":" + remoteDigest, nil
}

func accessPeople(v installAccessView) []appaccess.Person {
	ps := make([]appaccess.Person, 0, len(v.People))
	for _, p := range v.People {
		ps = append(ps, appaccess.Person{ID: p.ID, Active: p.State == "active"})
	}
	return ps
}

func (a *appSetup) verifiedAccessGrantPlan(ctx context.Context, app string, choices []appaccess.Choice) (appaccess.Plan, error) {
	if a.s.people == nil || a.s.people.API == nil {
		return appaccess.Plan{}, fmt.Errorf("people are not managed on this appliance")
	}
	api, err := a.s.people.API()
	if err != nil {
		return appaccess.Plan{}, fmt.Errorf("cannot verify Authentik people")
	}
	ownerUID := ""
	if a.s.people.OwnerSub != nil {
		ownerUID = a.s.people.OwnerSub()
	}
	roster, remoteDigest, err := accessRoster(ctx, a.st, api, ownerUID)
	if err != nil {
		return appaccess.Plan{}, err
	}
	return appaccess.Build(app, accessPeople(installAccessView{People: roster}), choices, true, "setup-grants-v2:"+remoteDigest)
}

func handleInstallAccessPlan(s *Server, st *store.Store, catalogDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body installBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req, err := installRequest(st, catalogDir, r.PathValue("name"), body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		plan, status, err := planChecked(st, req)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		view, integration, err := s.installAccess(r.Context(), st, req.Manifest, plan, body.PublicName)
		if err != nil {
			http.Error(w, "cannot read app access state", http.StatusInternalServerError)
			return
		}
		if view.Mode == "self-managed" {
			if len(body.Choices) != 0 {
				http.Error(w, "this app keeps its own accounts; Homestead cannot apply per-person access choices", http.StatusUnprocessableEntity)
				return
			}
			p, err := appaccess.Build(req.Manifest.Name, nil, nil, false, integration)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, map[string]any{"digest": p.Digest})
			return
		}
		if view.Mode != "managed" {
			http.Error(w, fmt.Sprintf("access decisions unavailable: %s", view.Reason), http.StatusUnprocessableEntity)
			return
		}
		p, err := appaccess.Build(req.Manifest.Name, accessPeople(view), body.Choices, view.AdminSupported, integration)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, map[string]any{"digest": p.Digest})
	}
}
