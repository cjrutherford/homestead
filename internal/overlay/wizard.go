package overlay

import (
	"context"
	"encoding/json"
	"fmt"

	"homestead/internal/secret"
	"homestead/internal/wizard"
)

// RemoteWizardID identifies the remote-access flow in the generic
// wizard store (S036). Like its siblings (S097, S106), secrets never
// touch the payload: auth material lives under the adapter conventions
// (TailscaleAuthKeySecret, HeadscalePreauthSecret).
const RemoteWizardID = "remote"

// RemoteSteps orders the flow: choose provider, shape the overlay
// config, store credentials, prove connectivity, done. Enrolment
// itself runs between credentials and validation through the S111/S113
// operations at the caller's layer — the wizard guides and verifies,
// never enrols.
var RemoteSteps = []string{"provider", "settings", "credentials", "validate", "done"}

// remoteStepIndex orders steps; -1 means unknown.
func remoteStepIndex(step string) int {
	for i, name := range RemoteSteps {
		if name == step {
			return i
		}
	}
	return -1
}

// RemotePlan is the wizard outcome: the overlay config plus, for
// Headscale, the server origin and user enrolment belongs to. Callers
// enrol from it, then validate connectivity back through the wizard.
type RemotePlan struct {
	Config Config
	Server string
	User   string
}

// RemoteWizard guides remote-access setup over the wizard and secret
// stores. It owns no HTTP surface: the generic wizard endpoints and
// later slices drive it.
type RemoteWizard struct {
	wizards *wizard.Service
	secrets *secret.Store
}

// NewRemoteWizard returns a RemoteWizard.
func NewRemoteWizard(wizards *wizard.Service, secrets *secret.Store) *RemoteWizard {
	return &RemoteWizard{wizards: wizards, secrets: secrets}
}

