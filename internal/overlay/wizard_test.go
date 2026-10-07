package overlay

import (
	"context"
	"encoding/json"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

func openRemoteWizard(t *testing.T) *RemoteWizard {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	secrets, err := secret.New(s, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	return NewRemoteWizard(wizard.New(s), secrets)
}

func configPayload(t *testing.T, config Config, extra map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	payload := map[string]any{"provider": string(config.Provider), "config": decoded}
	for key, value := range extra {
		payload[key] = value
	}
	return payload
}

func tailscaleConfig() Config {
	return Config{
		Provider: ProviderTailscale,
		Hostname: "homestead",
		AdvertiseRoutes: []SubnetRoute{
			{Destination: netip.MustParsePrefix("192.168.1.0/24"), Enabled: true},
		},
		AcceptRoutes: true,
	}
}

func headscalePayload(t *testing.T) map[string]any {
	config := tailscaleConfig()
	config.Provider = ProviderHeadscale
	return configPayload(t, config, map[string]any{
		"server_url": "https://headscale.example.com",
		"user":       "owner",
	})
}

func TestRemoteSaveFlow(t *testing.T) {
	w := openRemoteWizard(t)
	payload := configPayload(t, tailscaleConfig(), nil)
	for _, step := range RemoteSteps {
		if err := w.Save(step, payload); err != nil {
			t.Fatalf("save %s: %v", step, err)
		}
	}
	plan, err := w.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Config.Hostname != "homestead" || plan.Server != "" || plan.User != "" {
		t.Fatalf("plan = %+v", plan)
	}
	head := openRemoteWizard(t)
	hp := headscalePayload(t)
	if err := head.Save("settings", hp); err != nil {
		t.Fatalf("save: %v", err)
	}
	plan, err = head.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Server != "https://headscale.example.com" || plan.User != "owner" {
		t.Fatalf("plan = %+v", plan)
	}
	none := openRemoteWizard(t)
	off := Config{Provider: ProviderNone}
	if err := none.Save("done", configPayload(t, off, nil)); err != nil {
		t.Fatalf("none save: %v", err)
	}
	if err := none.ValidateConnectivity(context.Background()); err != nil {
		t.Fatalf("none validate: %v", err)
	}
}

func TestRemoteSaveRejects(t *testing.T) {
	w := openRemoteWizard(t)
	good := configPayload(t, tailscaleConfig(), nil)
	cases := []struct {
		name    string
		step    string
		payload map[string]any
		want    string
	}{
		{"unknown step", "nope", good, "unknown step"},
		{"bad provider", "provider", map[string]any{"provider": "nebula"}, "none, tailscale, or headscale"},
		{"no settings", "settings", map[string]any{"provider": "tailscale"}, "need config"},
		{"headscale no server", "settings", configPayload(t, func() Config {
			c := tailscaleConfig()
			c.Provider = ProviderHeadscale
			return c
		}(), nil), "http or https"},
		{"authkey in state", "credentials", configPayload(t, tailscaleConfig(),
			map[string]any{"authkey": ""}), "must not be empty"},
		{"preauth in state", "credentials", headscalePayload(t), "secret store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := tc.payload
			if tc.name == "preauth in state" {
				payload = headscalePayload(t)
				payload["preauth"] = "leak"
			}
			err := w.Save(tc.step, payload)
			if err == nil {
				t.Fatal("saved invalid payload")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	t.Run("provider drift", func(t *testing.T) {
		payload := headscalePayload(t)
		payload["provider"] = "tailscale"
		err := w.Save("settings", payload)
		if err == nil || !strings.Contains(err.Error(), "disagrees") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestRemoteCredentials(t *testing.T) {
	w := openRemoteWizard(t)
	if err := w.StoreCredentials(ProviderTailscale, nil); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if err := w.StoreCredentials(ProviderTailscale, map[string]string{"authkey": "key"}); err != nil {
		t.Fatalf("key: %v", err)
	}
	if err := w.StoreCredentials(ProviderTailscale, map[string]string{"a": "1", "b": "2"}); err == nil {
		t.Fatal("stored extras")
	}
	if err := w.StoreCredentials(ProviderHeadscale, map[string]string{"preauth": "key"}); err != nil {
		t.Fatalf("preauth: %v", err)
	}
	if err := w.StoreCredentials(ProviderHeadscale, nil); err == nil {
		t.Fatal("stored nothing")
	}
	if err := w.StoreCredentials(ProviderNone, nil); err == nil {
		t.Fatal("stored for none")
	}
}

func TestRemotePrerequisites(t *testing.T) {
	ctx := context.Background()
	online := `{"BackendState": "Running",
		"Self": {"HostName": "homestead", "TailscaleIPs": ["100.64.0.1"]}, "Peer": {}}`
	stubTailscale(t, func(args []string) (string, error) {
		if len(args) == 1 && args[0] == "version" {
			return "1.80.0", nil
		}
		return online, nil
	})
	w := openRemoteWizard(t)
	payload := configPayload(t, tailscaleConfig(), nil)
	if err := w.Save("settings", payload); err != nil {
		t.Fatalf("save: %v", err)
	}
	prereq, err := w.Prerequisites(ctx)
	if err != nil {
		t.Fatalf("Prerequisites: %v", err)
	}
	if !prereq.Ready || !strings.Contains(prereq.Detail, "existing login") {
		t.Fatalf("prereq = %+v", prereq)
	}
	if err := w.ValidateConnectivity(ctx); err != nil {
		t.Fatalf("ValidateConnectivity: %v", err)
	}
	// Headscale without key is not ready; with key it is.
	head := openRemoteWizard(t)
	if err := head.Save("settings", headscalePayload(t)); err != nil {
		t.Fatalf("save: %v", err)
	}
	prereq, err = head.Prerequisites(ctx)
	if err != nil {
		t.Fatalf("Prerequisites: %v", err)
	}
	if prereq.Ready || !strings.Contains(prereq.Detail, "not stored") {
		t.Fatalf("prereq = %+v", prereq)
	}
	if err := head.StoreCredentials(ProviderHeadscale, map[string]string{"preauth": "key"}); err != nil {
		t.Fatalf("store: %v", err)
	}
	prereq, err = head.Prerequisites(ctx)
	if err != nil {
		t.Fatalf("Prerequisites: %v", err)
	}
	if !prereq.Ready {
		t.Fatalf("prereq = %+v", prereq)
	}
	// Missing binary fails prerequisites and connectivity alike.
	stubTailscale(t, func(args []string) (string, error) {
		return "", ErrTailscaleAbsent
	})
	prereq, err = head.Prerequisites(ctx)
	if err != nil {
		t.Fatalf("Prerequisites: %v", err)
	}
	if prereq.Ready {
		t.Fatalf("prereq = %+v", prereq)
	}
	if err := head.ValidateConnectivity(ctx); err == nil {
		t.Fatal("validated without binary")
	}
	// Offline node fails connectivity.
	stubTailscale(t, func(args []string) (string, error) {
		return `{"BackendState": "Stopped", "Self": {}, "Peer": {}}`, nil
	})
	if err := w.ValidateConnectivity(ctx); err == nil ||
		!strings.Contains(err.Error(), "not online") {
		t.Fatalf("err = %v", err)
	}
	empty := openRemoteWizard(t)
	if _, err := empty.Prerequisites(ctx); err == nil {
		t.Fatal("prereqs without selection")
	}
}
