package exposure

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/wizard"
)

// ExposureWizardID identifies the public-exposure flow in the generic
// wizard store (S036). The payload carries route, target, and planner
// inputs — all non-sensitive — while the reviewed artifact is always
// recomputed, never stored, so reviews cannot go stale beside their
// inputs.
const ExposureWizardID = "exposure"

// ExposureSteps orders the flow: name the route, choose the target
// with planner inputs, review the computed plan, done.
var ExposureSteps = []string{"route", "target", "review", "done"}

// exposureStepIndex orders steps; -1 means unknown.
func exposureStepIndex(step string) int {
	for i, name := range ExposureSteps {
		if name == step {
			return i
		}
	}
	return -1
}

// Wizard guides private routes to reviewed public plans over the
// wizard store. It owns no HTTP surface: the generic wizard endpoints
// and later slices drive it.
type Wizard struct {
	wizards *wizard.Service
	// routes reads the recorded edge route table. The dashboard route is
	// identified from it, never from the client payload (ADR-035).
	routes func() (docker.RouteSet, error)
}

// SetRoutes wires the recorded edge route table, so planning knows which
// names are the dashboard route whatever the payload says.
func (w *Wizard) SetRoutes(load func() (docker.RouteSet, error)) { w.routes = load }

// request decodes the planner request and marks the dashboard route from
// the recorded table; a public target for a dashboard name is refused.
func (w *Wizard) request(payload map[string]any) (Request, error) {
	req, err := decodeRequest(payload)
	if err != nil {
		return Request{}, err
	}
	if w.routes == nil {
		return req, nil
	}
	set, err := w.routes()
	if err != nil {
		return Request{}, fmt.Errorf("exposure: read edge routes: %w", err)
	}
	if dash, ok := set.DashboardRoute(); ok {
		for _, n := range dash.AllNames() {
			if strings.EqualFold(string(n), string(req.Route.Host)) {
				req.Route.Dashboard = true
			}
		}
	}
	if req.Route.Dashboard && req.To == docker.ExposurePublic {
		return Request{}, fmt.Errorf("exposure: %s", docker.ErrDashboardPublic)
	}
	return req, nil
}

// NewWizard returns a Wizard.
func NewWizard(wizards *wizard.Service) *Wizard {
	return &Wizard{wizards: wizards}
}

// decodeRoute renders the route payload back into a validated route.
func decodeRoute(payload map[string]any) (docker.Route, error) {
	raw, ok := payload["route"]
	if !ok {
		return docker.Route{}, fmt.Errorf("exposure: route step needs a route")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return docker.Route{}, fmt.Errorf("exposure: route encode: %w", err)
	}
	var route docker.Route
	if err := json.Unmarshal(encoded, &route); err != nil {
		return docker.Route{}, fmt.Errorf("exposure: route decode: %w", err)
	}
	if err := route.Validate(); err != nil {
		return docker.Route{}, err
	}
	return route, nil
}

// decodeCertificates renders the optional certificate table; absent
// means an empty set, which plans issuance with suggested names.
func decodeCertificates(payload map[string]any) (cert.Set, error) {
	raw, ok := payload["certificates"]
	if !ok {
		return cert.Set{}, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return cert.Set{}, fmt.Errorf("exposure: certificates encode: %w", err)
	}
	var set cert.Set
	if err := json.Unmarshal(encoded, &set); err != nil {
		return cert.Set{}, fmt.Errorf("exposure: certificates decode: %w", err)
	}
	if err := set.Validate(); err != nil {
		return cert.Set{}, err
	}
	return set, nil
}

// decodeRequest renders the full planner request from a target-or-later
// payload: target exposure, zone, public address, and certificates.
// Monitoring statuses ride with apply-time data, not wizard state, so
// unknown coverage conservatively plans issuance (S105 refines later).
func decodeRequest(payload map[string]any) (Request, error) {
	route, err := decodeRoute(payload)
	if err != nil {
		return Request{}, err
	}
	to, _ := payload["to"].(string)
	if docker.Exposure(to) != docker.ExposureLocal &&
		docker.Exposure(to) != docker.ExposureOverlay &&
		docker.Exposure(to) != docker.ExposurePublic {
		return Request{}, fmt.Errorf("exposure: target must be local, overlay, or public")
	}
	if docker.Exposure(to) == route.Exposure {
		return Request{}, fmt.Errorf("exposure: already at %s exposure", to)
	}
	zone, _ := payload["zone"].(string)
	var publicIP netip.Addr
	if text, _ := payload["public_ip"].(string); text != "" {
		publicIP, err = netip.ParseAddr(text)
		if err != nil {
			return Request{}, fmt.Errorf("exposure: public address: %w", err)
		}
	}
	certificates, err := decodeCertificates(payload)
	if err != nil {
		return Request{}, err
	}
	return Request{
		Route: route, To: docker.Exposure(to),
		Zone: zone, PublicIP: publicIP, Certificates: certificates,
	}, nil
}

// Save validates the payload for step and persists it. Target and later
// steps run the planner, proving the selection reviews to a plan
// instead of persisting hope.
func (w *Wizard) Save(step string, payload map[string]any) error {
	idx := exposureStepIndex(step)
	if idx < 0 {
		return fmt.Errorf("exposure: unknown step %q", step)
	}
	if _, err := decodeRoute(payload); err != nil {
		return err
	}
	if idx >= exposureStepIndex("target") {
		if _, err := w.request(payload); err != nil {
			return err
		}
	}
	if idx >= exposureStepIndex("review") {
		if _, err := w.planPayload(payload); err != nil {
			return err
		}
	}
	_, err := w.wizards.Put(ExposureWizardID, step, payload)
	return err
}

// planPayload runs the planner over a validated payload.
func (w *Wizard) planPayload(payload map[string]any) (Plan, error) {
	req, err := w.request(payload)
	if err != nil {
		return Plan{}, err
	}
	return PlanExposure(req)
}

// selection loads the saved payload.
func (w *Wizard) selection() (map[string]any, error) {
	state, err := w.wizards.Get(ExposureWizardID)
	if err != nil {
		return nil, err
	}
	return state.Payload, nil
}

// Plan recomputes the reviewed artifact from saved inputs: the plan
// never stores, so reviews always reflect their inputs.
func (w *Wizard) Plan() (Plan, error) {
	payload, err := w.selection()
	if err != nil {
		return Plan{}, err
	}
	return w.planPayload(payload)
}
