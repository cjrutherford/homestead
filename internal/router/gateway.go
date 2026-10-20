package router

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mapping protocols this unit detects. Detection is lightweight
// presence probing; each protocol's real handshake (S135-S137)
// disambiguates versions and capabilities its own way.
type MappingProtocol string

const (
	ProtocolUPnP   MappingProtocol = "upnp"
	ProtocolNATPMP MappingProtocol = "nat-pmp"
	ProtocolPCP    MappingProtocol = "pcp"
)

// NAT-PMP/PCP share UDP 5351: the version byte of any response tells
// them apart (0 for NAT-PMP, 2 for PCP). A response to the NAT-PMP
// public-address request additionally carries the external address,
// which later slices reuse for instructions and verification.
const mappingPort = 5351

// ssdpTarget is the multicast discovery origin, overridden in tests
// where multicast never routes.
var ssdpTarget = "239.255.255.250:1900"

// Gateway is the likely NAT upstream: the default route's next hop on
// its interface, from S042 facts. Absent default routes mean no
// gateway to identify, which is an error rather than a guess.
type Gateway struct {
	Address   netip.Addr
	Interface string
}

// gatewayFrom extracts the gateway from discovered network facts.
func gatewayFrom(networkInterface, defaultGateway string) (Gateway, error) {
	addr, err := netip.ParseAddr(defaultGateway)
	if err != nil || addr.IsUnspecified() || addr.IsLoopback() {
		return Gateway{}, fmt.Errorf("router: no usable default gateway")
	}
	if networkInterface == "" {
		return Gateway{}, fmt.Errorf("router: default gateway has no interface")
	}
	return Gateway{Address: addr, Interface: networkInterface}, nil
}

// UPnPInfo describes one InternetGatewayDevice answering SSDP: where
// its description lives, what it calls itself, and the control URL
// WAN connection actions post to (S137 drives it).
type UPnPInfo struct {
	Location     string
	FriendlyName string
	Model        string
	ControlURL   string
}

// GatewayInfo is the discovery outcome: the likely gateway, the
// external address when a mapping protocol disclosed it, the UPnP
// device when one answered, and the supported protocol set. Absent
// protocols simply stay out — unsupported is data, never failure.
type GatewayInfo struct {
	Gateway    Gateway
	ExternalIP netip.Addr
	UPnP       *UPnPInfo
	Protocols  []MappingProtocol
}

// has reports protocol support.
func (g GatewayInfo) has(protocol MappingProtocol) bool {
	for _, supported := range g.Protocols {
		if supported == protocol {
			return true
		}
	}
	return false
}

// ProbePMP unicasts a NAT-PMP public-address request at addr
// (host:port, normally the gateway mapping port) and reports the
// responder version with the disclosed external address. Versions
// distinguish the speaker: 0 is NAT-PMP, 2 is PCP sharing the port.
// Silence is not failure here — callers treat errors as unsupported.
func ProbePMP(ctx context.Context, addr string) (byte, netip.Addr, error) {
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return 0, netip.Addr{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	}
	if _, err := conn.Write([]byte{0, 0}); err != nil {
		return 0, netip.Addr{}, err
	}
	response := make([]byte, 12)
	n, err := conn.Read(response)
	if err != nil {
		return 0, netip.Addr{}, err
	}
	if n < 12 || response[1] != 128 {
		return 0, netip.Addr{}, fmt.Errorf("router: not a mapping response")
	}
	external, _ := netip.AddrFromSlice(response[8:12])
	return response[0], external, nil
}

// upnpDevice walks description documents recursively: gateway
// services nest under embedded devices, and the first WAN connection
// service wins.
type upnpDevice struct {
	FriendlyName string        `xml:"friendlyName"`
	ModelName    string        `xml:"modelName"`
	Services     []upnpService `xml:"serviceList>service"`
	Devices      []upnpDevice  `xml:"deviceList>device"`
}

type upnpService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

// findConnection returns the first WAN connection control URL.
func (d *upnpDevice) findConnection() (string, bool) {
	for _, service := range d.Services {
		if strings.Contains(service.ServiceType, "WANIPConnection") ||
			strings.Contains(service.ServiceType, "WANPPPConnection") {
			return service.ControlURL, true
		}
	}
	for i := range d.Devices {
		if control, ok := d.Devices[i].findConnection(); ok {
			return control, true
		}
	}
	return "", false
}

