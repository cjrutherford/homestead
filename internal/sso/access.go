package sso

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Groups Homestead owns in Authentik (ADR-043). Every group it creates,
// changes or deletes carries GroupPrefix; anything else is never touched.
const (
	GroupPrefix = "homestead-"
	OwnerGroup  = GroupPrefix + "owner"
	// PeopleGroup holds every active person Homestead manages (ADR-050
	// §3). The dashboard application is bound to it and OwnerGroup, so
	// people can reach the dashboard's sign-in and nobody else can.
	PeopleGroup = GroupPrefix + "people"
	// GroupsScope is the scope name of the groups claim mapping.
	GroupsScope = "groups"
)

// AppGroups are an app's user and admin groups.
func AppGroups(pkg string) (users, admins string) {
	return GroupPrefix + pkg + "-users", GroupPrefix + pkg + "-admins"
}

// Access is the Authentik-side access setup for one application: the
// groups, the owner's membership, the bindings that close the application
// to everyone else, and the groups claim mapping. It holds no secret.
type Access struct {
	// Groups are created when missing. OwnerGroup may be among them; it is
	// shared and never deleted by an app's removal.
	Groups []string `json:"groups"`
	// OwnerSub is the linked owner's subject (the (issuer, sub) pair's sub),
	// matched to the Authentik user through its uid. Empty: no owner is
	// linked yet and no membership is made.
	OwnerSub string `json:"owner_sub,omitempty"`
	// OwnerOf are the groups the owner joins.
	OwnerOf []string `json:"owner_of"`
	// Bindings are the groups the application is bound to.
	Bindings []string `json:"bindings"`
	// ClaimMapping is the scope mapping's name; ClaimExpression its Python
	// expression. Empty for the dashboard, which reads no groups.
	ClaimMapping    string `json:"claim_mapping,omitempty"`
	ClaimExpression string `json:"claim_expression,omitempty"`
}

// ownedGroup reports whether name is a group Homestead may touch.
func ownedGroup(name string) bool {
	return strings.HasPrefix(name, GroupPrefix) && len(name) > len(GroupPrefix)
}

// Validate refuses any group outside the prefix.
func (x Access) Validate() error {
	for _, list := range [][]string{x.Groups, x.OwnerOf, x.Bindings} {
		for _, g := range list {
			if !ownedGroup(g) {
				return fmt.Errorf("sso: group %q does not carry the %s prefix: Homestead does not touch it", g, GroupPrefix)
			}
		}
	}
	return nil
}

// RemovableGroups are the groups a removal deletes: owned, and not the
// shared owner group.
func (x Access) RemovableGroups() []string {
	var out []string
	for _, g := range x.Groups {
		if ownedGroup(g) && g != OwnerGroup && g != PeopleGroup {
			out = append(out, g)
		}
	}
	return out
}

// Lines describes the access setup for a plan, one sentence per object.
func (x Access) Lines() []string {
	var out []string
	for _, g := range x.Groups {
		out = append(out, "Group "+g+" (created when missing).")
	}
	switch {
	case len(x.OwnerOf) == 0:
	case x.OwnerSub == "":
		out = append(out, "No owner is linked yet, so nobody is added to "+strings.Join(x.OwnerOf, ", ")+". Link the owner (Settings, Sign-in), then plan this again.")
	default:
		out = append(out, "The linked owner (found by subject) joins "+strings.Join(x.OwnerOf, ", ")+".")
	}
	for _, g := range x.Bindings {
		out = append(out, "Bind the application to group "+g+" (nobody outside the bound groups can sign in).")
	}
	if x.ClaimMapping != "" {
		out = append(out, fmt.Sprintf("Scope mapping %q emits a groups claim listing only %s groups.", x.ClaimMapping, GroupPrefix+"*"))
	}
	return out
}

