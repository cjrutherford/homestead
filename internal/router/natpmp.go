package router

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// NAT-PMP wire constants (RFC 6886): version-0 datagrams on the
// shared mapping port, one opcode per mapping family plus the address
// request S131 already probes with. Lifetime 0 deletes; the protocol
// never learned to list, so QueryMapping stays unsupported here.
const (
	natpmpVersion    = 0
	natpmpOpUDPMap   = 1
	natpmpOpTCPMap   = 2
	natpmpRequestLen = 12
	natpmpReplyLen   = 16
	// defaultMappingLifetime applies when mappings omit lifetimes: one
	// hour, refreshed by callers rather than fossilized.
	defaultMappingLifetime = time.Hour
	natpmpAttempts         = 2
	natpmpTimeout          = 3 * time.Second
)

// natpmpResult translates result codes; unknown codes report by
// number so new speakers still diagnose.
func natpmpResult(code uint16) error {
	switch code {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("router: NAT-PMP unsupported version")
	case 2:
		return fmt.Errorf("router: NAT-PMP refused (not authorized or network failure)")
	case 3:
		return fmt.Errorf("router: NAT-PMP network failure")
	case 4:
		return fmt.Errorf("router: NAT-PMP out of resources")
	case 5:
		return fmt.Errorf("router: NAT-PMP unsupported opcode")
	default:
		return fmt.Errorf("router: NAT-PMP result code %d", code)
	}
}

// NATPMP automates NAT mappings through NAT Port Mapping Protocol
// against a NAT-PMP speaker (normally the gateway mapping port,
// disambiguated from PCP roommates by version in S131). It has no
// query operation, so QueryMapping reports unsupported and callers
// track what they created.
type NATPMP struct {
	Server  string
	enabled bool
}

// NewNATPMP builds the provider for server host:port with recorded
// owner opt-in. Disabled providers refuse automation with
// ErrNotEnabled.
func NewNATPMP(server string, enabled bool) (*NATPMP, error) {
	host, port, err := net.SplitHostPort(server)
	if err != nil || host == "" {
		return nil, fmt.Errorf("router: NAT-PMP server must be host:port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, fmt.Errorf("router: NAT-PMP server port must be 1-65535")
	}
	return &NATPMP{Server: server, enabled: enabled}, nil
}

// Name identifies the NAT-PMP provider.
func (p *NATPMP) Name() string { return "nat-pmp" }

// Capabilities reports mapping without query: the protocol cannot list.
func (p *NATPMP) Capabilities() []Capability { return []Capability{CapabilityMapping} }

// Enabled reports recorded owner opt-in.
func (p *NATPMP) Enabled() bool { return p.enabled }

// QueryMapping is unsupported: NAT-PMP has no list operation.
func (p *NATPMP) QueryMapping(_ context.Context, _ string, _ int) (Mapping, error) {
	return Mapping{}, ErrUnsupported
}

// natpmpOpcode maps the contract protocol to the family opcode.
func natpmpOpcode(protocol string) (byte, error) {
	switch strings.ToLower(protocol) {
	case ProtocolTCP:
		return natpmpOpTCPMap, nil
	case ProtocolUDP:
		return natpmpOpUDPMap, nil
	default:
		return 0, fmt.Errorf("router: protocol must be tcp or udp")
	}
}

// natpmpMapRequest builds a family request: suggested external port with a
// lifetime in seconds (0 deletes). Internal port rides along because
// NAT-PMP keys mappings on it server-side.
func natpmpMapRequest(mapping Mapping, lifetime uint32) ([]byte, error) {
	opcode, err := natpmpOpcode(mapping.Protocol)
	if err != nil {
		return nil, err
	}
	request := make([]byte, natpmpRequestLen)
	request[0] = natpmpVersion
	request[1] = opcode
	binary.BigEndian.PutUint16(request[4:6], uint16(mapping.InternalPort))
	binary.BigEndian.PutUint16(request[6:8], uint16(mapping.ExternalPort))
	binary.BigEndian.PutUint32(request[8:12], lifetime)
	return request, nil
}

// natpmpMapReply parses a family reply against the requested opcode,
// returning the assigned external port. Version, length, and opcode
// mismatches refuse before result codes diagnose.
func natpmpMapReply(reply []byte, opcode byte) (uint16, error) {
	if len(reply) < natpmpReplyLen {
		return 0, fmt.Errorf("router: NAT-PMP short reply")
	}
	if reply[0] != natpmpVersion || reply[1] != opcode+128 {
		return 0, fmt.Errorf("router: NAT-PMP not a mapping reply")
	}
	if err := natpmpResult(binary.BigEndian.Uint16(reply[2:4])); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(reply[10:12]), nil
}

// exchange sends one request with a retry, returning the raw reply.
func (p *NATPMP) exchange(ctx context.Context, request []byte) ([]byte, error) {
	dialer := &net.Dialer{Timeout: natpmpTimeout}
	conn, err := dialer.DialContext(ctx, "udp", p.Server)
	if err != nil {
		return nil, fmt.Errorf("router: NAT-PMP dial: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(natpmpTimeout))
	}
	var last error
	for attempt := 0; attempt < natpmpAttempts; attempt++ {
		if _, err := conn.Write(request); err != nil {
			last = err
			continue
		}
		reply := make([]byte, 1024)
		n, err := conn.Read(reply)
		if err != nil {
			last = err
			continue
		}
		return reply[:n], nil
	}
	return nil, fmt.Errorf("router: NAT-PMP exchange: %w", last)
}

// AddMapping creates or refreshes one mapping. Gateways may assign a
// different external port than suggested; Homestead serves fixed
// ports, so a reassignment fails loudly instead of silently serving
// the wrong port.
func (p *NATPMP) AddMapping(ctx context.Context, mapping Mapping) error {
	if !p.enabled {
		return ErrNotEnabled
	}
	if err := mapping.Validate(); err != nil {
		return err
	}
	lifetime := uint32(mapping.Lifetime / time.Second)
	if lifetime == 0 {
		lifetime = uint32(defaultMappingLifetime / time.Second)
	}
	opcode, err := natpmpOpcode(mapping.Protocol)
	if err != nil {
		return err
	}
	request, err := natpmpMapRequest(mapping, lifetime)
	if err != nil {
		return err
	}
	reply, err := p.exchange(ctx, request)
	if err != nil {
		return err
	}
	assigned, err := natpmpMapReply(reply, opcode)
	if err != nil {
		return err
	}
	if int(assigned) != mapping.ExternalPort {
		return fmt.Errorf("router: NAT-PMP assigned external port %d, want %d", assigned, mapping.ExternalPort)
	}
	return nil
}

// DeleteMapping removes one mapping with a zero-lifetime request.
// Absent mappings report success on most speakers, so deletion
// converges; anything else errors.
func (p *NATPMP) DeleteMapping(ctx context.Context, mapping Mapping) error {
	if !p.enabled {
		return ErrNotEnabled
	}
	if err := mapping.Validate(); err != nil {
		return err
	}
	opcode, err := natpmpOpcode(mapping.Protocol)
	if err != nil {
		return err
	}
	request, err := natpmpMapRequest(mapping, 0)
	if err != nil {
		return err
	}
	reply, err := p.exchange(ctx, request)
	if err != nil {
		return err
	}
	_, err = natpmpMapReply(reply, opcode)
	return err
}
