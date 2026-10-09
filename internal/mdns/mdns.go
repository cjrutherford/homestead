// Package mdns answers first-run local discovery (S221): the
// unclaimed appliance resolves on the LAN with no public
// prerequisites — no DNS server, no directory, no cloud. The daemon
// serves <hostname>.local A records plus a _homestead._tcp service
// advertisement carrying claim state, so owners find the box and see
// whether it still awaits them.
//
// Scope is deliberately narrow: IPv4 only, respond plus one
// announce, no probing or conflict arbitration. Single-appliance
// scope (ADR-012) puts one Homestead per LAN by design, so name
// fights are out of scope rather than unhandled. A taken mDNS port
// (Avahi and friends own 5353 on desktops) fails loudly at startup
// instead of silently going undiscoverable — the caller decides
// whether discovery is boot-critical.
package mdns

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"homestead/internal/discover"
)

// ServiceName is the advertised service type: _homestead._tcp.local.
const ServiceName = "_homestead._tcp.local"

// responseTTL seconds answers live: short enough to forget moved
// boxes, long enough to keep the LAN quiet.
const responseTTL = 120

// DNS types served.
const (
	typeA    = 1
	typePTR  = 12
	typeTXT  = 16
	typeSRV  = 33
	typeANY  = 255
	typeIN   = 1
	quBit    = 0x8000
	flushBit = 0x8000
)

var validHostnameLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Config drives one responder: the short hostname (homestead), the
// daemon port advertised in SRV, the build version in TXT, a claim
// state reader, and a LAN address reader. Readers run per response,
// so TXT state and A records track the appliance live.
type Config struct {
	Hostname  string
	Port      int
	Version   string
	State     func() string
	Addresses func() []netip.Addr
}

// Validate checks responder shape: an RFC label hostname, a usable
// port, and non-nil readers (nil readers would serve fiction).
func (c Config) Validate() error {
	if !validHostnameLabel.MatchString(c.Hostname) {
		return fmt.Errorf("mdns: hostname must be an RFC 1035 label")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("mdns: port must be 1-65535")
	}
	if c.State == nil {
		return fmt.Errorf("mdns: state reader must not be nil")
	}
	if c.Addresses == nil {
		return fmt.Errorf("mdns: address reader must not be nil")
	}
	return nil
}

// host names the A record: <hostname>.local.
func (c Config) host() string {
	return c.Hostname + ".local"
}

// instance names the service instance:
// <hostname>._homestead._tcp.local.
func (c Config) instance() string {
	return c.Hostname + "." + ServiceName
}

