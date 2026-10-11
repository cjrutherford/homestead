package secret

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"homestead/internal/operation"
)

// ErrRekeyInProgress reports a second re-key while one owns the store.
var ErrRekeyInProgress = errors.New("secret: re-key already in progress")

// OperationType is the operation type of a re-key run.
const OperationType = "secret.rekey"

// Re-key step names, in order; they are also the operation step names the
// dashboard and CLI show as progress.
const (
	StepWriteKey   = "write-key"
	StepReencrypt  = "reencrypt"
	StepVerify     = "verify"
	StepRetireOld  = "retire-old"
	pointKeyWrite  = "before-key-write"
	pointMidTx     = "mid-transaction"
	pointAfterTx   = "after-commit"
	pointOldDelete = "before-old-delete"
)

// KeyVersion reports the current (highest) key version.
func (s *Store) KeyVersion() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// RekeyInProgress reports whether a re-key owns the store.
func (s *Store) RekeyInProgress() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rekeying
}

func (s *Store) fire(point string) error {
	if s.hook == nil {
		return nil
	}
	return s.hook(point)
}

// rekeyRun is the state shared by the four re-key steps.
type rekeyRun struct {
	s      *Store
	from   int
	target int
}

// Rekey runs the whole re-key synchronously (ADR-021): used by tests and
// anything not needing step progress. It returns the new key version.
func (s *Store) Rekey() (int, error) {
	run := &rekeyRun{s: s}
	for _, step := range run.steps() {
		if err := step.Run(context.Background()); err != nil {
			return 0, err
		}
	}
	return run.target, nil
}

// RekeyDefinition returns the operation that re-keys the store, step by
// step. Failure at any step leaves the store readable and the operation
// rerunnable. Errors and step names never carry key material.
func (s *Store) RekeyDefinition() operation.Definition {
	run := &rekeyRun{s: s}
	return operation.Definition{Type: OperationType, Steps: run.steps()}
}

// steps wires the four steps; the first claims the store and any failure
// or the last step releases it.
func (r *rekeyRun) steps() []operation.StepDef {
	defs := []struct {
		name string
		fn   func() error
	}{
		{StepWriteKey, r.writeKey},
		{StepReencrypt, r.reencrypt},
		{StepVerify, r.verify},
		{StepRetireOld, r.retireOld},
	}
	out := make([]operation.StepDef, 0, len(defs))
	for i, d := range defs {
		i, d := i, d
		out = append(out, operation.StepDef{Name: d.name, Run: func(ctx context.Context) error {
			if i == 0 {
				if err := r.claim(); err != nil {
					return err
				}
			}
			err := ctx.Err()
			if err == nil {
				err = d.fn()
			}
			if err != nil || i == len(defs)-1 {
				r.release()
			}
			return err
		}})
	}
	return out
}

func (r *rekeyRun) claim() error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if r.s.rekeying {
		return ErrRekeyInProgress
	}
	r.s.rekeying = true
	return nil
}

func (r *rekeyRun) release() {
	r.s.mu.Lock()
	r.s.rekeying = false
	r.s.mu.Unlock()
}

// writeKey makes the target key durable before the database is touched.
// A key left by an earlier interrupted run (rows still older than the
// newest key) is reused instead of minting another.
func (r *rekeyRun) writeKey() error {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fire(pointKeyWrite); err != nil {
		return err
	}
	var oldest int
	if err := s.db.QueryRow(`SELECT COALESCE(MIN(` + versionExpr + `), 0) FROM secrets`).Scan(&oldest); err != nil {
		return fmt.Errorf("secret: re-key: %w", err)
	}
	r.from = s.current
	if oldest != 0 && oldest < s.current {
		r.target = s.current
		return nil
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return fmt.Errorf("secret: generate key: %w", err)
	}
	target := s.current + 1
	if err := writeKeyFile(s.keyFile(target), key); err != nil {
		return err
	}
	s.keys[target] = key
	s.current = target
	r.target = target
	return nil
}

type rowRef struct {
	name      string
	nonce, ct []byte
	version   int
}

// reencrypt rewrites every row under the target key in one transaction.
func (r *rekeyRun) reencrypt() error {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT name, nonce, ciphertext, ` + versionExpr + ` FROM secrets`)
	if err != nil {
		return fmt.Errorf("secret: re-key: %w", err)
	}
	var all []rowRef
	for rows.Next() {
		var ref rowRef
		if err := rows.Scan(&ref.name, &ref.nonce, &ref.ct, &ref.version); err != nil {
			rows.Close()
			return fmt.Errorf("secret: re-key: %w", err)
		}
		all = append(all, ref)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("secret: re-key: %w", err)
	}
	rows.Close()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("secret: re-key: %w", err)
	}
	defer tx.Rollback() // no-op after Commit
	fired := false
	for _, ref := range all {
		if ref.version == r.target {
			continue
		}
		plain, err := s.open(ref.version, ref.nonce, ref.ct)
		if err != nil {
			return fmt.Errorf("secret: re-key: row %q: %w", ref.name, err)
		}
		nonce, ct, err := s.seal(r.target, plain)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE secrets SET nonce = ?, ciphertext = ?, key_version = ? WHERE name = ?`,
			nonce, ct, r.target, ref.name,
		); err != nil {
			return fmt.Errorf("secret: re-key: row %q: %w", ref.name, err)
		}
		if !fired {
			fired = true
			if err := s.fire(pointMidTx); err != nil {
				return err
			}
		}
	}
	if !fired {
		if err := s.fire(pointMidTx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("secret: re-key commit: %w", err)
	}
	return s.fire(pointAfterTx)
}

// verify re-reads every row and decrypts it with the new key.
func (r *rekeyRun) verify() error {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return r.s.verifyAllLocked(r.target)
}

// retireOld deletes the older key files, only after commit and verify.
func (r *rekeyRun) retireOld() error {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fire(pointOldDelete); err != nil {
		return err
	}
	return s.retireOlderLocked(r.target)
}
