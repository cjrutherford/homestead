// Package config serves Homestead's validated configuration (S016).
//
// The Service composes the schema framework (S015: what is valid) with
// desired-state storage (S014: what is stored) behind three operations:
// read the current state, plan a candidate without persisting, and apply a
// candidate atomically. Both homesteadctl and the dashboard reach these
// through the same HTTP routes (ADR-004); the Go API client arrives in S017.
//
// Validation errors are data, not failures: Plan reports them with the
// effective settings so callers can preview, and Apply persists nothing
// when validation fails.
package config

import (
	"errors"

	"homestead/internal/desired"
	"homestead/internal/schema"
)

// Service is the validated configuration over one schema and one file.
type Service struct {
	schema schema.Schema
	path   string
}

// New returns a Service validating against s and persisting at path
// (usually desired.DefaultPath()).
func New(s schema.Schema, path string) *Service {
	return &Service{schema: s, path: path}
}

// Get returns the stored state, or a current-version empty state when no
// file exists yet (fresh appliance, S012 dirs provisioned but unconfigured).
func (s *Service) Get() (desired.State, error) {
	stored, err := desired.Load(s.path)
	if err != nil {
		if errors.Is(err, desired.ErrNotFound) {
			return desired.New(), nil
		}
		return desired.State{}, err
	}
	return stored, nil
}

// Plan merges candidate over schema defaults and validates the effective
// settings without persisting anything.
func (s *Service) Plan(candidate map[string]any) Plan {
	effective := s.schema.Defaults()
	for key, value := range candidate {
		effective[key] = value
	}
	errs := s.schema.Validate(effective)
	return Plan{
		Valid:     len(errs) == 0,
		Effective: effective,
		Errors:    errs,
	}
}

// Apply validates like Plan and, only when valid, atomically persists the
// effective settings. Invalid candidates leave the file untouched.
func (s *Service) Apply(candidate map[string]any) (desired.State, []schema.FieldError, error) {
	plan := s.Plan(candidate)
	if len(plan.Errors) > 0 {
		return desired.State{}, plan.Errors, nil
	}
	state := desired.State{Version: desired.CurrentVersion, Settings: plan.Effective}
	if err := desired.Save(s.path, state); err != nil {
		return desired.State{}, nil, err
	}
	return state, nil, nil
}

// Plan is the result of validating a candidate configuration. JSON tags
// serve CLI and dashboard output (ADR-004).
type Plan struct {
	Valid     bool                `json:"valid"`
	Effective map[string]any      `json:"effective,omitempty"`
	Errors    []schema.FieldError `json:"errors,omitempty"`
}