// decodeRemoteConfig renders the settings payload back into a validated
// Config: JSON round-trip keeps the store format-agnostic while the
// model stays authoritative.
func decodeRemoteConfig(payload map[string]any) (Config, error) {
	raw, ok := payload["config"]
	if !ok {
		return Config{}, fmt.Errorf("overlay: settings need config")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return Config{}, fmt.Errorf("overlay: settings encode: %w", err)
	}
	var config Config
	if err := json.Unmarshal(encoded, &config); err != nil {
		return Config{}, fmt.Errorf("overlay: settings decode: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// headscaleExtra validates the Headscale settings keys whenever the
// provider needs them: server origin always, user spelling always.
func headscaleExtra(payload map[string]any) (HeadscaleServer, string, error) {
	server, _ := payload["server_url"].(string)
	user, _ := payload["user"].(string)
	headscale := HeadscaleServer{URL: server}
	if err := headscale.Validate(); err != nil {
		return HeadscaleServer{}, "", err
	}
	if !ValidHeadscaleUser(user) {
		return HeadscaleServer{}, "", fmt.Errorf("overlay: headscale user must match [A-Za-z0-9][A-Za-z0-9_.-]{0,63}")
	}
	return headscale, user, nil
}

// Save validates the payload for step and persists it: cumulative
// provider, config, and (for Headscale) server settings.
func (w *RemoteWizard) Save(step string, payload map[string]any) error {
	idx := remoteStepIndex(step)
	if idx < 0 {
		return fmt.Errorf("overlay: unknown step %q", step)
	}
	provider, _ := payload["provider"].(string)
	if Provider(provider) != ProviderNone && Provider(provider) != ProviderTailscale && Provider(provider) != ProviderHeadscale {
		return fmt.Errorf("overlay: provider must be none, tailscale, or headscale")
	}
	if idx >= remoteStepIndex("settings") {
		config, err := decodeRemoteConfig(payload)
		if err != nil {
			return err
		}
		if string(config.Provider) != provider {
			return fmt.Errorf("overlay: settings provider %q disagrees with %q", config.Provider, provider)
		}
		if config.Provider == ProviderHeadscale {
			if _, _, err := headscaleExtra(payload); err != nil {
				return err
			}
		}
	}
	if step == "credentials" {
		switch Provider(provider) {
		case ProviderTailscale:
			for key := range payload {
				if key != "provider" && key != "config" && key != "authkey" {
					return fmt.Errorf("overlay: unexpected credential %q", key)
				}
			}
			if value, present := payload["authkey"]; present {
				if text, _ := value.(string); text == "" {
					return fmt.Errorf("overlay: authkey must not be empty when given")
				}
			}
		case ProviderHeadscale:
			if _, present := payload["preauth"]; present {
				return fmt.Errorf("overlay: preauth belongs in the secret store, not wizard state")
			}
		}
	}
	_, err := w.wizards.Put(RemoteWizardID, step, payload)
	return err
}

// selection loads the saved payload.
func (w *RemoteWizard) selection() (map[string]any, error) {
	state, err := w.wizards.Get(RemoteWizardID)
	if err != nil {
		return nil, err
	}
	return state.Payload, nil
}

// StoreCredentials saves provider auth material under conventional
// names: Tailscale accepts an empty key (adopt existing login) or one
// key, Headscale requires exactly the pre-auth key. Anything else is
// refused rather than silently kept.
func (w *RemoteWizard) StoreCredentials(provider Provider, values map[string]string) error {
	switch provider {
	case ProviderTailscale:
		if len(values) > 1 {
			return fmt.Errorf("overlay: tailscale takes at most an authkey")
		}
		for key, value := range values {
			if key != "authkey" || value == "" {
				return fmt.Errorf("overlay: tailscale takes an authkey or nothing")
			}
			if err := w.secrets.Set(TailscaleAuthKeySecret, value); err != nil {
				return err
			}
		}
		return nil
	case ProviderHeadscale:
		if len(values) != 1 {
			return fmt.Errorf("overlay: headscale needs exactly the pre-auth key")
		}
		value, ok := values["preauth"]
		if !ok || value == "" {
			return fmt.Errorf("overlay: headscale needs exactly the pre-auth key")
		}
		return w.secrets.Set(HeadscalePreauthSecret, value)
	default:
		return fmt.Errorf("overlay: provider must be tailscale or headscale")
	}
}

// Prereq reports setup readiness without enrolling: binary presence,
// complete settings, and stored credentials where required. None is
// trivially ready — local-only needs nothing.
type Prereq struct {
	Ready  bool
	Detail string
}

// Prerequisites proves the saved selection can proceed: settings
// validate, the client binary answers, and required secrets exist.
// Headscale additionally needs its server settings present.
func (w *RemoteWizard) Prerequisites(ctx context.Context) (Prereq, error) {
	payload, err := w.selection()
	if err != nil {
		return Prereq{}, err
	}
	provider, _ := payload["provider"].(string)
	if Provider(provider) == ProviderNone {
		return Prereq{Ready: true, Detail: "local-only needs nothing"}, nil
	}
	config, err := decodeRemoteConfig(payload)
	if err != nil {
		return Prereq{}, err
	}
	if string(config.Provider) != provider {
		return Prereq{}, fmt.Errorf("overlay: settings provider %q disagrees with %q", config.Provider, provider)
	}
	if !Present(ctx) {
		return Prereq{Detail: "tailscale client not installed"}, nil
	}
	switch config.Provider {
	case ProviderTailscale:
		if _, err := w.secrets.Get(TailscaleAuthKeySecret); err != nil {
			return Prereq{Ready: true, Detail: "no key stored: adopts existing login"}, nil
		}
		return Prereq{Ready: true, Detail: "client present, auth key stored"}, nil
	case ProviderHeadscale:
		if _, _, err := headscaleExtra(payload); err != nil {
			return Prereq{}, err
		}
		if _, err := w.secrets.Get(HeadscalePreauthSecret); err != nil {
			return Prereq{Detail: "pre-auth key not stored"}, nil
		}
		return Prereq{Ready: true, Detail: "client present, server set, key stored"}, nil
	default:
		return Prereq{}, fmt.Errorf("overlay: provider must be none, tailscale, or headscale")
	}
}

// ValidateConnectivity proves the overlay serves now: online with
// addresses through the shared status mapping. None passes trivially;
// anything else must actually be up, since the wizard verifies rather
// than assumes.
func (w *RemoteWizard) ValidateConnectivity(ctx context.Context) error {
	payload, err := w.selection()
	if err != nil {
		return err
	}
	provider, _ := payload["provider"].(string)
	if Provider(provider) == ProviderNone {
		return nil
	}
	network, err := Status(ctx)
	if err != nil {
		return err
	}
	if !network.Online {
		return fmt.Errorf("overlay: node not online")
	}
	if len(network.Addresses) == 0 {
		return fmt.Errorf("overlay: node online without addresses")
	}
	return nil
}

// Plan returns the wizard outcome for enrolment callers.
func (w *RemoteWizard) Plan() (RemotePlan, error) {
	payload, err := w.selection()
	if err != nil {
		return RemotePlan{}, err
	}
	config, err := decodeRemoteConfig(payload)
	if err != nil {
		return RemotePlan{}, err
	}
	plan := RemotePlan{Config: config}
	if config.Provider == ProviderHeadscale {
		server, user, err := headscaleExtra(payload)
		if err != nil {
			return RemotePlan{}, err
		}
		plan.Server = server.URL
		plan.User = user
	}
	return plan, nil
}
