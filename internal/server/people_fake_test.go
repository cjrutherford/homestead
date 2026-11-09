package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// peopleAPI is the user side of the Authentik fake (S317). It answers the
// user, set_password, recovery and remove_user calls People makes, over the
// same groups the S314 fake keeps. It reports whether it handled the call.
func (w *ssoWorld) peopleAPI(rw http.ResponseWriter, r *http.Request, body map[string]any, enc *json.Encoder) bool {
	p := r.URL.Path
	if w.users == nil {
		w.users = map[int]map[string]any{}
	}
	groupsObj := func(pk int) []any {
		out := []any{}
		for name, g := range w.groups {
			for _, m := range g["users"].([]any) {
				if numEq(m, pk) {
					out = append(out, map[string]any{"name": name})
				}
			}
		}
		return out
	}
	view := func(u map[string]any) map[string]any {
		c := map[string]any{}
		for k, v := range u {
			c[k] = v
		}
		c["groups_obj"] = groupsObj(u["pk"].(int))
		return c
	}
	switch {
	case strings.HasSuffix(p, "/remove_user/"):
		for _, g := range w.groups {
			if "/api/v3/core/groups/"+g["pk"].(string)+"/remove_user/" == p {
				var keep []any
				for _, m := range g["users"].([]any) {
					if !numEq(m, int(body["pk"].(float64))) {
						keep = append(keep, m)
					}
				}
				if keep == nil {
					keep = []any{}
				}
				g["users"] = keep
			}
		}
		rw.WriteHeader(http.StatusNoContent)
		return true
	case p == "/api/v3/core/users/" && r.URL.Query().Get("include_groups") != "":
		out := []any{map[string]any{"pk": 7, "username": "owner", "name": "Owner", "uid": w.sub, "type": "internal", "is_active": true, "attributes": map[string]any{}, "groups_obj": groupsObj(7)}}
		for _, u := range w.users {
			out = append(out, view(u))
		}
		_ = enc.Encode(map[string]any{"results": out})
		return true
	case p == "/api/v3/core/users/" && r.Method == http.MethodPost:
		pk := 100 + len(w.users)
		u := map[string]any{"pk": pk, "username": body["username"], "name": body["name"], "email": body["email"], "is_active": true,
			"type": "internal", "uid": fmt.Sprintf("u%d", pk), "attributes": body["attributes"], "path": body["path"]}
		w.users[pk] = u
		rw.WriteHeader(http.StatusCreated)
		_ = enc.Encode(view(u))
		return true
	case p == "/api/v3/core/users/":
		return false
	case strings.HasPrefix(p, "/api/v3/core/users/"):
		parts := strings.Split(strings.Trim(strings.TrimPrefix(p, "/api/v3/core/users/"), "/"), "/")
		pk, err := strconv.Atoi(parts[0])
		u := w.users[pk]
		if err != nil || u == nil {
			http.NotFound(rw, r)
			return true
		}
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			_ = enc.Encode(view(u))
		case len(parts) == 1 && r.Method == http.MethodPatch:
			for k, v := range body {
				u[k] = v
			}
			_ = enc.Encode(view(u))
		case len(parts) == 1 && r.Method == http.MethodDelete:
			delete(w.users, pk)
			rw.WriteHeader(http.StatusNoContent)
		case parts[1] == "set_password":
			u["password_set"] = true
			rw.WriteHeader(http.StatusNoContent)
		case parts[1] == "recovery":
			_ = enc.Encode(map[string]any{"link": "https://auth.example/recover/one-time-token/"})
		default:
			http.NotFound(rw, r)
		}
		return true
	}
	return false
}

// numEq compares a group member (an int, or a float64 once it went through
// JSON) with a pk.
func numEq(v any, pk int) bool {
	switch n := v.(type) {
	case int:
		return n == pk
	case float64:
		return int(n) == pk
	}
	return false
}
