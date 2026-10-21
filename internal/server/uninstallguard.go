package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"homestead/internal/appsso"
	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// uninstallLeftovers refuses an uninstall that would strand what only an
// installed app can remove (S299): its public names (S290) and its sign-in
// with Authentik (S298). Uninstalling Authentik is refused while other apps
// sign in through it. It answers the HTTP status, the refusal kind (sent
// as X-Homestead-Refusal, so the dashboard offers only the matching
// acknowledgement) and the message, or status 0 when the uninstall may go
// ahead.
func uninstallLeftovers(st *store.Store, manifest catalog.Manifest, leaveSignIn bool) (int, string, string, error) {
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return 0, "", "", err
	}
	if manifest.Name != authentikPackage {
		var public []string
		for _, route := range appsso.AppRoutes(manifest, set.Routes) {
			for _, n := range route.Tunnel {
				public = append(public, string(n))
			}
		}
		if len(public) > 0 {
			sort.Strings(public)
			return http.StatusConflict, refusePublicNames, fmt.Sprintf("%s still has public names on the tunnel (%s), and they can only be removed while it is installed. Remove each first: `homesteadctl apps public plan %s --off --hostname NAME`, then apply it (or Public names on the app page)",
				manifest.Title, strings.Join(public, ", "), manifest.Name), nil
		}
	}
	records := appsso.Records{DB: st.DB()}
	if manifest.Name == authentikPackage {
		using, err := records.Packages()
		if err != nil {
			return 0, "", "", err
		}
		if len(using) > 0 {
			return http.StatusConflict, refuseSignInDependents, fmt.Sprintf("apps still sign in through Authentik (%s). Turn their sign-in off first: `homesteadctl apps sign-in plan APP --off`, then apply it (or Sign-in with Authentik on each app page)",
				strings.Join(using, ", ")), nil
		}
		return 0, "", "", nil
	}
	if _, on, err := records.Get(manifest.Name); err != nil {
		return 0, "", "", err
	} else if on && !leaveSignIn {
		return http.StatusConflict, refuseSignIn, fmt.Sprintf("%s signs in through Authentik, and that can only be turned off while it is installed. Turn it off first: `homesteadctl apps sign-in plan %s --off`, then apply it (or Sign-in with Authentik on the app page). If Authentik cannot be reached, pass leave_sign_in to uninstall anyway and delete its provider and application in Authentik yourself",
			manifest.Title, manifest.Name), nil
	}
	return 0, "", "", nil
}

// Uninstall refusal kinds (X-Homestead-Refusal).
const (
	refusePublicNames      = "public-names"
	refuseSignIn           = "sign-in"
	refuseSignInDependents = "sign-in-dependents"
	refuseVolumes          = "volumes"
)

// forgetSignInStep is the last uninstall step when the owner left the
// app's sign-in in Authentik (leave_sign_in): the sealed client secret
// and the record go, so a later install starts with sign-in off.
func (s *Server) forgetSignInStep(st *store.Store, name, actor string) operation.StepDef {
	return operation.StepDef{Name: "forget-sign-in", Run: func(context.Context) error {
		records := appsso.Records{DB: st.DB()}
		rec, on, err := records.Get(name)
		if err != nil || !on {
			return err
		}
		if s.appSignIn != nil && s.appSignIn.cfg.Secrets != nil {
			if err := s.appSignIn.cfg.Secrets.Delete(appsso.SecretName(name)); err != nil {
				return fmt.Errorf("forget the client secret: %w", err)
			}
		}
		if err := records.Delete(name); err != nil {
			return err
		}
		_, _ = audit.New(st).Record(audit.Entry{
			Actor: actor, Action: "app.uninstall.leave-sign-in", Outcome: audit.OutcomeOK,
			Detail: fmt.Sprintf("%s uninstalled with its Authentik provider and application (client id %s) left in Authentik (acknowledged)", name, rec.ClientID),
		})
		return nil
	}}
}
