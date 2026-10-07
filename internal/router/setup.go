package router

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"homestead/internal/wizard"
)

// SetupWizardID identifies the assisted-setup flow in the generic
// wizard store (S036). Payloads carry gateway facts, purposes, and the
// appliance address — all non-sensitive — while instructions recompute
// and mappings query live, so nothing goes stale beside its inputs.
const SetupWizardID = "router-setup"

// SetupSteps orders the flow: detected gateway, serving purposes,
// rendered instructions, confirmed mappings, done. External proof of
// reachability belongs to verification (S133); this flow guides and
// confirms locally.
var SetupSteps = []string{"gateway", "purposes", "instructions", "validate", "done"}

// setupStepIndex orders steps; -1 means unknown.
func setupStepIndex(step string) int {
	for i, name := range SetupSteps {
		if name == step {
			return i
		}
	}
	return -1
}

// Wizard guides router setup over the wizard store. It owns no HTTP
// surface: the generic wizard endpoints and later slices drive it.
type Wizard struct {
	wizards *wizard.Service
}

// NewWizard returns a Wizard.
func NewWizard(wizards *wizard.Service) *Wizard {
	return &Wizard{wizards: wizards}
}

// decodeGateway renders the gateway payload back into validated facts:
// the full discovery document, with a usable gateway address on a
// named interface. The whole document persists (UPnP details feed
// QueryMappings), not just the address.
func decodeGateway(payload map[string]any) (GatewayInfo, error) {
	raw, ok := payload["gateway"]
	if !ok {
		return GatewayInfo{}, fmt.Errorf("router: gateway step needs gateway facts")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return GatewayInfo{}, fmt.Errorf("router: gateway encode: %w", err)
	}
	var info GatewayInfo
	if err := json.Unmarshal(encoded, &info); err != nil {
		return GatewayInfo{}, fmt.Errorf("router: gateway decode: %w", err)
	}
	gateway, err := gatewayFrom(info.Gateway.Interface, info.Gateway.Address.String())
	if err != nil {
		return GatewayInfo{}, err
	}
	info.Gateway = gateway
	return info, nil
}

// decodePurposes renders the purposes payload into a validated,
// deduplicated forward set.
func decodePurposes(payload map[string]any) ([]Forward, error) {
	raw, ok := payload["purposes"]
	if !ok {
		return nil, fmt.Errorf("router: purposes step needs purposes")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("router: purposes encode: %w", err)
	}
	var purposes []Purpose
	if err := json.Unmarshal(encoded, &purposes); err != nil {
		return nil, fmt.Errorf("router: purposes decode: %w", err)
	}
	if len(purposes) == 0 {
		return nil, fmt.Errorf("router: at least one purpose is required")
	}
	return RequiredSet(purposes...)
}

// decodeAppliance renders the appliance address for instruction
// targeting: the LAN address forwards point at.
func decodeAppliance(payload map[string]any) (netip.Addr, error) {
	text, _ := payload["lan_ip"].(string)
	addr, err := netip.ParseAddr(text)
	if err != nil || !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() {
		return netip.Addr{}, fmt.Errorf("router: appliance address must be usable")
	}
	return addr, nil
}

// Save validates the payload for step and persists it: gateway facts,
// purposes, renderable instructions, then confirmation. The validate
// step records owner attestation that rules were applied — only the
// owner sees their router UI — with UPnP gateways cross-checked by
// QueryMappings first at the caller's layer.
func (w *Wizard) Save(step string, payload map[string]any) error {
	idx := setupStepIndex(step)
	if idx < 0 {
		return fmt.Errorf("router: unknown step %q", step)
	}
	if _, err := decodeGateway(payload); err != nil {
		return err
	}
	if idx >= setupStepIndex("purposes") {
		if _, err := decodePurposes(payload); err != nil {
			return err
		}
	}
	if idx >= setupStepIndex("instructions") {
		if _, err := w.instructionsPayload(payload); err != nil {
			return err
		}
	}
	if idx >= setupStepIndex("validate") {
		confirmed, _ := payload["confirmed"].(bool)
		if !confirmed {
			return fmt.Errorf("router: confirm the rules exist before finishing")
		}
	}
	_, err := w.wizards.Put(SetupWizardID, step, payload)
	return err
}

// instructionsPayload renders instructions for validated inputs.
func (w *Wizard) instructionsPayload(payload map[string]any) (string, error) {
	forwards, err := decodePurposes(payload)
	if err != nil {
		return "", err
	}
	addr, err := decodeAppliance(payload)
	if err != nil {
		return "", err
	}
	return RenderInstructions(addr, forwards)
}

// selection loads the saved payload.
func (w *Wizard) selection() (map[string]any, error) {
	state, err := w.wizards.Get(SetupWizardID)
	if err != nil {
		return nil, err
	}
	return state.Payload, nil
}

