package autosync

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/ojson"
)

// Cursor keeps all hooks of a scope in one hooks.json, which the user may
// edit as well, so only the sessionStart entries that run Spellbook are
// added or removed. Cursor does not wait for a sessionStart hook.

func enableCursor(path string, loc config.Location) error {
	return editCursorHooks(path, func(entries []json.RawMessage) ([]json.RawMessage, error) {
		ours, err := json.Marshal(map[string]any{"command": command(loc), "timeout": 120})
		if err != nil {
			return nil, err
		}
		return append(withoutCursorOurs(entries), ours), nil
	})
}

func disableCursor(path string) error {
	return editCursorHooks(path, func(entries []json.RawMessage) ([]json.RawMessage, error) {
		return withoutCursorOurs(entries), nil
	})
}

func withoutCursorOurs(entries []json.RawMessage) []json.RawMessage {
	var kept []json.RawMessage
	for _, raw := range entries {
		var entry struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(raw, &entry) == nil && strings.HasPrefix(entry.Command, marker) {
			continue
		}
		kept = append(kept, raw)
	}
	return kept
}

// editCursorHooks changes the sessionStart hooks in a Cursor hooks file.
// Everything else, and the order of the keys, stays as it is.
func editCursorHooks(path string, edit func([]json.RawMessage) ([]json.RawMessage, error)) error {
	const event = "sessionStart"
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := ojson.Parse(data)
	if err != nil {
		return fmt.Errorf("%s is not valid JSON, so it was left alone: %w", path, err)
	}
	hooks := &ojson.Object{}
	if raw, ok := file.Get("hooks"); ok {
		if hooks, err = ojson.Parse(raw); err != nil {
			return fmt.Errorf("%s: \"hooks\" is not an object, so the file was left alone", path)
		}
	}
	var entries []json.RawMessage
	if raw, ok := hooks.Get(event); ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("%s: \"hooks.%s\" is not a list, so the file was left alone", path, event)
		}
	}
	if entries, err = edit(entries); err != nil {
		return err
	}
	if len(entries) == 0 {
		hooks.Remove(event)
	} else {
		raw, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		hooks.Set(event, raw)
	}
	if hooks.Len() == 0 {
		file.Remove("hooks")
		if _, ok := file.Get("version"); ok && file.Len() == 1 {
			// Only the version is left, and that says nothing on its own.
			return os.Remove(path)
		}
	} else {
		if _, ok := file.Get("version"); !ok {
			fresh := &ojson.Object{}
			fresh.Set("version", json.RawMessage("1"))
			for _, k := range file.Keys() {
				v, _ := file.Get(k)
				fresh.Set(k, v)
			}
			file = fresh
		}
		file.Set("hooks", hooks.Marshal())
	}
	out, err := file.Indented()
	if err != nil {
		return err
	}
	return writeFile(path, out)
}
