package router

import (
	"context"
	"encoding/json"
	"io"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"

	"homestead/internal/store"
	"homestead/internal/wizard"
)

func openSetupWizard(t *testing.T) *Wizard {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewWizard(wizard.New(s))
}

func gatewayPayload() map[string]any {
	raw, _ := json.Marshal(GatewayInfo{
		Gateway:    Gateway{Address: netip.MustParseAddr("192.168.1.1"), Interface: "eth0"},
		ExternalIP: netip.MustParseAddr("203.0.113.7"),
		Protocols:  []MappingProtocol{ProtocolUPnP},
	})
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	return map[string]any{
		"gateway":  decoded,
		"purposes": []string{"web"},
		"lan_ip":   "192.168.1.10",
	}
}

func TestSetupWizardFlow(t *testing.T) {
	w := openSetupWizard(t)
	payload := gatewayPayload()
	for _, step := range SetupSteps {
		if step == "validate" || step == "done" {
			payload["confirmed"] = true
		}
		if err := w.Save(step, payload); err != nil {
			t.Fatalf("save %s: %v", step, err)
		}
	}
	plan, err := w.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Gateway.Address.String() != "192.168.1.1" || len(plan.Forwards) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.Contains(plan.Instructions, "192.168.1.10 80") {
		t.Fatalf("instructions = %q", plan.Instructions)
	}
}

func TestSetupWizardRejects(t *testing.T) {
	w := openSetupWizard(t)
	cases := []struct {
		name   string
		step   string
		mutate func(map[string]any)
		want   string
	}{
		{"unknown step", "nope", func(m map[string]any) {}, "unknown step"},
		{"no gateway", "gateway", func(m map[string]any) { delete(m, "gateway") }, "needs gateway facts"},
		{"bad gateway", "gateway", func(m map[string]any) {
			m["gateway"] = map[string]any{"Gateway": map[string]any{"Address": "127.0.0.1", "Interface": "lo"}}
		}, "no usable default gateway"},
		{"no purposes", "purposes", func(m map[string]any) { delete(m, "purposes") }, "needs purposes"},
		{"bad purpose", "purposes", func(m map[string]any) { m["purposes"] = []string{"ftp"} }, "unknown purpose"},
		{"bad lan", "instructions", func(m map[string]any) { m["lan_ip"] = "nope" }, "must be usable"},
		{"unconfirmed", "validate", func(m map[string]any) { delete(m, "confirmed") }, "confirm the rules"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := gatewayPayload()
			tc.mutate(payload)
			err := w.Save(tc.step, payload)
			if err == nil {
				t.Fatal("saved invalid payload")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	empty := openSetupWizard(t)
	if _, err := empty.Plan(); err == nil {
		t.Fatal("planned without selection")
	}
}

// soapDouble answers mapping queries: present entries succeed with
// their target, absent ones fail 714, and one path errors outright.
func soapDouble(t *testing.T, present map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		port := ""
		if start := strings.Index(body, "<NewExternalPort>"); start >= 0 {
			rest := body[start+len("<NewExternalPort>"):]
			if end := strings.Index(rest, "</NewExternalPort>"); end >= 0 {
				port = rest[:end]
			}
		}
		if port == "9999" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		target, ok := present[port]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<error><errorCode>714</errorCode><errorDescription>NoSuchEntry</errorDescription></error>`))
			return
		}
		parts := strings.Split(target, ":")
		_, _ = w.Write([]byte(`<Envelope><Body><GetSpecificPortMappingEntryResponse>` +
			`<NewInternalClient>` + parts[0] + `</NewInternalClient>` +
			`<NewInternalPort>` + parts[1] + `</NewInternalPort>` +
			`</GetSpecificPortMappingEntryResponse></Body></Envelope>`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestQueryMappings(t *testing.T) {
	upnp := UPnPInfo{ControlURL: soapDouble(t, map[string]string{"80": "192.168.1.10:80"})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lanIP := netip.MustParseAddr("192.168.1.10")
	forwards := []Forward{
		{Name: "web-http", Protocol: ProtocolTCP, Port: 80},
		{Name: "web-https", Protocol: ProtocolTCP, Port: 443},
	}
	checks, err := QueryMappings(ctx, upnp, lanIP, forwards)
	if err != nil {
		t.Fatalf("QueryMappings: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("checks = %+v", checks)
	}
	if !checks[0].Found || !checks[0].Matches || checks[0].Internal != "192.168.1.10 80" {
		t.Fatalf("check = %+v", checks[0])
	}
	if checks[1].Found || checks[1].Matches {
		t.Fatalf("check = %+v", checks[1])
	}
	// Wrong target reports found without matching.
	off := UPnPInfo{ControlURL: soapDouble(t, map[string]string{"443": "192.168.1.99:443"})}
	checks, err = QueryMappings(ctx, off, lanIP, forwards[1:])
	if err != nil {
		t.Fatalf("QueryMappings: %v", err)
	}
	if !checks[0].Found || checks[0].Matches {
		t.Fatalf("check = %+v", checks[0])
	}
	// Gateway errors surface; missing control URL refuses.
	broken := UPnPInfo{ControlURL: soapDouble(t, nil)}
	_, err = QueryMappings(ctx, broken, lanIP, []Forward{{Name: "x", Protocol: ProtocolTCP, Port: 9999}})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if _, err := QueryMappings(ctx, UPnPInfo{}, lanIP, forwards); err == nil {
		t.Fatal("queried without control URL")
	}
}
