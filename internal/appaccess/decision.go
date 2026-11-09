// Package appaccess builds explicit, reviewable app access decisions.
package appaccess

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Role string

const (
	None  Role = "none"
	User  Role = "user"
	Admin Role = "admin"
)

// Person describes a managed person; the appliance owner is not a person.
type Person struct {
	ID     int64 `json:"id"`
	Active bool  `json:"active"`
}

type Choice struct {
	PersonID int64 `json:"person_id"`
	Role     Role  `json:"role"`
}

// Plan binds every choice to the app, managed people and role capability.
// It contains no credentials. Runtime readiness must also be checked by
// the caller before accepting or applying an installation.
type Plan struct {
	App            string   `json:"app"`
	People         []Person `json:"people"`
	Choices        []Choice `json:"choices"`
	AdminSupported bool     `json:"admin_supported"`
	Integration    string   `json:"integration"`
	Digest         string   `json:"digest"`
}

// Build expects a non-secret fingerprint of the verified app and identity
// integration from the caller. A digest is not an authorization decision.
func Build(app string, people []Person, choices []Choice, adminSupported bool, integration string) (Plan, error) {
	if app == "" || strings.TrimSpace(app) != app {
		return Plan{}, fmt.Errorf("app access: invalid application")
	}
	if integration == "" || strings.TrimSpace(integration) != integration {
		return Plan{}, fmt.Errorf("app access: integration fingerprint required")
	}
	states := make(map[int64]bool, len(people))
	for _, p := range people {
		if p.ID <= 0 {
			return Plan{}, fmt.Errorf("app access: invalid person ID")
		}
		if _, exists := states[p.ID]; exists {
			return Plan{}, fmt.Errorf("app access: duplicate person %d", p.ID)
		}
		states[p.ID] = p.Active
	}
	seen := make(map[int64]bool, len(choices))
	for _, c := range choices {
		active, exists := states[c.PersonID]
		if !exists || seen[c.PersonID] {
			return Plan{}, fmt.Errorf("app access: unknown or duplicate person %d", c.PersonID)
		}
		seen[c.PersonID] = true
		if c.Role != None && c.Role != User && c.Role != Admin {
			return Plan{}, fmt.Errorf("app access: invalid role for person %d", c.PersonID)
		}
		if !active && c.Role != None {
			return Plan{}, fmt.Errorf("app access: disabled person %d cannot be granted access", c.PersonID)
		}
		if c.Role == Admin && !adminSupported {
			return Plan{}, fmt.Errorf("app access: app administrator role is unavailable")
		}
	}
	if len(seen) != len(states) {
		return Plan{}, fmt.Errorf("app access: select access for every person")
	}
	p := Plan{App: app, People: append([]Person{}, people...), Choices: append([]Choice{}, choices...), AdminSupported: adminSupported, Integration: integration}
	sort.Slice(p.People, func(i, j int) bool { return p.People[i].ID < p.People[j].ID })
	sort.Slice(p.Choices, func(i, j int) bool { return p.Choices[i].PersonID < p.Choices[j].PersonID })
	data, err := json.Marshal(p)
	if err != nil {
		return Plan{}, fmt.Errorf("app access: encode plan: %w", err)
	}
	sum := sha256.Sum256(data)
	p.Digest = hex.EncodeToString(sum[:])
	return p, nil
}

// Verify rebuilds from current authoritative inputs before comparing an
// earlier digest. Callers must separately check capability and step-up.
func Verify(digest, app string, people []Person, choices []Choice, adminSupported bool, integration string) error {
	p, err := Build(app, people, choices, adminSupported, integration)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(digest), []byte(p.Digest)) != 1 {
		return fmt.Errorf("app access: decision changed; review again")
	}
	return nil
}
