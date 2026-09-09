// Package continuation persists the private native conversation identity owned
// by one Harness Actor.
package continuation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/kagent-dev/kagent/go/harness/internal/utils"
)

const stateVersion = 2

type state struct {
	Version int    `json:"version"`
	Runtime string `json:"runtime"`
	ID      string `json:"session_id,omitempty"`
}

// Validator validates one runtime-specific opaque continuation ID.
type Validator func(string) error

// Store is an atomic, actor-local continuation store.
//
// The persisted file, not the process memory, is the source of truth: an Actor
// resumed from its golden snapshot restores the process image captured before
// any turn ran, while its durable directory carries the state a later turn
// bound. Every Load and Bind therefore re-reads the file first.
type Store struct {
	mu       sync.Mutex
	path     string
	runtime  string
	validate Validator
	data     state
}

// New loads or creates a continuation store for runtime.
func New(durableDir, runtime string, validate Validator) (*Store, error) {
	if err := utils.EnsurePrivateDir(durableDir); err != nil {
		return nil, fmt.Errorf("prepare continuation state directory: %w", err)
	}
	s := &Store{path: filepath.Join(durableDir, "state.json"), runtime: runtime, validate: validate}
	if err := s.refresh(); err != nil {
		return nil, err
	}
	return s, nil
}

// Load returns the continuation currently persisted for the Actor.
func (s *Store) Load() (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refresh(); err != nil {
		return "", false, err
	}
	return s.data.ID, s.data.ID != "", nil
}

// Bind atomically binds the Actor to one continuation identity.
func (s *Store) Bind(id string) error {
	if err := s.validate(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refresh(); err != nil {
		return err
	}
	if s.data.ID != "" && s.data.ID != id {
		return fmt.Errorf("actor is already bound to another %s continuation", s.runtime)
	}
	if s.data.ID == id {
		return nil
	}
	next := state{Version: stateVersion, Runtime: s.runtime, ID: id}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encode continuation state: %w", err)
	}
	if err := utils.ReplacePrivateFile(s.path, b); err != nil {
		return fmt.Errorf("persist continuation state: %w", err)
	}
	s.data = next
	return nil
}

// refresh replaces the cached state with the persisted one. A missing file
// means the Actor has not bound a continuation yet. Callers hold s.mu.
func (s *Store) refresh() error {
	data := state{Version: stateVersion, Runtime: s.runtime}
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		s.data = data
		return nil
	}
	if err != nil {
		return fmt.Errorf("read continuation state: %w", err)
	}
	if err := json.Unmarshal(b, &data); err != nil {
		return fmt.Errorf("decode continuation state: %w", err)
	}
	if data.Version != stateVersion || data.Runtime != s.runtime {
		return fmt.Errorf("unsupported or corrupt %s continuation state", s.runtime)
	}
	if data.ID != "" {
		if err := s.validate(data.ID); err != nil {
			return fmt.Errorf("invalid persisted continuation state: %w", err)
		}
	}
	s.data = data
	return nil
}
