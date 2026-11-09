package server

import (
	"context"
	"fmt"

	"homestead/internal/catalog"
	"homestead/internal/firstrun"
	"homestead/internal/operation"
)

// firstRunStep finishes an app's own first-run setup at the end of its
// install (S318): Immich gets its administrator, with the generated
// password the owner may reveal, so its web app opens on the login page
// rather than "create an administrator". The stack is up by then, so a
// failure keeps the install and says how to try again.
func firstRunStep(m catalog.Manifest, secrets firstrun.Secrets, owner func(context.Context) string) operation.StepDef {
	return operation.StepDef{Name: "first-run", Run: func(ctx context.Context) error {
		name := ""
		if owner != nil {
			name = owner(ctx)
		}
		if _, err := firstrun.RunAs(ctx, m, secrets, name); err != nil {
			return fmt.Errorf("%s is installed and running, but Homestead could not finish its setup: %w. Turning on sign-in with Authentik tries again", m.Title, err)
		}
		return nil
	}}
}
