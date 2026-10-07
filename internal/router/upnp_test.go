package router

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// upnpDouble keeps gateway mapping state across SOAP actions.
type upnpDouble struct {
	t        *testing.T
	mappings map[string]string
	leases   map[string]int
}

func (d *upnpDouble) action(body string) string {
	for _, action := range []string{"AddPortMapping", "DeletePortMapping", "GetSpecificPortMappingEntry"} {
		if strings.Contains(body, "<u:"+action+" ") || strings.Contains(body, "<u:"+action+">") {
			return action
		}
	}
	return ""
}

func (d *upnpDouble) field(body, name string) string {
	open := "<New" + name + ">"
	start := strings.Index(body, open)
	if start < 0 {
		return ""
	}
	rest := body[start+len(open):]
	end := strings.Index(rest, "</New"+name+">")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func (d *upnpDouble) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body := string(raw)
	key := strings.ToUpper(d.field(body, "Protocol")) + "/" + d.field(body, "ExternalPort")
	switch d.action(body) {
	case "AddPortMapping":
		target := d.field(body, "InternalClient") + ":" + d.field(body, "InternalPort")
		if existing, ok := d.mappings[key]; ok && existing != target {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<fault><detail><UPnPError><errorCode>718</errorCode></UPnPError></detail></fault>`))
			return
		}
		if d.mappings == nil {
			d.mappings = map[string]string{}
		}
		d.mappings[key] = target
		if d.leases == nil {
			d.leases = map[string]int{}
		}
		lease := 0
		for _, ch := range d.field(body, "LeaseDuration") {
			lease = lease*10 + int(ch-'0')
		}
		d.leases[key] = lease
		_, _ = w.Write([]byte(`<Envelope><Body><AddPortMappingResponse/></Body></Envelope>`))
	case "DeletePortMapping":
		if _, ok := d.mappings[key]; !ok {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<fault><detail><UPnPError><errorCode>714</errorCode></UPnPError></detail></fault>`))
			return
		}
		delete(d.mappings, key)
		_, _ = w.Write([]byte(`<Envelope><Body><DeletePortMappingResponse/></Body></Envelope>`))
	case "GetSpecificPortMappingEntry":
		target, ok := d.mappings[key]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<fault><detail><UPnPError><errorCode>714</errorCode></UPnPError></detail></fault>`))
			return
		}
		parts := strings.Split(target, ":")
		_, _ = w.Write([]byte(`<Envelope><Body><GetSpecificPortMappingEntryResponse>` +
			`<NewInternalClient>` + parts[0] + `</NewInternalClient>` +
			`<NewInternalPort>` + parts[1] + `</NewInternalPort>` +
			`</GetSpecificPortMappingEntryResponse></Body></Envelope>`))
	default:
		d.t.Fatalf("unknown action in %q", body)
	}
}

func serveUPnP(t *testing.T, d *upnpDouble) *UPnP {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(d.handler))
	t.Cleanup(srv.Close)
	provider, err := NewUPnP(srv.URL, true)
	if err != nil {
		t.Fatalf("NewUPnP: %v", err)
	}
	return provider
}

func upnpMapping() Mapping {
	return Mapping{
		Protocol: ProtocolTCP, ExternalPort: 80,
		InternalClient: netip.MustParseAddr("192.168.1.10"), InternalPort: 80,
		Lifetime: time.Hour, Description: "web",
	}
}

func TestUPnPContract(t *testing.T) {
	var _ Provider = (*UPnP)(nil)
	provider, err := NewUPnP("http://192.168.1.1:1900/ctl", true)
	if err != nil {
		t.Fatalf("NewUPnP: %v", err)
	}
	if provider.Name() != "upnp" || !provider.Enabled() {
		t.Fatalf("provider = %+v", provider.Name())
	}
	caps := provider.Capabilities()
	if len(caps) != 2 {
		t.Fatalf("caps = %v", caps)
	}
	if err := ValidateProvider(provider); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
	for _, raw := range []string{"", "ftp://x/", "http://"} {
		if _, err := NewUPnP(raw, true); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestUPnPAddDeleteQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	double := &upnpDouble{t: t}
	provider := serveUPnP(t, double)
	if err := provider.AddMapping(ctx, upnpMapping()); err != nil {
		t.Fatalf("AddMapping: %v", err)
	}
	if double.leases["TCP/80"] != 3600 {
		t.Fatalf("leases = %v", double.leases)
	}
	// Refresh converges on identical targets.
	if err := provider.AddMapping(ctx, upnpMapping()); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	got, err := provider.QueryMapping(ctx, ProtocolTCP, 80)
	if err != nil {
		t.Fatalf("QueryMapping: %v", err)
	}
	if got.InternalClient.String() != "192.168.1.10" || got.InternalPort != 80 || got.ExternalPort != 80 {
		t.Fatalf("mapping = %+v", got)
	}
	if err := provider.DeleteMapping(ctx, upnpMapping()); err != nil {
		t.Fatalf("DeleteMapping: %v", err)
	}
	// Absent deletes converge; absent queries report.
	if err := provider.DeleteMapping(ctx, upnpMapping()); err != nil {
		t.Fatalf("re-delete: %v", err)
	}
	if _, err := provider.QueryMapping(ctx, ProtocolTCP, 80); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("err = %v", err)
	}
	// Conflicts fail loudly.
	other := upnpMapping()
	other.InternalClient = netip.MustParseAddr("192.168.1.99")
	if err := provider.AddMapping(ctx, upnpMapping()); err != nil {
		t.Fatalf("AddMapping: %v", err)
	}
	if err := provider.AddMapping(ctx, other); err == nil ||
		!strings.Contains(err.Error(), "718") {
		t.Fatalf("err = %v", err)
	}
	// Disabled automation refuses before the network.
	off := &UPnP{ControlURL: "http://192.168.1.1:1900/ctl"}
	if err := off.AddMapping(ctx, upnpMapping()); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v", err)
	}
	if err := off.DeleteMapping(ctx, upnpMapping()); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v", err)
	}
	if err := provider.AddMapping(ctx, Mapping{}); err == nil {
		t.Fatal("added invalid mapping")
	}
}
