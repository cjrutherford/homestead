package overlay

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/dns"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

const bootstrapOnline = `{"BackendState": "Running",
	"Self": {"HostName": "homestead", "TailscaleIPs": ["100.64.0.1", "fd7a:115c:a1e0::1"]}, "Peer": {}}`

const bootstrapOffline = `{"BackendState": "Stopped", "Self": {}, "Peer": {}}`

// openBootstrap builds a RemoteWizard and its store together: the
// bootstrap needs both handles over the same database.
func openBootstrap(t *testing.T) (*store.Store, *RemoteWizard) {
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
	return s, NewRemoteWizard(wizard.New(s), secrets)
}

func saveTailscale(t *testing.T, wiz *RemoteWizard) {
	t.Helper()
	if err := wiz.Save("done", configPayload(t, tailscaleConfig(), nil)); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func TestEstablishOverlayFullFlow(t *testing.T) {
	ctx := context.Background()
	stubTailscale(t, func(args []string) (string, error) { return bootstrapOnline, nil })
	st, wiz := openBootstrap(t)
	saveTailscale(t, wiz)
	ready, err := EstablishOverlay(ctx, st, wiz, "example.com", "home")
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if ready.Provider != "tailscale" || ready.Skipped {
		t.Fatalf("ready = %+v", ready)
	}
	if len(ready.Addresses) != 2 || ready.Addresses[0] != "100.64.0.1" {
		t.Fatalf("addresses = %+v", ready)
	}
	if ready.View.Audience != dns.AudienceOverlay || len(ready.View.Records) != 2 {
		t.Fatalf("view = %+v", ready.View)
	}
	for _, record := range ready.View.Records {
		if record.Name != "home" || (record.Type != dns.RecordA && record.Type != dns.RecordAAAA) {
			t.Fatalf("record = %+v", record)
		}
	}
	state, err := CurrentOverlay(st)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if state.Provider != "tailscale" || len(state.Addresses) != 2 {
		t.Fatalf("state = %+v", state)
	}
	// Re-running converges on the latest live state.
	again, err := EstablishOverlay(ctx, st, wiz, "example.com", "home")
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if again.Provider != "tailscale" || len(again.Addresses) != 2 {
		t.Fatalf("ready = %+v", again)
	}
}

func TestEstablishOverlaySkipsLocalOnly(t *testing.T) {
	ctx := context.Background()
	st, wiz := openBootstrap(t)
	if err := wiz.Save("done", configPayload(t, Config{Provider: ProviderNone}, nil)); err != nil {
		t.Fatalf("save: %v", err)
	}
	ready, err := EstablishOverlay(ctx, st, wiz, "example.com", "home")
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if !ready.Skipped || ready.Provider != "none" || len(ready.Addresses) != 0 {
		t.Fatalf("ready = %+v", ready)
	}
	state, err := CurrentOverlay(st)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if state.Provider != "none" || len(state.Addresses) != 0 {
		t.Fatalf("state = %+v", state)
	}
}

func TestEstablishOverlayRefusals(t *testing.T) {
	ctx := context.Background()
	stubTailscale(t, func(args []string) (string, error) { return bootstrapOnline, nil })
	st, wiz := openBootstrap(t)
	// No selection saved: nothing to reach.
	if _, err := EstablishOverlay(ctx, st, wiz, "example.com", "home"); err == nil {
		t.Fatal("unselected bootstrap accepted")
	}
	saveTailscale(t, wiz)
	if _, err := EstablishOverlay(ctx, nil, wiz, "example.com", "home"); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := EstablishOverlay(ctx, st, nil, "example.com", "home"); err == nil {
		t.Fatal("nil wizard accepted")
	}
	if _, err := EstablishOverlay(ctx, st, wiz, "Bad_Zone!", "home"); err == nil {
		t.Fatal("bad zone accepted")
	}
	if _, err := EstablishOverlay(ctx, st, wiz, "example.com", "Bad"); err == nil {
		t.Fatal("bad base accepted")
	}
	// Offline nodes fail connectivity with nothing recorded.
	stubTailscale(t, func(args []string) (string, error) { return bootstrapOffline, nil })
	if _, err := EstablishOverlay(ctx, st, wiz, "example.com", "home"); err == nil ||
		!strings.Contains(err.Error(), "not online") {
		t.Fatalf("offline bootstrap = %v", err)
	}
	if _, err := CurrentOverlay(st); err != ErrNoOverlay {
		t.Fatalf("Current = %v", err)
	}
}

func TestCurrentOverlayEmpty(t *testing.T) {
	st, _ := openBootstrap(t)
	if _, err := CurrentOverlay(st); err != ErrNoOverlay {
		t.Fatalf("Current = %v", err)
	}
}
