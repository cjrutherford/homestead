package mdns

import (
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// DefaultHostname is the label the daemon publishes: <label>.local.
const DefaultHostname = "homestead"

// State is what the doctor reports about the mDNS responder (S289,
// ADR-035): a taken port fails loudly at startup, and the .local name is
// then unavailable until the conflict is cleared.
type State struct {
	// Known is false until the responder has tried to start.
	Known   bool
	Running bool
	Err     string
	// Conflicts lists names another device answered recently, as
	// "<name> (<address>)" (ADR-039).
	Conflicts []string
}

// conflictWindow is how long a seen conflict is reported.
const conflictWindow = 15 * time.Minute

type conflict struct {
	src  netip.Addr
	seen time.Time
}

var conflicts = map[string]conflict{}

var (
	stateMu sync.Mutex
	current State
)

func setState(s State) {
	stateMu.Lock()
	current = s
	stateMu.Unlock()
}

// CurrentState returns the responder's last known state, with the
// conflicts seen within the window.
func CurrentState() State {
	stateMu.Lock()
	defer stateMu.Unlock()
	out := current
	out.Conflicts = nil
	for name, c := range conflicts {
		if time.Since(c.seen) < conflictWindow {
			out.Conflicts = append(out.Conflicts, fmt.Sprintf("%s (%s)", name, c.src))
		}
	}
	sort.Strings(out.Conflicts)
	return out
}

func noteConflict(name string, src netip.Addr, at time.Time) {
	stateMu.Lock()
	conflicts[name] = conflict{src: src, seen: at}
	stateMu.Unlock()
}

// forgetConflicts drops conflicts for names the responder no longer owns.
func forgetConflicts(owned map[string]bool) {
	stateMu.Lock()
	for name := range conflicts {
		if !owned[name] {
			delete(conflicts, name)
		}
	}
	stateMu.Unlock()
}
