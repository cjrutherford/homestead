// Package firewall owns Homestead's host firewall interface (Phase 4).
//
// ADR-008: Homestead manages only its own nftables table and rules, never
// the administrator's policy. This file (S050) observes: binary presence,
// kernel accessibility, existing tables, and the iptables backend flavor.
// Mutation (planning, atomic apply, lockout protection) belongs to later
// slices; no function here changes any ruleset.
package firewall

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// HomesteadTable is the only table Homestead ever manages (S052).
const HomesteadTable = "homestead"

// Table is one nftables table.
type Table struct {
	Family string
	Name   string
}

// State is the S050 detection result: firewall state and compatibility.
// Absence of data is data — a missing binary or an unlistable ruleset is
// reported, never fatal. Detect never mutates anything.
type State struct {
	NFTAvailable      bool
	NFTVersion        string
	RulesetAccessible bool
	RulesetError      string
	Tables            []Table
	HasHomesteadTable bool
	IPTablesAvailable bool
	IPTablesBackend   string
	// ExternalManagers names active third-party firewall managers
	// (ufw, firewalld), best-effort: unprivileged callers may not see
	// them. S057 records; it never manages through them.
	ExternalManagers []string
}

// runCmd and lookPath are vars so tests can stub process execution.
var (
	runCmd = func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				return string(out), fmt.Errorf("%v: %s", err, strings.TrimSpace(string(exit.Stderr)))
			}
			return string(out), err
		}
		return string(out), nil
	}
	lookPath = exec.LookPath
)

// Detect reports the host firewall state. It runs read-only commands
// (`nft --version`, `nft --json list ruleset`, `iptables --version`) and
// nothing else — enforced by test.
func Detect() State {
	var state State
	if _, err := lookPath("nft"); err != nil {
		return state
	}
	state.NFTAvailable = true
	if version, err := runCmd("nft", "--version"); err == nil {
		state.NFTVersion = parseNFTVersion(version)
	}
	ruleset, err := runCmd("nft", "--json", "list", "ruleset")
	switch {
	case err == nil:
		tables, err := parseTables([]byte(ruleset))
		if err != nil {
			state.RulesetError = err.Error()
			break
		}
		state.RulesetAccessible = true
		state.Tables = tables
		for _, table := range tables {
			if table.Name == HomesteadTable {
				state.HasHomesteadTable = true
			}
		}
	case isPermissionError(err):
		// Unprivileged: unlistable, not broken. Continue probing the rest.
	default:
		state.RulesetError = err.Error()
	}
	if _, err := lookPath("iptables"); err == nil {
		state.IPTablesAvailable = true
		if version, err := runCmd("iptables", "--version"); err == nil {
			state.IPTablesBackend = parseIPTablesBackend(version)
		}
	}
	state.ExternalManagers = detectExternalManagers()
	return state
}

// detectExternalManagers lists active third-party managers by asking each
// for its own state. Failures mean "cannot tell", never an error: absence
// of evidence is reported as absence.
func detectExternalManagers() []string {
	var managers []string
	if _, err := lookPath("ufw"); err == nil {
		if out, err := runCmd("ufw", "status"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(strings.TrimSpace(strings.ToLower(line)), "status: active") {
					managers = append(managers, "ufw")
					break
				}
			}
		}
	}
	if _, err := lookPath("firewall-cmd"); err == nil {
		if out, err := runCmd("firewall-cmd", "--state"); err == nil &&
			strings.TrimSpace(strings.ToLower(out)) == "running" {
			managers = append(managers, "firewalld")
		}
	}
	return managers
}

// parseNFTVersion extracts "1.0.9" from "nftables v1.0.9 (Old Doc Yak #3)".
func parseNFTVersion(output string) string {
	fields := strings.Fields(output)
	for i, field := range fields {
		if field == "nftables" && i+1 < len(fields) {
			return strings.TrimPrefix(fields[i+1], "v")
		}
	}
	return strings.TrimSpace(output)
}

// parseIPTablesBackend reports the backend flavor from `iptables --version`.
func parseIPTablesBackend(output string) string {
	lowered := strings.ToLower(output)
	switch {
	case strings.Contains(lowered, "legacy"):
		return "legacy"
	case strings.Contains(lowered, "nf_tables"):
		return "nf_tables"
	default:
		return "unknown"
	}
}

// isPermissionError reports unprivileged denials, which mean "unlistable"
// rather than "broken".
func isPermissionError(err error) bool {
	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "permission denied") ||
		strings.Contains(lowered, "operation not permitted")
}

// nftablesJSON is the envelope of `nft --json list ruleset`.
type nftablesJSON struct {
	Nftables []map[string]json.RawMessage `json:"nftables"`
}

type tableJSON struct {
	Family string `json:"family"`
	Name   string `json:"name"`
}

// parseTables extracts tables, ignoring metainfo and rules.
func parseTables(data []byte) ([]Table, error) {
	var envelope nftablesJSON
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("firewall: parse ruleset: %w", err)
	}
	var tables []Table
	for _, entry := range envelope.Nftables {
		raw, ok := entry["table"]
		if !ok {
			continue
		}
		var table tableJSON
		if err := json.Unmarshal(raw, &table); err != nil {
			return nil, fmt.Errorf("firewall: parse table: %w", err)
		}
		tables = append(tables, Table{Family: table.Family, Name: table.Name})
	}
	return tables, nil
}
