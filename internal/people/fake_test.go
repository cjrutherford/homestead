package people

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"homestead/internal/sso"
	"homestead/internal/store"
)

const fakeToken = "ak-people-token-DO-NOT-LEAK"

type fakeUser struct {
	PK       int
	Username string
	Name     string
	Email    string
	Active   bool
	Type     string
	UID      string
	Path     string
	Attrs    map[string]any
}

type fakeDevice struct{ pk, model string }

// fakeAuthentik is the slice of Authentik's API People uses, in memory.
type fakeAuthentik struct {
	mu        sync.Mutex
	srv       *httptest.Server
	users     map[int]*fakeUser
	groups    map[string][]int // group name -> member pks
	devices   map[int][]fakeDevice
	passwords map[int]string
	calls     []string
	nextPK    int
	noRecover bool
}

func newFake(t *testing.T) *fakeAuthentik {
	f := &fakeAuthentik{users: map[int]*fakeUser{}, groups: map[string][]int{}, devices: map[int][]fakeDevice{}, passwords: map[int]string{}, nextPK: 100}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAuthentik) seedUser(u fakeUser) *fakeUser {
	if u.Type == "" {
		u.Type = "internal"
	}
	if u.Attrs == nil {
		u.Attrs = map[string]any{}
	}
	f.users[u.PK] = &u
	return &u
}

func (f *fakeAuthentik) groupsOf(pk int) []map[string]any {
	out := []map[string]any{}
	for name, members := range f.groups {
		for _, m := range members {
			if m == pk {
				out = append(out, map[string]any{"name": name, "pk": "g-" + name})
			}
		}
	}
	return out
}

func (f *fakeAuthentik) render(u *fakeUser) map[string]any {
	return map[string]any{"pk": u.PK, "username": u.Username, "name": u.Name, "email": u.Email, "is_active": u.Active,
		"type": u.Type, "uid": u.UID, "path": u.Path, "attributes": u.Attrs, "groups_obj": f.groupsOf(u.PK)}
}

func (f *fakeAuthentik) member(name string, pk int) bool {
	for _, m := range f.groups[name] {
		if m == pk {
			return true
		}
	}
	return false
}

