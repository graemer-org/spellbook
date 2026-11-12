// Package state records which files Spellbook wrote, so a sync only ever
// changes or removes what it owns.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const version = 1

// Entry is one installed resource.
type Entry struct {
	// Path is relative to the location root (slash-separated) when the
	// resource lies inside it, and absolute otherwise. A relative path keeps
	// a committed project state valid on every machine.
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Target   string `json:"target"`
	Registry string `json:"registry"`
	Commit   string `json:"commit"`
	// Hash is the content hash of what was written.
	Hash string `json:"hash"`
}

// State is the content of a state.json file.
type State struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Load reads the state file. A missing file is an empty state.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{Version: version}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.Version != version {
		return nil, fmt.Errorf("%s has version %d, this spellbook understands version %d", path, s.Version, version)
	}
	return &s, nil
}

// Save writes the state file with entries in a stable order.
func (s *State) Save(path string) error {
	s.Version = version
	sort.Slice(s.Entries, func(i, j int) bool { return s.Entries[i].Path < s.Entries[j].Path })
	if s.Entries == nil {
		s.Entries = []Entry{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ByPath indexes the entries by their path.
func (s *State) ByPath() map[string]Entry {
	m := make(map[string]Entry, len(s.Entries))
	for _, e := range s.Entries {
		m[e.Path] = e
	}
	return m
}
