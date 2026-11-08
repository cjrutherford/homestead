// Package dockerpools keeps Docker's default-address-pools roomy enough for
// a full catalog (S463). Docker's built-in pool hands every compose network
// a /16 or /20 and runs dry at about 31 networks ("could not find an
// available, non-overlapping IPv4 address pool"); the catalog has 46 apps.
// The installer merges one small-subnet pool into /etc/docker/daemon.json,
// never touching other keys and never replacing pools the owner set.
package dockerpools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
)

// DefaultPath is Docker's daemon configuration file.
const DefaultPath = "/etc/docker/daemon.json"

// Key is the daemon.json key this package manages.
const Key = "default-address-pools"

// Size is the prefix length of each network handed out (/24, 254 hosts).
const Size = 24

// MinNetworks is the capacity Doctor wants: the 46-app catalog plus Authentik,
// the edge, and room for builds and the owner's own compose projects.
const MinNetworks = 64

// Candidates are tried in order; the first that overlaps no host network
// wins. All are private /16s that avoid the usual LAN ranges
// (192.168.x, 10.0.x/10.1.x, 172.16/12) and the overlay's 100.64/10.
var Candidates = []string{"10.200.0.0/16", "10.201.0.0/16", "10.202.0.0/16", "10.203.0.0/16"}

// Pool is one default-address-pools entry.
type Pool struct {
	Base string `json:"base"`
	Size int    `json:"size"`
}

// Capacity returns how many networks the configured pools can hand out, and
// whether the key is present at all.
func Capacity(config []byte) (networks int, present bool, err error) {
	var doc map[string]json.RawMessage
	if len(bytes.TrimSpace(config)) > 0 {
		if err := json.Unmarshal(config, &doc); err != nil {
			return 0, false, fmt.Errorf("daemon.json is not valid JSON: %w", err)
		}
	}
	raw, ok := doc[Key]
	if !ok {
		return 0, false, nil
	}
	var pools []Pool
	if err := json.Unmarshal(raw, &pools); err != nil {
		return 0, true, fmt.Errorf("%s is not a list of base/size pools: %w", Key, err)
	}
	for _, p := range pools {
		prefix, err := netip.ParsePrefix(p.Base)
		if err != nil || p.Size < prefix.Bits() || p.Size > 32 || p.Size-prefix.Bits() > 20 {
			continue
		}
		networks += 1 << (p.Size - prefix.Bits())
	}
	return networks, true, nil
}

// HostNetworks lists the networks of this machine's interfaces.
func HostNetworks() []netip.Prefix {
	var out []netip.Prefix
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if p, err := netip.ParsePrefix(ipnet.String()); err == nil {
				out = append(out, p.Masked())
			}
		}
	}
	return out
}

// Choose returns the first candidate base overlapping none of host.
func Choose(host []netip.Prefix) (string, error) {
	for _, c := range Candidates {
		cp := netip.MustParsePrefix(c)
		clash := false
		for _, h := range host {
			if h.Overlaps(cp) {
				clash = true
				break
			}
		}
		if !clash {
			return c, nil
		}
	}
	return "", fmt.Errorf("every candidate pool overlaps a host network; set %s in daemon.json by hand", Key)
}

// Merge returns config with the pool added. A config that already has the
// key is returned untouched (the owner's pools win). Every other key keeps
// its value. changed reports whether the bytes differ.
func Merge(config []byte, base string) (out []byte, changed bool, err error) {
	doc := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(config)) > 0 {
		if err := json.Unmarshal(config, &doc); err != nil {
			return nil, false, fmt.Errorf("daemon.json is not valid JSON, left alone: %w", err)
		}
	}
	if _, ok := doc[Key]; ok {
		return config, false, nil
	}
	pools, _ := json.Marshal([]Pool{{Base: base, Size: Size}})
	doc[Key] = pools
	out, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}