func (f *fakeAuthentik) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+fakeToken {
		http.Error(w, "no", http.StatusForbidden)
		return
	}
	var body map[string]any
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPatch) {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	enc := json.NewEncoder(w)
	p := r.URL.Path
	switch {
	case p == "/api/v3/core/users/" && r.Method == http.MethodGet:
		types := r.URL.Query()["type"]
		var out []any
		for pk := 0; pk < f.nextPK+1; pk++ {
			u := f.users[pk]
			if u == nil {
				continue
			}
			if len(types) > 0 {
				ok := false
				for _, t := range types {
					ok = ok || t == u.Type
				}
				if !ok {
					continue
				}
			}
			out = append(out, f.render(u))
		}
		_ = enc.Encode(map[string]any{"results": out})
	case p == "/api/v3/core/users/" && r.Method == http.MethodPost:
		name, _ := body["username"].(string)
		for _, u := range f.users {
			if u.Username == name {
				http.Error(w, `{"username":["taken"]}`, http.StatusBadRequest)
				return
			}
		}
		f.nextPK++
		attrs, _ := body["attributes"].(map[string]any)
		u := f.seedUser(fakeUser{PK: f.nextPK, Username: name, Name: str(body["name"]), Email: str(body["email"]), Active: true,
			Type: str(body["type"]), Path: str(body["path"]), Attrs: attrs, UID: fmt.Sprintf("uid-%d", f.nextPK)})
		w.WriteHeader(http.StatusCreated)
		_ = enc.Encode(f.render(u))
	case strings.HasPrefix(p, "/api/v3/core/users/"):
		rest := strings.TrimPrefix(p, "/api/v3/core/users/")
		parts := strings.Split(strings.Trim(rest, "/"), "/")
		pk, _ := strconv.Atoi(parts[0])
		u := f.users[pk]
		if u == nil {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			_ = enc.Encode(f.render(u))
		case len(parts) == 1 && r.Method == http.MethodPatch:
			if v, ok := body["username"]; ok {
				u.Username = str(v)
			}
			if v, ok := body["name"]; ok {
				u.Name = str(v)
			}
			if v, ok := body["email"]; ok {
				u.Email = str(v)
			}
			if v, ok := body["is_active"].(bool); ok {
				u.Active = v
			}
			if v, ok := body["attributes"].(map[string]any); ok {
				u.Attrs = v
			}
			_ = enc.Encode(f.render(u))
		case len(parts) == 1 && r.Method == http.MethodDelete:
			delete(f.users, pk)
			for g, ms := range f.groups {
				var keep []int
				for _, m := range ms {
					if m != pk {
						keep = append(keep, m)
					}
				}
				f.groups[g] = keep
			}
			w.WriteHeader(http.StatusNoContent)
		case parts[1] == "set_password":
			f.passwords[pk] = str(body["password"])
			w.WriteHeader(http.StatusNoContent)
		case parts[1] == "recovery":
			if f.noRecover {
				http.Error(w, "no recovery flow", http.StatusBadRequest)
				return
			}
			_ = enc.Encode(map[string]any{"link": fmt.Sprintf("https://auth.example/recover/tok-%d/", pk)})
		default:
			http.NotFound(w, r)
		}
	case p == "/api/v3/core/groups/" && r.Method == http.MethodGet:
		name := r.URL.Query().Get("name")
		var out []any
		if ms, ok := f.groups[name]; ok {
			users := []int{}
			users = append(users, ms...)
			out = append(out, map[string]any{"pk": "g-" + name, "name": name, "users": users})
		}
		_ = enc.Encode(map[string]any{"results": out})
	case strings.HasPrefix(p, "/api/v3/core/groups/g-") && r.Method == http.MethodPost:
		trim := strings.TrimPrefix(p, "/api/v3/core/groups/g-")
		name := strings.TrimSuffix(strings.TrimSuffix(trim, "/add_user/"), "/remove_user/")
		pk := int(body["pk"].(float64))
		if _, ok := f.groups[name]; !ok {
			http.NotFound(w, r)
			return
		}
		switch {
		case strings.HasSuffix(p, "/add_user/"):
			if !f.member(name, pk) {
				f.groups[name] = append(f.groups[name], pk)
			}
		default:
			var keep []int
			for _, m := range f.groups[name] {
				if m != pk {
					keep = append(keep, m)
				}
			}
			f.groups[name] = keep
		}
		w.WriteHeader(http.StatusNoContent)
	case p == "/api/v3/authenticators/admin/all/":
		pk, _ := strconv.Atoi(r.URL.Query().Get("user"))
		out := []any{}
		for _, d := range f.devices[pk] {
			out = append(out, map[string]any{"pk": d.pk, "name": "d", "meta_model_name": d.model})
		}
		_ = enc.Encode(out)
	case strings.HasPrefix(p, "/api/v3/authenticators/admin/") && r.Method == http.MethodDelete:
		parts := strings.Split(strings.Trim(strings.TrimPrefix(p, "/api/v3/authenticators/admin/"), "/"), "/")
		for pk, ds := range f.devices {
			var keep []fakeDevice
			for _, d := range ds {
				if d.pk != parts[1] {
					keep = append(keep, d)
				}
			}
			f.devices[pk] = keep
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func str(v any) string { s, _ := v.(string); return s }

// rig is a Service on a fresh database against the fake.
type rig struct {
	t    *testing.T
	f    *fakeAuthentik
	svc  *Service
	db   *sql.DB
	logs []string
	// kept is false to model no kept token.
	kept bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &rig{t: t, f: newFake(t), db: st.DB(), kept: true}
	for _, g := range []string{"homestead-owner", "homestead-nextcloud-users", "homestead-nextcloud-admins", "homestead-immich-users", "homestead-immich-admins", "plain-team"} {
		r.f.groups[g] = nil
	}
	r.f.seedUser(fakeUser{PK: 7, Username: "owner", Name: "Owner", Active: true, UID: "owner-sub"})
	r.f.seedUser(fakeUser{PK: 8, Username: "ak-outpost-abc", Active: true, Type: "internal_service_account"})
	r.f.seedUser(fakeUser{PK: 9, Username: "akadmin", Name: "authentik Admin", Active: true})
	r.f.seedUser(fakeUser{PK: 10, Username: "svc", Active: true, Type: "service_account"})
	r.f.seedUser(fakeUser{PK: 11, Username: "cousin", Name: "Cousin", Email: "cousin@example.test", Active: true})
	r.svc = &Service{
		DB: r.db,
		API: func() (*sso.API, error) {
			if !r.kept {
				return nil, ErrNoToken
			}
			return &sso.API{Base: r.f.srv.URL, Token: fakeToken, Client: func() *http.Client { return r.f.srv.Client() }}, nil
		},
		Apps:     func() ([]string, error) { return []string{"nextcloud", "immich"}, nil },
		OwnerSub: func() string { return "owner-sub" },
		Audit: func(actor, action, outcome, detail string) {
			r.logs = append(r.logs, actor+"|"+action+"|"+outcome+"|"+detail)
		},
	}
	return r
}
