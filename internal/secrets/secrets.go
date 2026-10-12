// Package secrets stores passwords outside the connections file.
package secrets

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

const service = "myrdp"

// Store keeps secrets by key. Get returns "" for a key that isn't stored.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Key builds the keyring key for a node's secret field.
func Key(nodeID, field string) string { return nodeID + "/" + field }

// Keyring is the OS keyring: Windows Credential Manager, or the Secret
// Service on Linux.
type Keyring struct{}

func (Keyring) Get(key string) (string, error) {
	v, err := keyring.Get(service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return v, err
}

func (Keyring) Set(key, value string) error { return keyring.Set(service, key, value) }

func (Keyring) Delete(key string) error {
	err := keyring.Delete(service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is an in-process store for tests.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (s *Memory) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key], nil
}

func (s *Memory) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func (s *Memory) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}