// RemoteGroup is an Authentik group as returned.
type RemoteGroup struct {
	PK    string `json:"pk"`
	Name  string `json:"name"`
	Users []int  `json:"users"`
}

// RemoteUser is the part of an Authentik user used to find the owner.
type RemoteUser struct {
	PK       int    `json:"pk"`
	UID      string `json:"uid"`
	IsActive *bool  `json:"is_active"`
}

// remoteBinding is an Authentik policy binding as returned.
type remoteBinding struct {
	PK    string `json:"pk"`
	Group string `json:"group"`
}

type pagedList[T any] struct {
	Results    []T `json:"results"`
	Pagination struct {
		Next int `json:"next"`
	} `json:"pagination"`
}

// FindGroup returns the group named name, or nil. Names outside the prefix
// are refused before any request.
func (a *API) FindGroup(ctx context.Context, name string) (*RemoteGroup, error) {
	if !ownedGroup(name) {
		return nil, fmt.Errorf("sso: group %q does not carry the %s prefix", name, GroupPrefix)
	}
	var out pagedList[RemoteGroup]
	if err := a.do(ctx, http.MethodGet, "/api/v3/core/groups/?include_users=true&name="+url.QueryEscape(name), nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Results {
		if out.Results[i].Name == name {
			return &out.Results[i], nil
		}
	}
	return nil, nil
}

// OwnerUser finds the internal Authentik user whose uid (the value of the
// hashed_user_id subject) is sub, or nil.
func (a *API) OwnerUser(ctx context.Context, sub string) (*RemoteUser, error) {
	if sub == "" {
		return nil, nil
	}
	for page := 1; page > 0 && page < 1000; {
		var out pagedList[RemoteUser]
		if err := a.do(ctx, http.MethodGet, fmt.Sprintf("/api/v3/core/users/?type=internal&page_size=200&page=%d", page), nil, &out); err != nil {
			return nil, err
		}
		for i := range out.Results {
			if out.Results[i].UID == sub {
				return &out.Results[i], nil
			}
		}
		page = out.Pagination.Next
	}
	return nil, nil
}

func (a *API) ensureGroup(ctx context.Context, name string) (*RemoteGroup, bool, error) {
	g, err := a.FindGroup(ctx, name)
	if err != nil || g != nil {
		return g, false, err
	}
	var created RemoteGroup
	if err := a.do(ctx, http.MethodPost, "/api/v3/core/groups/", map[string]any{"name": name}, &created); err != nil {
		return nil, false, err
	}
	return &created, true, nil
}

type remoteMapping struct {
	PK         string `json:"pk"`
	Name       string `json:"name"`
	ScopeName  string `json:"scope_name"`
	Expression string `json:"expression"`
}

func (a *API) findClaimMapping(ctx context.Context, name string) (*remoteMapping, error) {
	var out pagedList[remoteMapping]
	if err := a.do(ctx, http.MethodGet, "/api/v3/propertymappings/provider/scope/?name="+url.QueryEscape(name), nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Results {
		if out.Results[i].Name == name {
			return &out.Results[i], nil
		}
	}
	return nil, nil
}

// ensureClaimMapping creates or updates the groups scope mapping and
// returns its pk.
func (a *API) ensureClaimMapping(ctx context.Context, x Access, step func(string)) (string, error) {
	have, err := a.findClaimMapping(ctx, x.ClaimMapping)
	if err != nil {
		return "", err
	}
	body := map[string]any{"name": x.ClaimMapping, "scope_name": GroupsScope, "expression": x.ClaimExpression}
	switch {
	case have == nil:
		var created remoteMapping
		if err := a.do(ctx, http.MethodPost, "/api/v3/propertymappings/provider/scope/", body, &created); err != nil {
			return "", err
		}
		step("created the groups claim mapping")
		return created.PK, nil
	case have.ScopeName != GroupsScope || have.Expression != x.ClaimExpression:
		if err := a.do(ctx, http.MethodPatch, "/api/v3/propertymappings/provider/scope/"+url.PathEscape(have.PK)+"/", body, nil); err != nil {
			return "", err
		}
		step("updated the groups claim mapping")
	default:
		step("groups claim mapping already matches")
	}
	return have.PK, nil
}

func (a *API) listBindings(ctx context.Context, appPK string) ([]remoteBinding, error) {
	var out pagedList[remoteBinding]
	if err := a.do(ctx, http.MethodGet, "/api/v3/policies/bindings/?page_size=200&target="+url.QueryEscape(appPK), nil, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// reconcileAccess makes the groups, the owner's membership and the
// application's bindings match x. It adds, never removes, members and
// bindings it did not make, and never binds the owner group while it has
// no member, so the owner is not locked out of the application.
func (a *API) reconcileAccess(ctx context.Context, x Access, appPK string, step func(string)) error {
	if err := x.Validate(); err != nil {
		return err
	}
	var owner *RemoteUser
	if x.OwnerSub != "" && len(x.OwnerOf) > 0 {
		var err error
		owner, err = a.OwnerUser(ctx, x.OwnerSub)
		if err != nil {
			return err
		}
		if owner == nil || owner.IsActive == nil || !*owner.IsActive {
			return fmt.Errorf("sso: linked owner is missing or inactive in Authentik")
		}
	}
	groups := map[string]*RemoteGroup{}
	for _, name := range x.Groups {
		g, created, err := a.ensureGroup(ctx, name)
		if err != nil {
			return err
		}
		groups[name] = g
		if created {
			step("created group " + name)
		} else {
			step("group " + name + " already exists")
		}
	}
	if owner != nil {
		user := owner
		for _, name := range x.OwnerOf {
			g := groups[name]
			if g == nil {
				var err error
				if g, err = a.FindGroup(ctx, name); err != nil || g == nil {
					return fmt.Errorf("sso: group %s is missing", name)
				}
				groups[name] = g
			}
			if hasPK(g.Users, user.PK) {
				step("owner already in " + name)
				continue
			}
			if err := a.do(ctx, http.MethodPost, "/api/v3/core/groups/"+url.PathEscape(g.PK)+"/add_user/", map[string]any{"pk": user.PK}, nil); err != nil {
				return err
			}
			g.Users = append(g.Users, user.PK)
			step("added the owner to " + name)
		}
	}
	if appPK == "" || len(x.Bindings) == 0 {
		return nil
	}
	if hasGroup(x.Bindings, OwnerGroup) {
		g := groups[OwnerGroup]
		if g == nil {
			var err error
			if g, err = a.FindGroup(ctx, OwnerGroup); err != nil {
				return err
			}
		}
		if g == nil || len(g.Users) == 0 {
			// Binding any group now would close the application to the
			// unlinked owner, who must sign in to Authentik to link.
			// Homestead's own subject check still refuses everyone else.
			step("owner group is empty: no binding is made yet, so the owner can sign in to link")
			return nil
		}
	}
	have, err := a.listBindings(ctx, appPK)
	if err != nil {
		return err
	}
	bound := map[string]bool{}
	for _, b := range have {
		bound[b.Group] = true
	}
	for i, name := range x.Bindings {
		g := groups[name]
		if g == nil {
			if g, err = a.FindGroup(ctx, name); err != nil || g == nil {
				return fmt.Errorf("sso: group %s is missing", name)
			}
		}
		if bound[g.PK] {
			step("binding to " + name + " already exists")
			continue
		}
		body := map[string]any{"target": appPK, "group": g.PK, "order": i, "enabled": true, "negate": false, "timeout": 30, "failure_result": false}
		if err := a.do(ctx, http.MethodPost, "/api/v3/policies/bindings/", body, nil); err != nil {
			return err
		}
		step("bound the application to " + name)
	}
	return nil
}

func hasPK(list []int, pk int) bool {
	for _, v := range list {
		if v == pk {
			return true
		}
	}
	return false
}

// removeAccess deletes the groups a removal owns and the claim mapping.
// Only groups carrying the prefix, other than the shared owner group, are
// deleted.
func (a *API) removeAccess(ctx context.Context, x Access, step func(string)) error {
	for _, name := range x.RemovableGroups() {
		g, err := a.FindGroup(ctx, name)
		if err != nil {
			return err
		}
		if g == nil {
			step("group " + name + " already gone")
			continue
		}
		if err := a.do(ctx, http.MethodDelete, "/api/v3/core/groups/"+url.PathEscape(g.PK)+"/", nil, nil); err != nil {
			return err
		}
		step("deleted group " + name)
	}
	if x.ClaimMapping != "" {
		m, err := a.findClaimMapping(ctx, x.ClaimMapping)
		if err != nil {
			return err
		}
		if m == nil {
			step("claim mapping already gone")
			return nil
		}
		if err := a.do(ctx, http.MethodDelete, "/api/v3/propertymappings/provider/scope/"+url.PathEscape(m.PK)+"/", nil, nil); err != nil {
			return err
		}
		step("deleted the groups claim mapping")
	}
	return nil
}

// GroupState, BindingState and AccessReport are the read-back (ADR-043
// §6): what Authentik holds against what Homestead recorded. A difference
// is drift; nothing is reverted.
type GroupState struct {
	Name        string `json:"name"`
	Exists      bool   `json:"exists"`
	Members     int    `json:"members"`
	OwnerMember bool   `json:"owner_member"`
}

type BindingState struct {
	Group string `json:"group"`
	Bound bool   `json:"bound"`
}

type AccessReport struct {
	Groups   []GroupState   `json:"groups"`
	Bindings []BindingState `json:"bindings"`
	// ClaimMapping is "" when the spec has none, else ok, missing or differs.
	ClaimMapping string `json:"claim_mapping,omitempty"`
	// Open is true when the application has no group binding at all: every
	// Authentik user can sign in to it.
	Open  bool     `json:"open"`
	Drift []string `json:"drift"`
}

// ReadAccess reads, without changing anything, what Authentik holds for x
// against the application slug.
func (a *API) ReadAccess(ctx context.Context, x Access, appSlug string) (AccessReport, error) {
	rep := AccessReport{Groups: []GroupState{}, Bindings: []BindingState{}, Drift: []string{}}
	if err := x.Validate(); err != nil {
		return rep, err
	}
	var owner *RemoteUser
	if x.OwnerSub != "" {
		var err error
		if owner, err = a.OwnerUser(ctx, x.OwnerSub); err != nil {
			return rep, err
		}
	}
	byName := map[string]*RemoteGroup{}
	for _, name := range x.Groups {
		g, err := a.FindGroup(ctx, name)
		if err != nil {
			return rep, err
		}
		byName[name] = g
		st := GroupState{Name: name, Exists: g != nil}
		if g != nil {
			st.Members = len(g.Users)
			st.OwnerMember = owner != nil && hasPK(g.Users, owner.PK)
		} else {
			rep.Drift = append(rep.Drift, "group "+name+" is missing")
		}
		for _, want := range x.OwnerOf {
			if want == name && g != nil && owner != nil && !st.OwnerMember {
				rep.Drift = append(rep.Drift, "the owner is not in "+name)
			}
		}
		rep.Groups = append(rep.Groups, st)
	}
	app, err := a.FindApplication(ctx, appSlug)
	if err != nil {
		return rep, err
	}
	if app == nil {
		rep.Drift = append(rep.Drift, "application "+appSlug+" is missing")
	} else {
		have, err := a.listBindings(ctx, app.PK)
		if err != nil {
			return rep, err
		}
		bound := map[string]bool{}
		for _, b := range have {
			if b.Group != "" {
				bound[b.Group] = true
			}
		}
		rep.Open = len(have) == 0
		og := byName[OwnerGroup]
		// While the owner group is empty no binding is made (the owner
		// must be able to sign in to link), so nothing is drift.
		waiting := hasGroup(x.Bindings, OwnerGroup) && (og == nil || len(og.Users) == 0)
		for _, name := range x.Bindings {
			g := byName[name]
			st := BindingState{Group: name, Bound: g != nil && bound[g.PK]}
			if !st.Bound && !waiting {
				rep.Drift = append(rep.Drift, "the application is not bound to "+name)
			}
			rep.Bindings = append(rep.Bindings, st)
		}
	}
	if x.ClaimMapping != "" {
		m, err := a.findClaimMapping(ctx, x.ClaimMapping)
		if err != nil {
			return rep, err
		}
		switch {
		case m == nil:
			rep.ClaimMapping = "missing"
			rep.Drift = append(rep.Drift, "the groups claim mapping is missing")
		case m.Expression != x.ClaimExpression || m.ScopeName != GroupsScope:
			rep.ClaimMapping = "differs"
			rep.Drift = append(rep.Drift, "the groups claim mapping was changed")
		default:
			rep.ClaimMapping = "ok"
		}
	}
	sort.Strings(rep.Drift)
	return rep, nil
}

// inspectAccess reports what applying x would do to the groups, the
// owner's membership, the bindings and the claim mapping.
func (a *API) inspectAccess(ctx context.Context, x Access, app *Application) ([]Object, error) {
	var out []Object
	byName := map[string]*RemoteGroup{}
	for _, name := range x.Groups {
		g, err := a.FindGroup(ctx, name)
		if err != nil {
			return nil, err
		}
		byName[name] = g
		o := Object{Kind: "Group", Name: name, Action: ActionCreate}
		if g != nil {
			o.Action = ActionUnchanged
		}
		out = append(out, o)
	}
	var owner *RemoteUser
	if x.OwnerSub != "" {
		var err error
		if owner, err = a.OwnerUser(ctx, x.OwnerSub); err != nil {
			return nil, err
		}
	}
	for _, name := range x.OwnerOf {
		o := Object{Kind: "Group member", Name: "owner in " + name, Action: ActionUnchanged}
		switch g := byName[name]; {
		case owner == nil:
			o.Action, o.Changes = ActionUnchanged, []string{"the owner is not linked or not found: nobody is added"}
		case g == nil || !hasPK(g.Users, owner.PK):
			o.Action = ActionCreate
		}
		out = append(out, o)
	}
	var bound map[string]bool
	if app != nil {
		have, err := a.listBindings(ctx, app.PK)
		if err != nil {
			return nil, err
		}
		bound = map[string]bool{}
		for _, b := range have {
			bound[b.Group] = true
		}
	}
	og := byName[OwnerGroup]
	waiting := hasGroup(x.Bindings, OwnerGroup) && owner == nil && (og == nil || len(og.Users) == 0)
	for _, name := range x.Bindings {
		o := Object{Kind: "Binding", Name: "application to " + name, Action: ActionCreate}
		g := byName[name]
		switch {
		case g != nil && bound[g.PK]:
			o.Action = ActionUnchanged
		case waiting:
			o.Action, o.Changes = ActionUnchanged, []string{"not bound until the owner is linked, so the owner can sign in to link"}
		}
		out = append(out, o)
	}
	if x.ClaimMapping != "" {
		m, err := a.findClaimMapping(ctx, x.ClaimMapping)
		if err != nil {
			return nil, err
		}
		o := Object{Kind: "Scope mapping", Name: x.ClaimMapping, Action: ActionCreate}
		if m != nil {
			o.Action = ActionUnchanged
			if m.Expression != x.ClaimExpression || m.ScopeName != GroupsScope {
				o.Action, o.Changes = ActionUpdate, []string{"expression"}
			}
		}
		out = append(out, o)
	}
	return out, nil
}
