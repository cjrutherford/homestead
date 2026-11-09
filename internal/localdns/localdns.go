// Package localdns is the optional appliance resolver (S295, ADR-039): it
// answers DNS for the appliance's own `.lan` names on its LAN addresses,
// port 53, over UDP and TCP, so the owner can add one conditional forward
// for `lan` on the router instead of a hosts entry per app.
//
// It is deliberately not a general resolver: known names get A records,
// other `.lan` names get NXDOMAIN, and every other query is REFUSED. It
// forwards nothing, caches nothing, and serves no other record types
// (a known name asked for AAAA gets an empty answer).
package localdns

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Port is the DNS port the resolver listens on.
const Port = 53

// Suffix is the only zone the resolver answers for.
const Suffix = "lan"

// answerTTL seconds: short, so a moved or removed app is forgotten soon.
const answerTTL = 60

// Link is one LAN address and the subnet it serves, so answers can carry
// the address on the asker's subnet.
type Link struct {
	Addr   netip.Addr
	Prefix netip.Prefix
}

// Config drives the resolver. Names returns the lower-case names it owns
// (homestead.lan, immich.lan); it is read per query, so installs and
// removals take effect without a restart.
type Config struct {
	Names func() []string
	Links []Link
}

// Answer builds the response to one query message, or an error for bytes
// that are not a parseable query (the caller stays silent).
func Answer(query []byte, names []string, links []Link, src netip.Addr) ([]byte, error) {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil, err
	}
	if h.Response {
		return nil, errors.New("localdns: not a query")
	}
	questions, err := p.AllQuestions()
	if err != nil {
		return nil, err
	}
	resp := dnsmessage.Header{ID: h.ID, Response: true, OpCode: h.OpCode, RecursionDesired: h.RecursionDesired}
	reply := func(rcode dnsmessage.RCode, authoritative bool, q []dnsmessage.Question, answer func(*dnsmessage.Builder) error) ([]byte, error) {
		resp.RCode, resp.Authoritative = rcode, authoritative
		b := dnsmessage.NewBuilder(nil, resp)
		b.EnableCompression()
		if err := b.StartQuestions(); err != nil {
			return nil, err
		}
		for _, one := range q {
			if err := b.Question(one); err != nil {
				return nil, err
			}
		}
		if answer != nil {
			if err := b.StartAnswers(); err != nil {
				return nil, err
			}
			if err := answer(&b); err != nil {
				return nil, err
			}
		}
		return b.Finish()
	}
	if h.OpCode != 0 {
		return reply(dnsmessage.RCodeNotImplemented, false, questions, nil)
	}
	if len(questions) != 1 {
		return reply(dnsmessage.RCodeFormatError, false, questions, nil)
	}
	q := questions[0]
	name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
	if q.Class != dnsmessage.ClassINET || (name != Suffix && !strings.HasSuffix(name, "."+Suffix)) {
		return reply(dnsmessage.RCodeRefused, false, questions, nil)
	}
	owned := false
	for _, n := range names {
		if n == name {
			owned = true
			break
		}
	}
	if !owned {
		return reply(dnsmessage.RCodeNameError, true, questions, nil)
	}
	if q.Type != dnsmessage.TypeA && q.Type != dnsmessage.TypeALL {
		return reply(dnsmessage.RCodeSuccess, true, questions, nil) // no data
	}
	addrs := forSource(links, src)
	return reply(dnsmessage.RCodeSuccess, true, questions, func(b *dnsmessage.Builder) error {
		for _, a := range addrs {
			rh := dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: answerTTL}
			if err := b.AResource(rh, dnsmessage.AResource{A: a.As4()}); err != nil {
				return err
			}
		}
		return nil
	})
}

// forSource picks the addresses on the asker's subnet, or all of them
// when the asker is on none (a routed client).
func forSource(links []Link, src netip.Addr) []netip.Addr {
	var matched, all []netip.Addr
	for _, l := range links {
		if !l.Addr.Is4() {
			continue
		}
		all = append(all, l.Addr)
		if src.IsValid() && l.Prefix.IsValid() && l.Prefix.Contains(src.Unmap()) {
			matched = append(matched, l.Addr)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return all
}

// Server is a running resolver; Close stops every listener.
type Server struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closer []io.Closer
	// Listening lists the addresses bound, as host:port.
	Listening []string
}

// Start binds UDP and TCP port 53 on each link address. Binding is all
// or nothing: a taken port on any address closes the rest and fails, so
// the resolver never half-answers.
func Start(cfg Config) (*Server, error) {
	return start(cfg, Port)
}

func start(cfg Config, port int) (*Server, error) {
	if cfg.Names == nil {
		return nil, errors.New("localdns: names reader must not be nil")
	}
	if len(cfg.Links) == 0 {
		return nil, errors.New("localdns: no LAN address to listen on")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cancel: cancel}
	fail := func(err error) (*Server, error) {
		s.Close()
		return nil, err
	}
	for _, l := range cfg.Links {
		addr := netip.AddrPortFrom(l.Addr, uint16(port)).String()
		pc, err := net.ListenPacket("udp4", addr)
		if err != nil {
			return fail(fmt.Errorf("localdns: listen udp %s: %w", addr, err))
		}
		s.closer = append(s.closer, pc)
		ln, err := net.Listen("tcp4", addr)
		if err != nil {
			return fail(fmt.Errorf("localdns: listen tcp %s: %w", addr, err))
		}
		s.closer = append(s.closer, ln)
		s.Listening = append(s.Listening, addr)
		s.wg.Add(2)
		go s.serveUDP(pc, cfg)
		go s.serveTCP(ctx, ln, cfg)
	}
	return s, nil
}

// Close stops the listeners and waits for them.
func (s *Server) Close() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	for _, c := range s.closer {
		_ = c.Close()
	}
	s.wg.Wait()
}

func (s *Server) serveUDP(pc net.PacketConn, cfg Config) {
	defer s.wg.Done()
	buf := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		src := netip.Addr{}
		if ua, ok := from.(*net.UDPAddr); ok {
			src, _ = netip.AddrFromSlice(ua.IP)
		}
		resp, err := Answer(buf[:n], cfg.Names(), cfg.Links, src)
		if err != nil {
			continue
		}
		_, _ = pc.WriteTo(resp, from)
	}
}

func (s *Server) serveTCP(ctx context.Context, ln net.Listener, cfg Config) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			defer stop()
			src := netip.Addr{}
			if ta, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
				src, _ = netip.AddrFromSlice(ta.IP)
			}
			// A few queries per connection, each length-prefixed.
			for range 8 {
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				var size [2]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				msg := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(conn, msg); err != nil {
					return
				}
				resp, err := Answer(msg, cfg.Names(), cfg.Links, src)
				if err != nil {
					return
				}
				out := binary.BigEndian.AppendUint16(nil, uint16(len(resp)))
				if _, err := conn.Write(append(out, resp...)); err != nil {
					return
				}
			}
		}()
	}
}
