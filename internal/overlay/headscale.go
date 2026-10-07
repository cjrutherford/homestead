package overlay

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"homestead/internal/operation"
)

// Headscale secret names under the shared convention: the pre-auth key
// joins clients to the server, the API key manages server settings
// (users, keys, routes) in later slices. Declared together so writers
// and readers meet at one spelling.
const (
	// HeadscalePreauthSecret holds the pre-auth key for client enrolment.
	HeadscalePreauthSecret = "overlay_headscale_preauth"
	// HeadscaleAPIKeySecret holds the server API key for management calls.
	HeadscaleAPIKeySecret = "overlay_headscale_apikey"
)

// HeadscaleServer identifies the control plane a client enrols
// against: self-hosted managed (S114) or an existing external server
// (S113). Only the origin matters here — API paths attach at call
// sites, never in stored state.
type HeadscaleServer struct {
	URL string
}

// Validate checks the server origin: http or https with a host.
// Self-hosted LAN servers are plain http by deployment choice, so both
// schemes stay legal; callers choose the trust posture, not this unit.
func (s HeadscaleServer) Validate() error {
	parsed, err := url.Parse(s.URL)
	if err != nil {
		return fmt.Errorf("overlay: headscale URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("overlay: headscale URL must be http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("overlay: headscale URL must have a host")
	}
	return nil
}

// HeadscaleUp plans client enrolment against server: the shared
// `tailscale up` flow with the control plane pinned to the Headscale
// origin. Observation reuses Status unchanged — headscale-backed
// tailscaled emits the same JSON, so no second parser exists to drift.
func HeadscaleUp(server HeadscaleServer, options UpOptions) (operation.Definition, error) {
	if err := server.Validate(); err != nil {
		return operation.Definition{}, err
	}
	options.LoginServer = strings.TrimSuffix(server.URL, "/")
	return UpDefinition(options)
}

// HeadscaleStatus reads a headscale-backed node identically to a
// Tailscale one: same daemon, same JSON, same neutral mapping.
func HeadscaleStatus(ctx context.Context) (Network, error) {
	return Status(ctx)
}
