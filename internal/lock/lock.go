// Package lock records the commit each registry was last synced at, so a
// sync can be repeated exactly and upstream changes can be reviewed.
package lock

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

const version = 1

// Entry pins one registry.
type Entry struct {
	Name string `toml:"name"`
	URL  string `toml:"url"`
	// Ref is the configured ref the commit was resolved from.
	Ref    string `toml:"ref,omitempty"`
	Commit string `toml:"commit"`
}

// Lock is the content of a spellbook.lock file.
type Lock struct {
	Version    int     `toml:"version"`
	Registries []Entry `toml:"registry,omitempty"`
}

// Load reads the lock file. A missing file is an empty lock.
func Load(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Lock{Version: version}, nil
	}
	if err != nil {
		return nil, err
	}
	var l Lock
	if _, err := toml.Decode(string(data), &l); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if l.Version != version {
		return nil, fmt.Errorf("%s has version %d, this spellbook understands version %d", path, l.Version, version)
	}
	return &l, nil
}

// Save writes the lock file with registries in a stable order.
func (l *Lock) Save(path string) error {
	l.Version = version
	sort.Slice(l.Registries, func(i, j int) bool { return l.Registries[i].Name < l.Registries[j].Name })
	var buf bytes.Buffer
	buf.WriteString("# Written by spellbook. Do not edit.\n")
	if err := toml.NewEncoder(&buf).Encode(l); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Get returns the entry for the named registry.
func (l *Lock) Get(name string) (Entry, bool) {
	for _, e := range l.Registries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}
