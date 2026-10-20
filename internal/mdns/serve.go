package mdns

import (
	"context"
	"fmt"
	"net"
	"sync"
)

// mdnsPort is the shared multicast DNS port: taken on desktops by
// Avahi and friends, which is why startup conflicts fail loudly
// instead of silently going undiscoverable.
const mdnsPort = 5353

// mdnsGroup is the IPv4 multicast group for mDNS traffic.
var mdnsGroup = net.ParseIP("224.0.0.251")

// ListenAndServe answers mDNS queries on every usable interface until
// ctx ends: A records for the hostname plus the service advertisement
// with live claim state and addresses. A taken port or missing
// interfaces fail before serving anything — half-discovery (some
// interfaces answering, others silent) would confuse more than help.
func ListenAndServe(ctx context.Context, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		setState(State{Known: true, Err: err.Error()})
		return err
	}
	ifaces, err := multicastInterfaces()
	if err != nil {
		setState(State{Known: true, Err: err.Error()})
		return err
	}
	groupAddr := &net.UDPAddr{IP: mdnsGroup, Port: mdnsPort}
	var conns []*net.UDPConn
	for _, iface := range ifaces {
		conn, err := listenOn(iface, groupAddr)
		if err != nil {
			for _, c := range conns {
				_ = c.Close()
			}
			setState(State{Known: true, Err: err.Error()})
			return err
		}
		conns = append(conns, conn)
	}
	setState(State{Known: true, Running: true})
	serve(ctx, cfg, conns, groupAddr)
	setState(State{Known: true})
	return nil
}

// listenOn joins the mDNS group on one interface. Factored for
// tests, which serve loopback deterministically.
func listenOn(iface net.Interface, groupAddr *net.UDPAddr) (*net.UDPConn, error) {
	conn, err := net.ListenMulticastUDP("udp4", &iface, groupAddr)
	if err != nil {
		return nil, fmt.Errorf("mdns: listen on %s: %w", iface.Name, err)
	}
	return conn, nil
}

// serve announces once per connection, then answers queries on each
// until ctx ends.
func serve(ctx context.Context, cfg Config, conns []*net.UDPConn, groupAddr *net.UDPAddr) {
	// One gratuitous advertisement per connection: newcomers learn us
	// without asking first.
	if advertisement := announce(cfg, cfg.Addresses()); advertisement != nil {
		for _, conn := range conns {
			_, _ = conn.WriteToUDP(advertisement, groupAddr)
		}
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var wg sync.WaitGroup
	for _, conn := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			go func() {
				<-ctx.Done()
				_ = conn.Close()
			}()
			buf := make([]byte, 9000)
			for {
				n, src, err := conn.ReadFromUDP(buf)
				if err != nil {
					return
				}
				resp, unicast := respond(buf[:n], cfg)
				if resp == nil {
					continue
				}
				if unicast {
					_, _ = conn.WriteToUDP(resp, src)
				} else {
					_, _ = conn.WriteToUDP(resp, groupAddr)
				}
			}
		}()
	}
	<-ctx.Done()
	wg.Wait()
}
