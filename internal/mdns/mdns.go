// Package mdns answers first-run local discovery (S221): the
// unclaimed appliance resolves on the LAN with no public
// prerequisites — no DNS server, no directory, no cloud. The daemon
// serves <hostname>.local A records plus a _homestead._tcp service
// advertisement carrying claim state, so owners find the box and see
// whether it still awaits them. Since S295 (ADR-039) it also answers
// <label>.local for each app route, from a live list.
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
	Addresses func() []LinkAddr
	// Apps lists the app labels also answered as <label>.local
	// (ADR-039). Optional; read per response, so names follow installs.
	Apps func() []string
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

// names lists every A-record name the responder owns: the hostname
// first, then each valid app label, without duplicates.
func (c Config) names() []string {
	out := []string{c.host()}
	if c.Apps == nil {
		return out
	}
	seen := map[string]bool{c.Hostname: true}
	for _, label := range c.Apps() {
		label = strings.ToLower(label)
		if seen[label] || !validHostnameLabel.MatchString(label) {
			continue
		}
		seen[label] = true
		out = append(out, label+".local")
	}
	return out
}

// owns reports whether name is one of the responder's A-record names.
func (c Config) owns(name string) bool {
	for _, n := range c.names() {
		if n == name {
			return true
		}
	}
	return false
}

// instance names the service instance:
// <hostname>._homestead._tcp.local.
func (c Config) instance() string {
	return c.Hostname + "." + ServiceName
}

// LinkAddr is one advertisable address with the subnet and interface
// it lives on, so answers can be limited to the link a query arrived
// from (RFC 6762 section 6.2).
type LinkAddr struct {
	Addr      netip.Addr
	Prefix    netip.Prefix
	Interface string
}

// dockerBridge reports container-managed bridge interfaces: never the
// LAN, so never advertised.
func dockerBridge(name string) bool {
	return name == "docker0" || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth")
}

// LANIPv4 selects advertisable addresses from discovery facts: up,
// non-loopback, non-Docker-bridge interfaces, IPv4, neither loopback
// nor link-local. Sorted for deterministic answers.
func LANIPv4(network discover.Network) []LinkAddr {
	var out []LinkAddr
	for _, iface := range network.Interfaces {
		if !iface.Up || iface.Loopback || dockerBridge(iface.Name) {
			continue
		}
		for _, address := range iface.Addresses {
			ip, err := netip.ParseAddr(address.IP)
			if err != nil || !ip.Is4() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			prefix, _ := netip.ParsePrefix(address.Network)
			out = append(out, LinkAddr{Addr: ip, Prefix: prefix, Interface: iface.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.String() < out[j].Addr.String() })
	return out
}

// forSource picks the addresses on the link src queried from. An
// unknown source, or one off every connected subnet, falls back to
// all addresses so discovery still works.
func forSource(addrs []LinkAddr, src netip.Addr) []netip.Addr {
	var matched, all []netip.Addr
	for _, a := range addrs {
		all = append(all, a.Addr)
		if src.IsValid() && a.Prefix.IsValid() && a.Prefix.Contains(src) {
			matched = append(matched, a.Addr)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return all
}

// forInterface picks the addresses configured on one interface.
func forInterface(addrs []LinkAddr, name string) []netip.Addr {
	var out []netip.Addr
	for _, a := range addrs {
		if a.Interface == name {
			out = append(out, a.Addr)
		}
	}
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

// glue appends A records for name to buf, reporting how many.
func glue(buf []byte, name string, addrs []netip.Addr) ([]byte, int) {
	count := 0
	for _, addr := range addrs {
		ip := addr.As4()
		buf = encodeRR(buf, name, typeA, true, responseTTL, ip[:])
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
		case (q.Type == typeA || q.Type == typeANY) && cfg.owns(q.Name):
			var count int
			answers, count = glue(answers, q.Name, addrs)
			answerCount += count
		case (q.Type == typePTR || q.Type == typeANY) && q.Name == ServiceName:
			ptr := encodeName(nil, cfg.instance())
			answers = encodeRR(answers, ServiceName, typePTR, false, responseTTL, ptr)
			answerCount++
			var count int
			additionals, count = glue(additionals, cfg.host(), addrs)
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
			additionals, count = glue(additionals, cfg.host(), addrs)
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
// Addresses are limited to src's link; a zero src means all.
func respond(msg []byte, cfg Config, src netip.Addr) ([]byte, bool) {
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
	addrs := forSource(cfg.Addresses(), src)
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
	glued := 0
	for _, name := range cfg.names() {
		var count int
		additionals, count = glue(additionals, name, addrs)
		glued += count
	}
	out := header(0, 3, glued)
	out = append(out, answers...)
	return append(out, additionals...)
}

// announceNames builds an unsolicited A-record advertisement for names
// that appeared since the last one (an app was installed), or nil.
func announceNames(names []string, addrs []netip.Addr) []byte {
	if len(names) == 0 || len(addrs) == 0 {
		return nil
	}
	var answers []byte
	count := 0
	for _, name := range names {
		var n int
		answers, n = glue(answers, name, addrs)
		count += n
	}
	return append(header(0, count, 0), answers...)
}

// answeredNames lists the names of A records in a response's answer,
// authority and additional sections, so the responder can notice another
// device claiming one of its names. Malformed messages yield what parsed.
func answeredNames(msg []byte) []string {
	if len(msg) < 12 || msg[2]&0x80 == 0 {
		return nil
	}
	qd := int(msg[4])<<8 | int(msg[5])
	rr := (int(msg[6])<<8 | int(msg[7])) + (int(msg[8])<<8 | int(msg[9])) + (int(msg[10])<<8 | int(msg[11]))
	offset := 12
	for i := 0; i < qd; i++ {
		_, next, ok := readName(msg, offset)
		if !ok || next+4 > len(msg) {
			return nil
		}
		offset = next + 4
	}
	var out []string
	for i := 0; i < rr; i++ {
		name, next, ok := readName(msg, offset)
		if !ok || next+10 > len(msg) {
			return out
		}
		rrtype := uint16(msg[next])<<8 | uint16(msg[next+1])
		length := int(msg[next+8])<<8 | int(msg[next+9])
		if next+10+length > len(msg) {
			return out
		}
		if rrtype == typeA {
			out = append(out, name)
		}
		offset = next + 10 + length
	}
	return out
}

// readName decodes a possibly compressed name at offset and returns the
// offset after it where it started. Pointer jumps are bounded so hostile
// loops end.
func readName(msg []byte, offset int) (string, int, bool) {
	var labels []string
	end := -1
	for jumps := 0; jumps < 16; {
		if offset >= len(msg) {
			return "", 0, false
		}
		length := int(msg[offset])
		switch {
		case length == 0:
			if end < 0 {
				end = offset + 1
			}
			return strings.Join(labels, "."), end, true
		case length&0xc0 == 0xc0:
			if offset+1 >= len(msg) {
				return "", 0, false
			}
			if end < 0 {
				end = offset + 2
			}
			offset = (length&0x3f)<<8 | int(msg[offset+1])
			jumps++
		case length > 63 || offset+1+length > len(msg):
			return "", 0, false
		default:
			labels = append(labels, strings.ToLower(string(msg[offset+1:offset+1+length])))
			offset += 1 + length
		}
	}
	return "", 0, false
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
		if iface.Flags&net.FlagLoopback != 0 || dockerBridge(iface.Name) {
			continue
		}
		out = append(out, iface)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("mdns: no multicast interfaces")
	}
	return out, nil
}
