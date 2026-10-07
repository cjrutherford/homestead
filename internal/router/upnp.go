package router

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// defaultUPnPLease applies when mappings omit lifetimes: one hour,
// refreshed by callers rather than fossilized as permanent leases.
const defaultUPnPLease = time.Hour

// UPnP automates NAT mappings through InternetGatewayDevice control
// URLs (discovered in S131) behind recorded owner opt-in. Explicit
// review lives one layer up — the assisted flow confirms rules before
// this provider ever runs (S132) — so automation here stays
// mechanical: validated calls, classified failures, nothing clever.
type UPnP struct {
	ControlURL string
	enabled    bool
}

// NewUPnP builds the provider for a control URL with recorded owner
// opt-in. Disabled providers refuse automation with ErrNotEnabled.
func NewUPnP(controlURL string, enabled bool) (*UPnP, error) {
	parsed, err := url.Parse(controlURL)
	if err != nil {
		return nil, fmt.Errorf("router: UPnP control URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("router: UPnP control URL must be http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("router: UPnP control URL must have a host")
	}
	return &UPnP{ControlURL: controlURL, enabled: enabled}, nil
}

// Name identifies the UPnP provider.
func (u *UPnP) Name() string { return "upnp" }

// Capabilities reports mapping plus single-entry query.
func (u *UPnP) Capabilities() []Capability { return []Capability{CapabilityMapping, CapabilityQuery} }

// Enabled reports recorded owner opt-in.
func (u *UPnP) Enabled() bool { return u.enabled }

// QueryMapping reads one mapping entry through both WAN services,
// reporting absence as ErrNoMapping.
func (u *UPnP) QueryMapping(ctx context.Context, protocol string, externalPort int) (Mapping, error) {
	check, err := queryMapping(ctx, u.ControlURL, netip.Addr{}, Forward{Protocol: protocol, Port: externalPort})
	if err != nil {
		return Mapping{}, err
	}
	if !check.Found {
		return Mapping{}, ErrNoMapping
	}
	parts := strings.Split(check.Internal, " ")
	if len(parts) != 2 {
		return Mapping{}, fmt.Errorf("router: UPnP bad internal target %q", check.Internal)
	}
	client, err := netip.ParseAddr(parts[0])
	if err != nil {
		return Mapping{}, fmt.Errorf("router: UPnP bad internal client %q", parts[0])
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil || port < 1 || port > 65535 {
		return Mapping{}, fmt.Errorf("router: UPnP bad internal port %q", parts[1])
	}
	return Mapping{Protocol: protocol, ExternalPort: externalPort, InternalClient: client, InternalPort: port}, nil
}

// soapCall posts one action body to the first answering WAN service,
// trying IP then PPP connection types before conceding.
func (u *UPnP) soapCall(ctx context.Context, action, body string) error {
	var last error
	for _, service := range wanServices {
		envelope := fmt.Sprintf(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<s:Body><u:%s xmlns:u="%s">%s</u:%s></s:Body></s:Envelope>`,
			action, service, body, action)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.ControlURL, strings.NewReader(envelope))
		if err != nil {
			return fmt.Errorf("router: UPnP request: %w", err)
		}
		req.Header.Set("Content-Type", "text/xml; charset=utf-8")
		req.Header.Set("SOAPAction", fmt.Sprintf("%s#%s", service, action))
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			last = fmt.Errorf("router: UPnP %s: %w", action, err)
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			last = fmt.Errorf("router: UPnP %s read: %w", action, err)
			continue
		}
		if resp.StatusCode == 200 {
			return nil
		}
		if code := soapErrorCode(raw); code != "" {
			last = fmt.Errorf("router: UPnP %s failed (%s)", action, code)
			continue
		}
		last = fmt.Errorf("router: UPnP %s status %s", action, resp.Status)
	}
	return last
}

// soapErrorCode extracts UPnP error codes (714 absent, 718 conflict)
// from fault bodies for classified failures.
func soapErrorCode(raw []byte) string {
	var fault struct {
		Detail struct {
			Code string `xml:"UPnPError>errorCode"`
		} `xml:"detail"`
	}
	if err := xml.Unmarshal(raw, &fault); err != nil {
		return ""
	}
	return strings.TrimSpace(fault.Detail.Code)
}

// AddMapping creates or refreshes one mapping with an explicit lease.
// Conflicts (718, already mapped elsewhere) fail loudly with the
// code attached instead of silently taking over.
func (u *UPnP) AddMapping(ctx context.Context, mapping Mapping) error {
	if !u.enabled {
		return ErrNotEnabled
	}
	if err := mapping.Validate(); err != nil {
		return err
	}
	lease := mapping.Lifetime
	if lease == 0 {
		lease = defaultUPnPLease
	}
	body := fmt.Sprintf(`<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort>`+
		`<NewProtocol>%s</NewProtocol><NewInternalPort>%d</NewInternalPort>`+
		`<NewInternalClient>%s</NewInternalClient><NewEnabled>1</NewEnabled>`+
		`<NewPortMappingDescription>%s</NewPortMappingDescription><NewLeaseDuration>%d</NewLeaseDuration>`,
		mapping.ExternalPort, strings.ToUpper(mapping.Protocol), mapping.InternalPort,
		mapping.InternalClient.String(), xmlEscape(mapping.Description), int64(lease/time.Second))
	return u.soapCall(ctx, "AddPortMapping", body)
}

// DeleteMapping removes one mapping by key. Absent entries (714)
// converge to success; anything else errors.
func (u *UPnP) DeleteMapping(ctx context.Context, mapping Mapping) error {
	if !u.enabled {
		return ErrNotEnabled
	}
	if err := mapping.Validate(); err != nil {
		return err
	}
	body := fmt.Sprintf(`<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort>`+
		`<NewProtocol>%s</NewProtocol>`,
		mapping.ExternalPort, strings.ToUpper(mapping.Protocol))
	err := u.soapCall(ctx, "DeletePortMapping", body)
	if err != nil && strings.Contains(err.Error(), "(714)") {
		return nil
	}
	return err
}

// xmlEscape escapes text for hand-built SOAP documents.
func xmlEscape(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(text)
}
