// Package autosync throttles the automatic sync and installs the session
// hooks that trigger it.
package autosync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/registry"
)

// DefaultInterval is how often the automatic sync runs when the config does
// not say otherwise.
const DefaultInterval = 24 * time.Hour

// retryAfter is how long a failed automatic sync waits before trying
// again, so a machine without network does not retry on every session.
const retryAfter = time.Hour

// Status records the outcome of the automatic syncs of one location.
type Status struct {
	LastSuccess time.Time `json:"last_success"`
	LastAttempt time.Time `json:"last_attempt"`
	// LastError is the message of the last attempt, empty when it worked.
	LastError string `json:"last_error,omitempty"`
}

func statusPath(loc config.Location) (string, error) {
	root, err := registry.CacheRoot()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(loc.ConfigPath))
	return filepath.Join(root, "autosync", hex.EncodeToString(sum[:8])+".json"), nil
}

// Load reads the status of loc. A missing file is an empty status.
func Load(loc config.Location) (Status, error) {
	var s Status
	path, err := statusPath(loc)
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	// A damaged status file only means the next sync runs early.
	_ = json.Unmarshal(data, &s)
	return s, nil
}

// Due reports whether an automatic sync should run now.
func (s Status) Due(now time.Time, interval time.Duration) bool {
	if now.Sub(s.LastSuccess) < interval {
		return false
	}
	wait := retryAfter
	if interval < wait {
		wait = interval
	}
	return s.LastError == "" || now.Sub(s.LastAttempt) >= wait
}

// InProgress is what LastError holds while an automatic sync is running.
// It keeps other sessions from starting a second one at the same time.
const InProgress = "still running, or interrupted before it finished"

// MarkStarted records that an automatic sync is about to run.
func MarkStarted(loc config.Location, now time.Time) error {
	return write(loc, func(s *Status) {
		s.LastAttempt = now
		s.LastError = InProgress
	})
}

// Record stores the outcome of an automatic sync that just ran.
func Record(loc config.Location, now time.Time, syncErr error) error {
	return write(loc, func(s *Status) {
		s.LastAttempt = now
		s.LastError = ""
		if syncErr != nil {
			s.LastError = syncErr.Error()
		} else {
			s.LastSuccess = now
		}
	})
}

func write(loc config.Location, change func(*Status)) error {
	s, err := Load(loc)
	if err != nil {
		return err
	}
	change(&s)
	path, err := statusPath(loc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