// LANIPv4 selects advertisable addresses from discovery facts: up,
// non-loopback interfaces, IPv4, neither loopback nor link-local.
// Sorted for deterministic answers.
func LANIPv4(network discover.Network) []netip.Addr {
	var out []netip.Addr
	for _, iface := range network.Interfaces {
		if !iface.Up || iface.Loopback {
			continue
		}
		for _, address := range iface.Addresses {
			ip, err := netip.ParseAddr(address.IP)
			if err != nil || !ip.Is4() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ip)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// question is one parsed query question.
type question struct {
	Name string
	Type uint16
	QU   bool
}

// parseQuestions extracts the question section. Responses (QR set)
// and malformed messages refuse: the responder only answers plain
// queries, and guessing at truncated bytes would answer wrong.
func parseQuestions(msg []byte) ([]question, error) {
	if len(msg) < 12 {
		return nil, fmt.Errorf("mdns: message shorter than header")
	}
	if msg[2]&0x80 != 0 {
		return nil, fmt.Errorf("mdns: not a query")
	}
	count := int(msg[4])<<8 | int(msg[5])
	offset := 12
	var out []question
	for i := 0; i < count; i++ {
		name, next, err := parseName(msg, offset)
		if err != nil {
			return nil, err
		}
		if len(msg) < next+4 {
			return nil, fmt.Errorf("mdns: question overruns message")
		}
		qtype := uint16(msg[next])<<8 | uint16(msg[next+1])
		class := uint16(msg[next+2])<<8 | uint16(msg[next+3])
		out = append(out, question{Name: name, Type: qtype, QU: class&quBit != 0})
		offset = next + 4
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("mdns: no questions")
	}
	return out, nil
}

// parseName decodes one uncompressed name at offset. Pointers refuse:
// questions never compress, and following them risks loops on hostile
// bytes. Names compare case-insensitively downstream.
func parseName(msg []byte, offset int) (string, int, error) {
	var labels []string
	for {
		if offset >= len(msg) {
			return "", 0, fmt.Errorf("mdns: name overruns message")
		}
		length := int(msg[offset])
		offset++
		if length == 0 {
			break
		}
		if length&0xc0 != 0 {
			return "", 0, fmt.Errorf("mdns: compressed name in question")
		}
		if length > 63 || offset+length > len(msg) {
			return "", 0, fmt.Errorf("mdns: bad label length")
		}
		labels = append(labels, strings.ToLower(string(msg[offset:offset+length])))
		offset += length
		if len(labels) > 127 {
			return "", 0, fmt.Errorf("mdns: too many labels")
		}
	}
	return strings.Join(labels, "."), offset, nil
}

// encodeName writes one name without compression.
func encodeName(buf []byte, name string) []byte {
	for _, label := range strings.Split(name, ".") {
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	return append(buf, 0)
}

// encodeRR appends one resource record.
func encodeRR(buf []byte, name string, rrtype uint16, flush bool, ttl uint32, rdata []byte) []byte {
	buf = encodeName(buf, name)
	class := uint16(typeIN)
	if flush {
		class |= flushBit
	}
	buf = append(buf, byte(rrtype>>8), byte(rrtype), byte(class>>8), byte(class))
	buf = append(buf, byte(ttl>>24), byte(ttl>>16), byte(ttl>>8), byte(ttl))
	buf = append(buf, byte(len(rdata)>>8), byte(len(rdata)))
	return append(buf, rdata...)
}

// encodeSRVData renders SRV rdata: priority, weight, port, target.
func encodeSRVData(port int, target string) []byte {
	data := []byte{0, 0, 0, 0, byte(port >> 8), byte(port)}
	return encodeName(data, target)
}

// encodeTXTData renders TXT rdata: length-prefixed character strings.
func encodeTXTData(pairs ...string) []byte {
	var data []byte
	for _, pair := range pairs {
		if len(pair) > 255 {
			pair = pair[:255]
		}
		data = append(data, byte(len(pair)))
		data = append(data, pair...)
	}
	return data
}

// header renders a response header for id with answer and additional counts.
func header(id uint16, answers, additionals int) []byte {
	return []byte{
		byte(id >> 8), byte(id), 0x84, 0x00,
		0, 0,
		byte(answers >> 8), byte(answers),
		0, 0,
		byte(additionals >> 8), byte(additionals),
	}
}

// glue appends A records for host to buf, reporting how many.
func glue(buf []byte, cfg Config, addrs []netip.Addr) ([]byte, int) {
	count := 0
	for _, addr := range addrs {
		ip := addr.As4()
		buf = encodeRR(buf, cfg.host(), typeA, true, responseTTL, ip[:])
		count++
	}
	return buf, count
}

// answer builds the response for parsed questions, or nil to stay
// silent: unknown names and types get no answer, never errors —
// responders answer what they own and ignore the rest.
func answer(id uint16, questions []question, cfg Config, addrs []netip.Addr) []byte {
	var answers, additionals []byte
	answerCount, additionalCount := 0, 0
	for _, q := range questions {
		switch {
		case (q.Type == typeA || q.Type == typeANY) && q.Name == cfg.host():
			var count int
			answers, count = glue(answers, cfg, addrs)
			answerCount += count
		case (q.Type == typePTR || q.Type == typeANY) && q.Name == ServiceName:
			ptr := encodeName(nil, cfg.instance())
			answers = encodeRR(answers, ServiceName, typePTR, false, responseTTL, ptr)
			answerCount++
			var count int
			additionals, count = glue(additionals, cfg, addrs)
			additionalCount += count
		case (q.Type == typeSRV || q.Type == typeTXT || q.Type == typeANY) && q.Name == cfg.instance():
			if q.Type == typeSRV || q.Type == typeANY {
				answers = encodeRR(answers, cfg.instance(), typeSRV, true, responseTTL, encodeSRVData(cfg.Port, cfg.host()))
				answerCount++
			}
			if q.Type == typeTXT || q.Type == typeANY {
				txt := encodeTXTData("state="+cfg.State(), "version="+cfg.Version)
				answers = encodeRR(answers, cfg.instance(), typeTXT, true, responseTTL, txt)
				answerCount++
			}
			var count int
			additionals, count = glue(additionals, cfg, addrs)
			additionalCount += count
		}
	}
	if answerCount == 0 {
		return nil
	}
	out := header(id, answerCount, additionalCount)
	out = append(out, answers...)
	return append(out, additionals...)
}

// respond parses one incoming message and builds the response: nil
// means stay silent (unparseable, a response, or nothing we own).
func respond(msg []byte, cfg Config) ([]byte, bool) {
	if len(msg) < 2 {
		return nil, false
	}
	id := uint16(msg[0])<<8 | uint16(msg[1])
	questions, err := parseQuestions(msg)
	if err != nil {
		return nil, false
	}
	unicast := false
	for _, q := range questions {
		if q.QU {
			unicast = true
			break
		}
	}
	addrs := cfg.Addresses()
	if len(addrs) == 0 {
		return nil, false
	}
	resp := answer(id, questions, cfg, addrs)
	if resp == nil {
		return nil, false
	}
	return resp, unicast
}

// announce builds the unsolicited startup advertisement: service
// presence plus instance details and address glue.
func announce(cfg Config, addrs []netip.Addr) []byte {
	if len(addrs) == 0 {
		return nil
	}
	var answers, additionals []byte
	answers = encodeRR(answers, ServiceName, typePTR, false, responseTTL, encodeName(nil, cfg.instance()))
	answers = encodeRR(answers, cfg.instance(), typeSRV, true, responseTTL, encodeSRVData(cfg.Port, cfg.host()))
	answers = encodeRR(answers, cfg.instance(), typeTXT, true, responseTTL, encodeTXTData("state="+cfg.State(), "version="+cfg.Version))
	var count int
	additionals, count = glue(additionals, cfg, addrs)
	_ = count
	out := header(0, 3, len(addrs))
	out = append(out, answers...)
	return append(out, additionals...)
}

// multicastInterfaces lists usable packet interfaces: up with
// multicast, loopback excluded — loopback answers reach nobody on
// the LAN.
func multicastInterfaces() ([]net.Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("mdns: list interfaces: %w", err)
	}
	var out []net.Interface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		out = append(out, iface)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("mdns: no multicast interfaces")
	}
	return out, nil
}