// DiscoverUPnP multicasts for InternetGatewayDevices, fetches each
// distinct description, and returns the first with a WAN connection
// service. Nothing answering means no UPnP, reported as nil without
// error.
func DiscoverUPnP(ctx context.Context) (*UPnPInfo, error) {
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return nil, fmt.Errorf("router: ssdp socket: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	}
	target, err := net.ResolveUDPAddr("udp", ssdpTarget)
	if err != nil {
		return nil, fmt.Errorf("router: ssdp target: %w", err)
	}
	request := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ns=01; ns=01\"\r\nMX: 2\r\n" +
		"ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n\r\n"
	if _, err := conn.WriteTo([]byte(request), target); err != nil {
		return nil, fmt.Errorf("router: ssdp search: %w", err)
	}
	seen := map[string]bool{}
	buffer := make([]byte, 8192)
	for {
		n, _, err := conn.ReadFrom(buffer)
		if err != nil {
			break
		}
		location, ok := ssdpLocation(buffer[:n])
		if !ok || seen[location] {
			continue
		}
		seen[location] = true
		if info, err := fetchUPnP(ctx, location); err == nil {
			return info, nil
		}
	}
	return nil, nil
}

// ssdpLocation extracts the LOCATION header from an SSDP response.
func ssdpLocation(response []byte) (string, bool) {
	for _, line := range strings.Split(string(response), "\n") {
		if index := strings.Index(line, ":"); index > 0 {
			if strings.EqualFold(strings.TrimSpace(line[:index]), "location") {
				location := strings.TrimSpace(line[index+1:])
				if location != "" {
					return location, true
				}
			}
		}
	}
	return "", false
}

// fetchUPnP reads one description document into device info, resolving
// relative control URLs against the description origin.
func fetchUPnP(ctx context.Context, location string) (*UPnPInfo, error) {
	origin, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("router: description status %s", resp.Status)
	}
	var root struct {
		Device upnpDevice `xml:"device"`
	}
	if err := xml.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	control, ok := root.Device.findConnection()
	if !ok {
		return nil, fmt.Errorf("router: no WAN connection service")
	}
	if ref, err := url.Parse(control); err == nil && !ref.IsAbs() {
		control = origin.ResolveReference(ref).String()
	}
	return &UPnPInfo{
		Location:     location,
		FriendlyName: strings.TrimSpace(root.Device.FriendlyName),
		Model:        strings.TrimSpace(root.Device.ModelName),
		ControlURL:   control,
	}, nil
}

// DiscoverGateway identifies the likely gateway from network facts
// and probes mapping support: UPnP by multicast, NAT-PMP/PCP by
// unicast to the mapping port. The whole run respects ctx.
func DiscoverGateway(ctx context.Context, networkInterface, defaultGateway string) (GatewayInfo, error) {
	gateway, err := gatewayFrom(networkInterface, defaultGateway)
	if err != nil {
		return GatewayInfo{}, err
	}
	info := GatewayInfo{Gateway: gateway}
	// The two probes wait on different sockets, so they run together: the
	// read costs the longer timeout, not the sum (S294).
	type upnpResult struct {
		upnp *UPnPInfo
		err  error
	}
	upnpDone := make(chan upnpResult, 1)
	go func() {
		upnp, err := DiscoverUPnP(ctx)
		upnpDone <- upnpResult{upnp, err}
	}()
	version, external, pmpErr := ProbePMP(ctx, gateway.Address.String()+":"+strconv.Itoa(mappingPort))
	if result := <-upnpDone; result.err == nil && result.upnp != nil {
		info.UPnP = result.upnp
		info.Protocols = append(info.Protocols, ProtocolUPnP)
	}
	if err := pmpErr; err == nil {
		switch version {
		case 0:
			info.Protocols = append(info.Protocols, ProtocolNATPMP)
		case 2:
			info.Protocols = append(info.Protocols, ProtocolPCP)
		}
		if external.IsValid() && !external.IsUnspecified() {
			info.ExternalIP = external
		}
	}
	sort.Slice(info.Protocols, func(i, j int) bool { return info.Protocols[i] < info.Protocols[j] })
	return info, nil
}
