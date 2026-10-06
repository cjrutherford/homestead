// Package operation owns long-running operation records (S060).
//
// An Operation is a named unit of work (firewall.apply, catalog.install)
// moving through a strict state machine, composed of ordered steps with
// the same machine. Results and errors are JSON/string payloads for later
// consumers (event stream S062, audit S063, progress UI S064). Execution
// belongs to the runner (S061); this package defines, validates,
// transitions, and persists records.
package operation

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Statuses, shared by operations and steps.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

var validType = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

// Sentinel domain failures.
var (
	// ErrNotFound reports unknown operation or step ids.
	ErrNotFound = errors.New("operation not found")
	// ErrBadTransition reports an illegal state-machine edge.
	ErrBadTransition = errors.New("illegal status transition")
)

// Operation is one unit of tracked work.
type Operation struct {
	ID        int64
	Type      string
	Status    string
	Payload   string
	Result    string
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Steps     []Step
}

// Step is one ordered unit within an operation.
type Step struct {
	ID          int64
	OperationID int64
	Position    int
	Name        string
	Status      string
	StartedAt   time.Time
	FinishedAt  time.Time
	Error       string
}

// Terminal reports succeeded, failed, or cancelled.
func Terminal(status string) bool {
	return status == StatusSucceeded || status == StatusFailed || status == StatusCancelled
}

// CanTransition reports a legal state-machine edge: pending starts or
// cancels, running finishes or cancels, terminal states never move.
func CanTransition(from, to string) bool {
	switch from {
	case StatusPending:
		return to == StatusRunning || to == StatusCancelled
	case StatusRunning:
		return to == StatusSucceeded || to == StatusFailed || to == StatusCancelled
	default:
		return false
	}
}

// ValidateType checks operation type names (dotted lowercase namespaces).
func ValidateType(opType string) error {
	if !validType.MatchString(opType) {
		return fmt.Errorf("operation: invalid type %q", opType)
	}
	return nil
}

// ValidatePayload checks optional JSON input.
func ValidatePayload(payload string) error {
	if payload == "" {
		return nil
	}
	if !json.Valid([]byte(payload)) {
		return fmt.Errorf("operation: invalid payload JSON")
	}
	return nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}
