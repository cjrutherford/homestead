package appsso

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"homestead/internal/sso"
)

// ClaimMappingName is the Authentik scope mapping that carries an app's
// groups claim (ADR-043 §3).
func ClaimMappingName(title string) string { return "Homestead: " + title + " groups" }

// RoleMapping is how an adapter turns the admin group into the app's own
// admin role. Mapped false means the app cannot: its admins stay local.
type RoleMapping struct {
	Mapped bool
	// Note is the plan line that says how the admin role is set, or why
	// it is not.
	Note string
	// AdminAlias, when set, is added to the groups claim for members of the
	// admin group (Nextcloud reads the group "admin").
	AdminAlias string
	// UserAlias (S417), when set, is added to the groups claim for
	// everyone outside the admin group (Audiobookshelf reads "user").
	UserAlias string
	// Claims are extra claims, each a Python expression over `admin` (true
	// for members of the admin group).
	Claims map[string]string
}

// GroupNames are the three groups an app is bound to.
type GroupNames struct{ Users, Admins, Owner string }

func groupNames(pkg string) GroupNames {
	u, a := sso.AppGroups(pkg)
	return GroupNames{Users: u, Admins: a, Owner: sso.OwnerGroup}
}

func pyStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// claimExpression is the scope mapping's Python: a groups claim with only
// homestead- groups, plus what the adapter adds for the admin role.
func claimExpression(g GroupNames, rm RoleMapping) string {
	var b strings.Builder
	fmt.Fprintf(&b, "names = sorted(group.name for group in request.user.ak_groups.all() if group.name.startswith(%s))\n", pyStr(sso.GroupPrefix))
	fmt.Fprintf(&b, "admin = %s in names\n", pyStr(g.Admins))
	b.WriteString("claims = {\"groups\": list(names)}\n")
	if rm.AdminAlias != "" {
		fmt.Fprintf(&b, "if admin:\n    claims[\"groups\"].append(%s)\n", pyStr(rm.AdminAlias))
	}
	if rm.UserAlias != "" {
		fmt.Fprintf(&b, "if not admin:\n    claims[\"groups\"].append(%s)\n", pyStr(rm.UserAlias))
	}
	keys := make([]string, 0, len(rm.Claims))
	for k := range rm.Claims {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "claims[%s] = %s\n", pyStr(k), rm.Claims[k])
	}
	b.WriteString("return claims\n")
	return b.String()
}

// buildAccess is the Authentik access setup for an app: the three groups,
// the owner in all of them, the application bound to all of them, and the
// groups claim mapping.
func buildAccess(pkg, title string, ownerSub string, rm RoleMapping) sso.Access {
	g := groupNames(pkg)
	all := []string{g.Users, g.Admins, g.Owner}
	return sso.Access{
		Groups: all, OwnerSub: ownerSub, OwnerOf: append([]string(nil), all...), Bindings: append([]string(nil), all...),
		ClaimMapping: ClaimMappingName(title), ClaimExpression: claimExpression(g, rm),
	}
}

// removeAccess names what turning sign-in off deletes: the app's two
// groups and its claim mapping. The shared owner group stays.
func removeAccess(pkg, title string) sso.Access {
	g := groupNames(pkg)
	return sso.Access{Groups: []string{g.Users, g.Admins}, OwnerOf: []string{}, Bindings: []string{}, ClaimMapping: ClaimMappingName(title)}
}

func sameAccess(a, b *sso.Access) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
