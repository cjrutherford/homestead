package mdns

import "sync"

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
}

var (
	stateMu sync.Mutex
	current State
)

func setState(s State) {
	stateMu.Lock()
	current = s
	stateMu.Unlock()
}

// CurrentState returns the responder's last known state.
func CurrentState() State {
	stateMu.Lock()
	defer stateMu.Unlock()
	return current
}
