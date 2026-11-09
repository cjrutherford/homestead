package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"homestead/internal/catalog"
)

// KumaAdminAccount is the administrator account Homestead creates in
// Uptime Kuma.
const KumaAdminAccount = "homestead-admin"

// restartContainer restarts an app's container, stubbed in tests.
var restartContainer = func(ctx context.Context, container string) error {
	return exec.CommandContext(ctx, "docker", "restart", container).Run()
}

// uptimeKumaFirstRun gives Uptime Kuma its administrator at install, so its
// first-visit setup screen is never open. It runs the manifest's own pinned
// commands (the same script the sign-in operation uses, refusing any other
// Kuma version): prepare chooses the built-in SQLite database (restarting
// Kuma once if it asks), then account creates the account if the user table
// is empty. The password is generated and sealed first (revealable) and only
// its bcrypt hash travels, on stdin. Kuma's own login stays on until sign-in
// with Authentik is applied; an existing account is left alone.
//
// VERIFIED AGAINST Uptime Kuma 2.5.7 (see the manifest's script): an empty
// user table is what shows the setup screen.
func uptimeKumaFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	var o *catalog.ForwardOwnLoginOff
	if m.SSO.ForwardAuth != nil {
		o = m.SSO.ForwardAuth.OwnLoginOff
	}
	if f == nil || m.Name != "uptime-kuma" || f.Account != KumaAdminAccount || secrets == nil || o == nil || len(o.Account) == 0 {
		return "", errors.New("Uptime Kuma first-run configuration is unsupported")
	}
	container := m.Name + "-" + f.Service
	return waitFor(ctx, m.Title, func() (Result, bool, error) {
		out, err := execIn(ctx, container, "", o.Prepare...)
		if err != nil {
			return "", false, errors.New("Uptime Kuma is starting")
		}
		if strings.Contains(out, catalog.RestartMarker) {
			if err := restartContainer(ctx, container); err != nil {
				return "", true, errors.New("Uptime Kuma could not be restarted after choosing its database")
			}
			return "", false, errors.New("Uptime Kuma is restarting")
		}
		password, err := Password(m, secrets)
		if err != nil {
			return "", true, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
		if err != nil {
			return "", true, err
		}
		doc, _ := json.Marshal(map[string]string{"username": f.Account, "hash": string(hash)})
		out, err = execIn(ctx, container, string(doc)+"\n", o.Account...)
		if err != nil {
			return "", false, errors.New("Uptime Kuma is still creating its database")
		}
		switch {
		case strings.Contains(out, "account created"):
			return Created, true, nil
		case strings.Contains(out, "account exists"):
			return Ready, true, nil
		}
		return "", true, errors.New("Uptime Kuma returned an unexpected first-run response")
	})
}
