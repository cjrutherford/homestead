package overlay

import (
	"context"
	"fmt"
	"regexp"

	"homestead/internal/operation"
)

// existingHeadscaleUser names the Headscale user whose pre-auth key
// enrols this node. Pre-auth keys are minted server-side (headscale
// CLI on the server); the appliance only receives one, so issuance
// through the server API stays a later slice (DEFERRED) rather than a
// half-trusted handshake here.
var validHeadscaleUser = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// ExistingSetup enrols the appliance against an already-running
// Headscale server: origin, owning user, node hostname, routes, and
// posture. The pre-auth key arrives per call from the secret store
// (HeadscalePreauthSecret) at the caller's layer — stored secrets, not
// stored state, carry enrollment power.
type ExistingSetup struct {
	Server       HeadscaleServer
	User         string
	Hostname     string
	Routes       []SubnetRoute
	AcceptRoutes bool
	AcceptDNS    bool
}

// Validate checks setup shape: server origin, user spelling, node
// hostname, and routable routes.
func (s ExistingSetup) Validate() error {
	if err := s.Server.Validate(); err != nil {
		return err
	}
	if !validHeadscaleUser.MatchString(s.User) {
		return fmt.Errorf("overlay: headscale user must match [A-Za-z0-9][A-Za-z0-9_.-]{0,63}")
	}
	if err := validateHostname("overlay.hostname", s.Hostname); err != nil {
		return err
	}
	for i := range s.Routes {
		if err := s.Routes[i].Validate(); err != nil {
			return fmt.Errorf("route #%d: %w", i, err)
		}
	}
	return nil
}

// EnrolDefinition plans enrolment: up against the server with the
// pre-auth key, then a verify proving the node is online with
// addresses. An empty key is refused outright — enrolment without
// authentication is never a quiet re-apply.
func EnrolDefinition(setup ExistingSetup, preauthKey string) (operation.Definition, error) {
	if err := setup.Validate(); err != nil {
		return operation.Definition{}, err
	}
	if preauthKey == "" {
		return operation.Definition{}, fmt.Errorf("overlay: pre-auth key must not be empty")
	}
	up, err := HeadscaleUp(setup.Server, UpOptions{
		AuthKey:      preauthKey,
		Hostname:     setup.Hostname,
		Routes:       setup.Routes,
		AcceptRoutes: setup.AcceptRoutes,
		AcceptDNS:    setup.AcceptDNS,
	})
	if err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "headscale.enrol"}
	def.Steps = append(def.Steps, up.Steps...)
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "verify",
		Run: func(ctx context.Context) error {
			return verifyEnrolled(ctx)
		},
	})
	return def, nil
}

// verifyEnrolled proves enrolment took: the node reports online with at
// least one overlay address. Control-plane identity is not asserted —
// status JSON carries none — so this proves connectivity, and the
// caller proves it dialled the right server by owning the URL.
func verifyEnrolled(ctx context.Context) error {
	network, err := HeadscaleStatus(ctx)
	if err != nil {
		return err
	}
	if !network.Online {
		return fmt.Errorf("overlay: node not online after enrolment")
	}
	if len(network.Addresses) == 0 {
		return fmt.Errorf("overlay: node online without addresses")
	}
	return nil
}