// SetupPlan is the reviewable outcome: gateway facts, forwards, and
// rendered instructions. Mappings themselves verify through
// QueryMappings (UPnP) and verification (S133).
type SetupPlan struct {
	Gateway      Gateway
	Forwards     []Forward
	Instructions string
}

// Plan recomputes the setup outcome from saved inputs.
func (w *Wizard) Plan() (SetupPlan, error) {
	payload, err := w.selection()
	if err != nil {
		return SetupPlan{}, err
	}
	info, err := decodeGateway(payload)
	if err != nil {
		return SetupPlan{}, err
	}
	forwards, err := decodePurposes(payload)
	if err != nil {
		return SetupPlan{}, err
	}
	instructions, err := w.instructionsPayload(payload)
	if err != nil {
		return SetupPlan{}, err
	}
	return SetupPlan{Gateway: info.Gateway, Forwards: forwards, Instructions: instructions}, nil
}

// MappingCheck reports one forward's presence in the gateway: found
// with the actual internal target, whether that target matches the
// appliance, and the evidence either way.
type MappingCheck struct {
	Forward  Forward
	Found    bool
	Matches  bool
	Internal string
}

// wanServices tries WANIPConnection first, then WANPPPConnection:
// descriptions do not say which the control URL serves, so both are
// attempted before conceding.
var wanServices = []string{
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

// QueryMappings asks a UPnP gateway which forwards exist, one
// GetSpecificPortMappingEntry per rule. Error 714 (no such entry)
// means absent; anything else errors. Non-UPnP gateways have nothing
// to ask — their mappings prove externally (S133) — so this helper is
// UPnP-only by design.
func QueryMappings(ctx context.Context, upnp UPnPInfo, lanIP netip.Addr, forwards []Forward) ([]MappingCheck, error) {
	if upnp.ControlURL == "" {
		return nil, fmt.Errorf("router: no UPnP control URL")
	}
	var out []MappingCheck
	for i := range forwards {
		check, err := queryMapping(ctx, upnp.ControlURL, lanIP, forwards[i])
		if err != nil {
			return nil, err
		}
		out = append(out, check)
	}
	return out, nil
}

// queryMapping resolves one forward through both WAN services.
func queryMapping(ctx context.Context, controlURL string, lanIP netip.Addr, forward Forward) (MappingCheck, error) {
	check := MappingCheck{Forward: forward}
	var missing bool
	for _, service := range wanServices {
		client, port, found, err := getMapping(ctx, controlURL, service, forward)
		if err != nil {
			if strings.Contains(err.Error(), "714") {
				missing = true
				continue
			}
			return check, err
		}
		if !found {
			missing = true
			continue
		}
		check.Found = true
		check.Internal = fmt.Sprintf("%s %d", client, port)
		check.Matches = client == lanIP.String() && port == forward.Port
		return check, nil
	}
	if missing {
		return check, nil
	}
	return check, fmt.Errorf("router: no WAN service answered")
}

// getMapping performs one SOAP query, reporting found with the
// internal target. A 500 carrying 714 reports absent without error.
func getMapping(ctx context.Context, controlURL, service string, forward Forward) (string, int, bool, error) {
	body := fmt.Sprintf(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<s:Body><u:GetSpecificPortMappingEntry xmlns:u="%s">
<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort><NewProtocol>%s</NewProtocol>
</u:GetSpecificPortMappingEntry></s:Body></s:Envelope>`,
		service, forward.Port, strings.ToUpper(forward.Protocol))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL, strings.NewReader(body))
	if err != nil {
		return "", 0, false, fmt.Errorf("router: mapping request: %w", err)
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", fmt.Sprintf("%s#GetSpecificPortMappingEntry", service))
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, false, fmt.Errorf("router: mapping query: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, false, fmt.Errorf("router: mapping read: %w", err)
	}
	if resp.StatusCode == 200 {
		var envelope struct {
			Body struct {
				Response struct {
					InternalClient string `xml:"NewInternalClient"`
					InternalPort   int    `xml:"NewInternalPort"`
				} `xml:"GetSpecificPortMappingEntryResponse"`
			} `xml:"Body"`
		}
		if err := xml.Unmarshal(raw, &envelope); err != nil {
			return "", 0, false, fmt.Errorf("router: mapping decode: %w", err)
		}
		return envelope.Body.Response.InternalClient, envelope.Body.Response.InternalPort, true, nil
	}
	if resp.StatusCode == 500 && strings.Contains(string(raw), "714") {
		return "", 0, false, nil
	}
	return "", 0, false, fmt.Errorf("router: mapping query status %s: %s", resp.Status, strings.TrimSpace(string(raw)))
}
