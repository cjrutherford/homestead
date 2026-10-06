package firewall

import (
	"fmt"
	"net/netip"
)

// Connection is the administrator's current management path: who connects
// from where, to which service port. Callers fill it from the live
// connection (S056 wizard) or flags; empty Interface means unknown.
type Connection struct {
	SourceIP  string
	Interface string
	Protocol  string
	Port      int
}

// Validate checks the connection shape.
func (c Connection) Validate() error {
	if _, err := netip.ParseAddr(c.SourceIP); err != nil {
		return fmt.Errorf("connection: invalid source IP: %w", err)
	}
	if c.Interface != "" && (!validIface.MatchString(c.Interface) || c.Interface == "." || c.Interface == "..") {
		return fmt.Errorf("connection: invalid interface %q", c.Interface)
	}
	if c.Protocol != ProtocolTCP && c.Protocol != ProtocolUDP {
		return fmt.Errorf("connection: protocol must be tcp or udp")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("connection: port must be 1-65535")
	}
	return nil
}

// Matches reports whether the rule selects the connection: interface,
// source, and service all match. Zones play no part — coverage is
// mechanical over rendered match fields.
func (r Rule) Matches(conn Connection) bool {
	if len(r.Interfaces) > 0 {
		if conn.Interface == "" {
			return false
		}
		matched := false
		for _, iface := range r.Interfaces {
			if iface == conn.Interface {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if r.Source.CIDR != "" {
		prefix, err := netip.ParsePrefix(r.Source.CIDR)
		if err != nil {
			return false
		}
		addr, err := netip.ParseAddr(conn.SourceIP)
		if err != nil || !prefix.Contains(addr) {
			return false
		}
	}
	if r.Service.Protocol != conn.Protocol {
		return false
	}
	end := r.Service.EndPort
	if end == 0 {
		end = r.Service.Port
	}
	return conn.Port >= r.Service.Port && conn.Port <= end
}

// Guard verifies the desired ruleset keeps the administrator's path
// usable, before anything is applied. Evaluation follows first-match
// order: the first covering rule decides. Our table defaults to accept,
// so silence means reachable — only an explicit earlier drop blocks,
// and only that fails the guard, naming the rule.
func Guard(set RuleSet, conn Connection) error {
	if err := set.Validate(); err != nil {
		return err
	}
	if err := conn.Validate(); err != nil {
		return err
	}
	for _, rule := range set.Sorted() {
		if !rule.Matches(conn) {
			continue
		}
		if rule.Action == ActionDrop {
			return fmt.Errorf("lockout: connection %s %s:%d blocked by rule %q",
				conn.Protocol, conn.SourceIP, conn.Port, rule.Name)
		}
		return nil
	}
	return nil
}

// ApplyGuarded guards the management path, then applies. It changes
// nothing when the guard fails.
func ApplyGuarded(set RuleSet, conn Connection) error {
	if err := Guard(set, conn); err != nil {
		return err
	}
	return ApplySet(set)
}
