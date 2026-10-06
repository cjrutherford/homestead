package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ErrAdminNotFound reports recovery for an unknown username.
var ErrAdminNotFound = errors.New("administrator not found")

// Recover resets username's password without the old credential. There is
// deliberately no claim-state gate and no session: possession of the
// root-only local socket (S021) is the authorization. The administrator
// must already exist — creation stays in SetupAdmin — and the new password
// faces the same policy.
func (s *Service) Recover(username, password string) (Admin, error) {
	if !validUsername.MatchString(username) {
		return Admin{}, ErrBadUsername
	}
	if len(password) < MinPasswordLength {
		return Admin{}, ErrWeakPassword
	}
	var id int64
	err := s.db.QueryRow(`SELECT id FROM identities WHERE username = ?`, username).Scan(&id)
	if err == sql.ErrNoRows {
		return Admin{}, ErrAdminNotFound
	}
	if err != nil {
		return Admin{}, fmt.Errorf("auth: find admin: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Admin{}, fmt.Errorf("auth: hash password: %w", err)
	}
	if _, err := s.db.Exec(
		`UPDATE identities SET password_hash = ?, updated_at = ? WHERE id = ?`,
		hash, formatTime(time.Now()), id,
	); err != nil {
		return Admin{}, fmt.Errorf("auth: reset password: %w", err)
	}
	return Admin{ID: id, Username: username}, nil
}
