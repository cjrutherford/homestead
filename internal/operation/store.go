package operation

import (
	"database/sql"
	"fmt"
	"time"

	"homestead/internal/store"
)

// Store persists operation records. The runner (S061) drives lifecycles
// through here; readers (events S062, audit S063, UI S064) observe them.
type Store struct {
	db *sql.DB
}

// New returns a Store over an open store (migrations provide v5+).
func New(s *store.Store) *Store {
	return &Store{db: s.DB()}
}

// Create records a pending operation of the given type and payload.
func (s *Store) Create(opType, payload string) (Operation, error) {
	if err := ValidateType(opType); err != nil {
		return Operation{}, err
	}
	if err := ValidatePayload(payload); err != nil {
		return Operation{}, err
	}
	now := formatTime(time.Now())
	res, err := s.db.Exec(
		`INSERT INTO operations (type, status, payload, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		opType, StatusPending, payload, now, now,
	)
	if err != nil {
		return Operation{}, fmt.Errorf("operation: create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Operation{}, fmt.Errorf("operation: create: %w", err)
	}
	return s.Get(id)
}

// Get returns the operation with its steps in position order.
func (s *Store) Get(id int64) (Operation, error) {
	var op Operation
	var created, updated string
	err := s.db.QueryRow(
		`SELECT id, type, status, payload, result, error, created_at, updated_at
		 FROM operations WHERE id = ?`, id,
	).Scan(&op.ID, &op.Type, &op.Status, &op.Payload, &op.Result, &op.Error, &created, &updated)
	if err == sql.ErrNoRows {
		return Operation{}, ErrNotFound
	}
	if err != nil {
		return Operation{}, fmt.Errorf("operation: get: %w", err)
	}
	op.CreatedAt, err = parseTime(created)
	if err != nil {
		return Operation{}, fmt.Errorf("operation: get: %w", err)
	}
	op.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return Operation{}, fmt.Errorf("operation: get: %w", err)
	}
	steps, err := s.steps(id)
	if err != nil {
		return Operation{}, err
	}
	op.Steps = steps
	return op, nil
}

// List returns the most recent operations, newest first. Non-positive
// limits default to 100.
func (s *Store) List(limit int) ([]Operation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id FROM operations ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("operation: list: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("operation: list: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("operation: list: %w", err)
	}
	ops := make([]Operation, 0, len(ids))
	for _, id := range ids {
		op, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// Transition moves an operation along a legal edge, stamping result and
// error on terminal states. UpdatedAt always advances.
func (s *Store) Transition(id int64, to, result, errMsg string) error {
	op, err := s.Get(id)
	if err != nil {
		return err
	}
	if !CanTransition(op.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrBadTransition, op.Status, to)
	}
	if _, err := s.db.Exec(
		`UPDATE operations SET status = ?, result = ?, error = ?, updated_at = ? WHERE id = ?`,
		to, result, errMsg, formatTime(time.Now()), id,
	); err != nil {
		return fmt.Errorf("operation: transition: %w", err)
	}
	return nil
}

// AddStep appends a pending step, positioned after existing ones.
func (s *Store) AddStep(operationID int64, name string) (Step, error) {
	if name == "" {
		return Step{}, fmt.Errorf("operation: step name must not be empty")
	}
	if _, err := s.Get(operationID); err != nil {
		return Step{}, err
	}
	var position int
	err := s.db.QueryRow(
		`SELECT COALESCE(MAX(position), -1) + 1 FROM operation_steps WHERE operation_id = ?`,
		operationID,
	).Scan(&position)
	if err != nil {
		return Step{}, fmt.Errorf("operation: add step: %w", err)
	}
	res, err := s.db.Exec(
		`INSERT INTO operation_steps (operation_id, position, name, status) VALUES (?, ?, ?, ?)`,
		operationID, position, name, StatusPending,
	)
	if err != nil {
		return Step{}, fmt.Errorf("operation: add step: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Step{}, fmt.Errorf("operation: add step: %w", err)
	}
	return s.getStep(operationID, id)
}

// TransitionStep moves one step along a legal edge.
func (s *Store) TransitionStep(operationID, stepID int64, to, errMsg string) error {
	step, err := s.getStep(operationID, stepID)
	if err != nil {
		return err
	}
	if !CanTransition(step.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrBadTransition, step.Status, to)
	}
	now := formatTime(time.Now())
	started := ""
	if !step.StartedAt.IsZero() {
		started = formatTime(step.StartedAt)
	}
	finished := ""
	switch to {
	case StatusRunning:
		if started == "" {
			started = now
		}
	case StatusSucceeded, StatusFailed, StatusCancelled:
		finished = now
	}
	if _, err := s.db.Exec(
		`UPDATE operation_steps SET status = ?, error = ?, started_at = ?, finished_at = ?
		 WHERE id = ? AND operation_id = ?`,
		to, errMsg, started, finished, stepID, operationID,
	); err != nil {
		return fmt.Errorf("operation: transition step: %w", err)
	}
	return nil
}

func (s *Store) getStep(operationID, stepID int64) (Step, error) {
	var step Step
	var started, finished string
	err := s.db.QueryRow(
		`SELECT id, operation_id, position, name, status, started_at, finished_at, error
		 FROM operation_steps WHERE id = ? AND operation_id = ?`,
		stepID, operationID,
	).Scan(&step.ID, &step.OperationID, &step.Position, &step.Name, &step.Status, &started, &finished, &step.Error)
	if err == sql.ErrNoRows {
		return Step{}, ErrNotFound
	}
	if err != nil {
		return Step{}, fmt.Errorf("operation: get step: %w", err)
	}
	step.StartedAt, err = parseTime(started)
	if err != nil {
		return Step{}, fmt.Errorf("operation: get step: %w", err)
	}
	step.FinishedAt, err = parseTime(finished)
	if err != nil {
		return Step{}, fmt.Errorf("operation: get step: %w", err)
	}
	return step, nil
}

func (s *Store) steps(operationID int64) ([]Step, error) {
	rows, err := s.db.Query(
		`SELECT id FROM operation_steps WHERE operation_id = ? ORDER BY position`, operationID,
	)
	if err != nil {
		return nil, fmt.Errorf("operation: steps: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("operation: steps: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("operation: steps: %w", err)
	}
	steps := make([]Step, 0, len(ids))
	for _, id := range ids {
		step, err := s.getStep(operationID, id)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}
