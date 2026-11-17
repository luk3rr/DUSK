// Package state reads what older dusk versions saved between runs: each
// monitor's original RGB gains (ADR-5). The controller no longer writes it;
// only `dusk reset-ddc` reads it, to put those gains back.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Version is the file format version.
const Version = 1

// RGB holds a monitor's red, green and blue gains.
type RGB struct {
	R int `json:"r"`
	G int `json:"g"`
	B int `json:"b"`
}

// State is the content of state.json.
type State struct {
	Version   int            `json:"version"`
	Originals map[string]RGB `json:"originals"`
}

// New returns an empty state.
func New() State { return State{Version: Version, Originals: map[string]RGB{}} }

// Store reads and writes the state at Path.
type Store struct{ Path string }

// DefaultPath returns $XDG_STATE_HOME/dusk/state.json (or ~/.local/state/...).
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "dusk", "state.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "dusk", "state.json"), nil
}

// Load reads the state. A missing file yields an empty state. A corrupt file
// or an unknown version yields an empty state together with the error.
func (s Store) Load() (State, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return New(), err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return New(), fmt.Errorf("%s is corrupt: %w", s.Path, err)
	}
	if st.Version != Version {
		return New(), fmt.Errorf("%s: unsupported version %d", s.Path, st.Version)
	}
	if st.Originals == nil {
		st.Originals = map[string]RGB{}
	}
	return st, nil
}

// Save writes the state atomically (temp file + rename, mode 0600).
func (s Store) Save(st State) error {
	st.Version = Version
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".state-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}
