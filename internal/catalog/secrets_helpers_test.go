package catalog

import (
	"testing"

	"homestead/internal/secret"
)

// memSecrets is an in-memory SecretStore for tests; it also stubs the
// chown of secret files, which tests cannot perform unprivileged.
type memSecrets struct {
	values map[string]string
	sets   int
}

func newMemSecrets(t *testing.T) *memSecrets {
	t.Helper()
	old := chownSecret
	chownSecret = func(string, int, int) error { return nil }
	t.Cleanup(func() { chownSecret = old })
	return &memSecrets{values: map[string]string{}}
}

func (m *memSecrets) Get(name string) (string, error) {
	v, ok := m.values[name]
	if !ok {
		return "", secret.ErrNotFound
	}
	return v, nil
}

func (m *memSecrets) Set(name, value string) error {
	m.values[name] = value
	m.sets++
	return nil
}
