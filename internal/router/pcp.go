package router

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// PCP wire constants (RFC 6887): version 2 datagrams on the shared
// mapping port, MAP opcode for create/delete/refresh. ANNOUNCE and
// PEER stay out: Homestead maps inbound ports only, never announces
// presence or pins outbound flows.
const (
	pcpVersion    = 2
	pcpOpMap      = 1
	pcpProtoTCP   = 6
	pcpProtoUDP   = 17
	pcpHeaderSize = 24
	pcpMapSize    = 60
	// defaultPCPLifetime applies when mappings omit lifetimes: one
	// hour, refreshed by callers rather than fossilized.
	defaultPCPLifetime = time.Hour
	pcpAttempts        = 2
	pcpTimeout         = 3 * time.Second
)

// pcpResultNames translates common result codes; anything else reports
// by number so unknown codes still diagnose.
func pcpResult(code byte) error {
	switch code {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("router: PCP unsupported version")
	case 2:
		return fmt.Errorf("router: PCP not authorized")
	case 3:
		return fmt.Errorf("router: PCP malformed request")
	case 4:
		return fmt.Errorf("router: PCP unavailable resources")
	default:
		return fmt.Errorf("router: PCP result code %d", code)
	}
}

// PCP automates NAT mappings through Port Control Protocol against a
// PCP speaker (normally the gateway mapping port). It has no query
// operation — the protocol never learned to list — so QueryMapping
// reports unsupported and callers track what they created.
type PCP struct {
	Server  string
	enabled bool
}

// NewPCP builds the provider for server host:port with recorded owner
// opt-in. Disabled providers refuse automation with ErrNotEnabled.
func NewPCP(server string, enabled bool) (*PCP, error) {
	host, port, err := net.SplitHostPort(server)
	if err != nil || host == "" {
		return nil, fmt.Errorf("router: PCP server must be host:port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, fmt.Errorf("router: PCP server port must be 1-65535")
	}
	return &PCP{Server: server, enabled: enabled}, nil
}

// Name identifies the PCP provider.
func (p *PCP) Name() string { return "pcp" }

// Capabilities reports mapping without query: the protocol cannot list.
func (p *PCP) Capabilities() []Capability { return []Capability{CapabilityMapping} }

// Enabled reports recorded owner opt-in.
func (p *PCP) Enabled() bool { return p.enabled }

// QueryMapping is unsupported: PCP has no list operation.
func (p *PCP) QueryMapping(_ context.Context, _ string, _ int) (Mapping, error) {
	return Mapping{}, ErrUnsupported
}

// pcpNonce mints the 12-byte mapping nonce identifying one operation.
func pcpNonce() ([12]byte, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return [12]byte{}, fmt.Errorf("router: PCP nonce: %w", err)
	}
	return nonce, nil
}

// pcpProto maps the contract protocol to the PCP number.
func pcpProto(protocol string) (byte, error) {
	switch strings.ToLower(protocol) {
	case ProtocolTCP:
		return pcpProtoTCP, nil
	case ProtocolUDP:
		return pcpProtoUDP, nil
	default:
		return 0, fmt.Errorf("router: protocol must be tcp or udp")
	}
}

// mapRequest builds a MAP opcode body: lifetime in seconds (0 deletes),
// zeroed client and external addresses (the server learns both), and
// the suggested external port. Deletion matches on client, protocol,
// and internal port server-side.
func mapRequest(mapping Mapping, lifetime uint32, nonce [12]byte) ([]byte, error) {
	proto, err := pcpProto(mapping.Protocol)
	if err != nil {
		return nil, err
	}
	request := make([]byte, pcpMapSize)
	request[0] = pcpVersion
	request[1] = pcpOpMap
	binary.BigEndian.PutUint32(request[4:8], lifetime)
	copy(request[8:20], make([]byte, 16))
	copy(request[20:32], nonce[:])
	request[32] = proto
	binary.BigEndian.PutUint16(request[36:38], uint16(mapping.InternalPort))
	binary.BigEndian.PutUint16(request[38:40], uint16(mapping.ExternalPort))
	copy(request[40:56], make([]byte, 16))
	return request, nil
}

// mapResponse parses a MAP response against the request nonce,
// returning lifetime granted, assigned external port, and assigned
// external address.
func mapResponse(response []byte, nonce [12]byte) (uint32, uint16, error) {
	if len(response) < 56 {
		return 0, 0, fmt.Errorf("router: PCP short response")
	}
	if response[0] != pcpVersion || response[1] != (0x80|pcpOpMap) {
		return 0, 0, fmt.Errorf("router: PCP not a MAP response")
	}
	if err := pcpResult(response[3]); err != nil {
		return 0, 0, err
	}
	for i := range nonce {
		if response[20+i] != nonce[i] {
			return 0, 0, fmt.Errorf("router: PCP nonce mismatch")
		}
	}
	lifetime := binary.BigEndian.Uint32(response[4:8])
	port := binary.BigEndian.Uint16(response[38:40])
	return lifetime, port, nil
}

// exchange sends one request with a retry, returning the raw response.
func (p *PCP) exchange(ctx context.Context, request []byte) ([]byte, error) {
	dialer := &net.Dialer{Timeout: pcpTimeout}
	conn, err := dialer.DialContext(ctx, "udp", p.Server)
	if err != nil {
		return nil, fmt.Errorf("router: PCP dial: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(pcpTimeout))
	}
	var last error
	for attempt := 0; attempt < pcpAttempts; attempt++ {
		if _, err := conn.Write(request); err != nil {
			last = err
			continue
		}
		response := make([]byte, 1024)
		n, err := conn.Read(response)
		if err != nil {
			last = err
			continue
		}
		return response[:n], nil
	}
	return nil, fmt.Errorf("router: PCP exchange: %w", last)
}

// AddMapping creates or refreshes one mapping. Gateways may assign a
// different external port than suggested; Homestead serves fixed
// ports, so a reassignment fails loudly instead of silently serving
// the wrong port.
func (p *PCP) AddMapping(ctx context.Context, mapping Mapping) error {
	if !p.enabled {
		return ErrNotEnabled
	}
	if err := mapping.Validate(); err != nil {
		return err
	}
	lifetime := uint32(mapping.Lifetime / time.Second)
	if lifetime == 0 {
		lifetime = uint32(defaultPCPLifetime / time.Second)
	}
	nonce, err := pcpNonce()
	if err != nil {
		return err
	}
	request, err := mapRequest(mapping, lifetime, nonce)
	if err != nil {
		return err
	}
	response, err := p.exchange(ctx, request)
	if err != nil {
		return err
	}
	_, assigned, err := mapResponse(response, nonce)
	if err != nil {
		return err
	}
	if int(assigned) != mapping.ExternalPort {
		return fmt.Errorf("router: PCP assigned external port %d, want %d", assigned, mapping.ExternalPort)
	}
	return nil
}

// DeleteMapping removes one mapping with a zero-lifetime MAP. Servers
// match on client, protocol, and internal port; absent mappings report
// success, so deletion converges.
func (p *PCP) DeleteMapping(ctx context.Context, mapping Mapping) error {
	if !p.enabled {
		return ErrNotEnabled
	}
	if err := mapping.Validate(); err != nil {
		return err
	}
	nonce, err := pcpNonce()
	if err != nil {
		return err
	}
	request, err := mapRequest(mapping, 0, nonce)
	if err != nil {
		return err
	}
	response, err := p.exchange(ctx, request)
	if err != nil {
		return err
	}
	_, _, err = mapResponse(response, nonce)
	return err
}
