package discover

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Production source paths for route and resolver discovery.
const (
	ipv4RoutePath = "/proc/net/route"
	resolvPath    = "/etc/resolv.conf"
)

// Address is one interface address with its network.
type Address struct {
	IP      string
	Network string
}

// Interface is one network interface with its addresses.
type Interface struct {
	Name      string
	Index     int
	MTU       int
	MAC       string
	Up        bool
	Loopback  bool
	Multicast bool
	Addresses []Address
}

// Network is the S042 fact set: interfaces, IPv4 default gateway,
// the interface owning it, and configured resolvers. IPv6 gateway
// discovery belongs to the slices that need it (S131/S222 extend here).
type Network struct {
	Interfaces       []Interface
	DefaultGateway   string
	DefaultInterface string
	Resolvers        []string
}

// DiscoverNetwork collects the network fact set from the local host.
// A missing default route (isolated containers) yields empty gateway
// fields, never an error; only unreadable interfaces fail.
func DiscoverNetwork() (Network, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return Network{}, fmt.Errorf("discover: list interfaces: %w", err)
	}
	out := Network{}
	for _, iface := range ifaces {
		entry := Interface{
			Name:      iface.Name,
			Index:     iface.Index,
			MTU:       iface.MTU,
			MAC:       iface.HardwareAddr.String(),
			Up:        iface.Flags&net.FlagUp != 0,
			Loopback:  iface.Flags&net.FlagLoopback != 0,
			Multicast: iface.Flags&net.FlagMulticast != 0,
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return Network{}, fmt.Errorf("discover: interface %s addresses: %w", iface.Name, err)
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				ones, _ := ipnet.Mask.Size()
				masked := ipnet.IP.Mask(ipnet.Mask)
				entry.Addresses = append(entry.Addresses, Address{
					IP:      ipnet.IP.String(),
					Network: fmt.Sprintf("%s/%d", masked.String(), ones),
				})
			}
		}
		out.Interfaces = append(out.Interfaces, entry)
	}
	sort.Slice(out.Interfaces, func(i, j int) bool {
		return out.Interfaces[i].Index < out.Interfaces[j].Index
	})
	routes, err := os.ReadFile(ipv4RoutePath)
	if err == nil {
		out.DefaultGateway, out.DefaultInterface = parseIPv4Routes(routes)
	}
	resolv, err := os.ReadFile(resolvPath)
	if err == nil {
		out.Resolvers = parseResolvConf(resolv)
	}
	return out, nil
}

// ipv4Route is one parsed /proc/net/route line.
type ipv4Route struct {
	iface   string
	gateway string
	metric  int
}

// parseIPv4Routes returns the lowest-metric default gateway and its
// interface, or empties when no default route exists.
func parseIPv4Routes(data []byte) (gateway, iface string) {
	var best *ipv4Route
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[0] == "Iface" {
			continue
		}
		if fields[1] != "00000000" {
			continue
		}
		ip, err := decodeRouteAddress(fields[2])
		if err != nil {
			continue
		}
		metric, err := strconv.Atoi(fields[6])
		if err != nil {
			continue
		}
		candidate := &ipv4Route{iface: fields[0], gateway: ip, metric: metric}
		if best == nil || candidate.metric < best.metric {
			best = candidate
		}
	}
	if best == nil {
		return "", ""
	}
	return best.gateway, best.iface
}

// decodeRouteAddress converts /proc/net/route hex (little-endian bytes)
// to dotted quad, e.g. 0101A8C0 -> 192.168.1.1.
func decodeRouteAddress(hex string) (string, error) {
	if len(hex) != 8 {
		return "", fmt.Errorf("discover: bad route address %q", hex)
	}
	raw := make([]byte, 4)
	for i := range raw {
		parsed, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return "", fmt.Errorf("discover: bad route address %q: %w", hex, err)
		}
		raw[3-i] = byte(parsed)
	}
	addr, ok := netip.AddrFromSlice(raw)
	if !ok {
		return "", fmt.Errorf("discover: bad route address %q", hex)
	}
	return addr.String(), nil
}

// parseResolvConf extracts nameserver IPs, skipping comments and options.
func parseResolvConf(data []byte) []string {
	var resolvers []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "nameserver" {
			continue
		}
		if _, err := netip.ParseAddr(fields[1]); err != nil {
			continue
		}
		resolvers = append(resolvers, fields[1])
	}
	return resolvers
}

// HasInterface reports whether a named interface exists.
func (n Network) HasInterface(name string) bool {
	for _, iface := range n.Interfaces {
		if iface.Name == name {
			return true
		}
	}
	return false
}
