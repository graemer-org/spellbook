package autosync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
)

// marker identifies a hook command as Spellbook's.
const marker = "spellbook sync --auto"

func command(loc config.Location) string {
	if loc.Scope == config.ScopeProject {
		return marker + " --project"
	}
	return marker
}

// HookFile returns the file that holds the session hook of a target.
func HookFile(target string, loc config.Location) (string, error) {
	project := loc.Scope == config.ScopeProject
	switch target {
	case "claude":
		return filepath.Join(loc.Root, ".claude", "settings.json"), nil
	case "copilot":
		if project {
			return filepath.Join(loc.Root, ".github", "hooks", "spellbook.json"), nil
		}
		return filepath.Join(loc.Root, ".copilot", "hooks", "spellbook.json"), nil
	}
	return "", fmt.Errorf("unknown target %q", target)
}

// Installed reports whether the session hook of a target is in place.
func Installed(target string, loc config.Location) (bool, error) {
	path, err := HookFile(target, loc)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Contains(data, []byte(marker)), nil
}

// Enable installs the session hook of a target. It is safe to call twice.
func Enable(target string, loc config.Location) error {
	path, err := HookFile(target, loc)
	if err != nil {
		return err
	}
	switch target {
	case "copilot":
		// Copilot reads every JSON file in its hooks directory, so the hook
		// gets a file of its own. The command goes to the background
		// because Copilot waits for session hooks.
		content := map[string]any{
			"version": 1,
			"hooks": map[string]any{
				"sessionStart": []any{map[string]any{
					"type":       "command",
					"bash":       command(loc) + " >/dev/null 2>&1 &",
					"powershell": command(loc),
					"timeoutSec": 120,
				}},
			},
		}
		data, err := json.MarshalIndent(content, "", "  ")
		if err != nil {
			return err
		}
		return writeFile(path, append(data, '\n'))
	default:
		return editClaudeSettings(path, func(entries []json.RawMessage) ([]json.RawMessage, error) {
			entries = withoutOurs(entries)
			ours, err := json.Marshal(map[string]any{
				"matcher": "startup",
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": command(loc),
					// Run beside the session instead of delaying its start.
					"async": true,
				}},
			})
			if err != nil {
				return nil, err
			}
			return append(entries, ours), nil
		})
	}
}

// Disable removes the session hook of a target, leaving other hooks alone.
func Disable(target string, loc config.Location) error {
	path, err := HookFile(target, loc)
	if err != nil {
		return err
	}
	if on, err := Installed(target, loc); err != nil || !on {
		return err
	}
	if target == "copilot" {
		return os.Remove(path)
	}
	return editClaudeSettings(path, func(entries []json.RawMessage) ([]json.RawMessage, error) {
		return withoutOurs(entries), nil
	})
}

// withoutOurs drops the SessionStart entries that run Spellbook.
func withoutOurs(entries []json.RawMessage) []json.RawMessage {
	var kept []json.RawMessage
	for _, raw := range entries {
		var entry struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		ours := false
		if json.Unmarshal(raw, &entry) == nil {
			for _, h := range entry.Hooks {
				if strings.HasPrefix(h.Command, marker) {
					ours = true
				}
			}
		}
		if !ours {
			kept = append(kept, raw)
		}
	}
	return kept
}

// editClaudeSettings changes the SessionStart hooks in a Claude Code
// settings file. Every other setting, and the order of the keys, stays as
// it is.
func editClaudeSettings(path string, edit func([]json.RawMessage) ([]json.RawMessage, error)) error {
	settings := &object{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if settings, err = parseObject(data); err != nil {
			return fmt.Errorf("%s is not valid JSON, so it was left alone: %w", path, err)
		}
	}
	hooks := &object{}
	if raw, ok := settings.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return fmt.Errorf("%s: \"hooks\" is not an object, so the file was left alone", path)
		}
	}
	var entries []json.RawMessage
	if raw, ok := hooks.get("SessionStart"); ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("%s: \"hooks.SessionStart\" is not a list, so the file was left alone", path)
		}
	}
	entries, err = edit(entries)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		hooks.remove("SessionStart")
	} else {
		raw, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		hooks.set("SessionStart", raw)
	}
	if len(hooks.keys) == 0 {
		settings.remove("hooks")
	} else {
		settings.set("hooks", hooks.marshal())
	}
	var out bytes.Buffer
	if err := json.Indent(&out, settings.marshal(), "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	return writeFile(path, out.Bytes())
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".spellbook-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// object is a JSON object that remembers the order of its keys.
type object struct {
	keys   []string
	values map[string]json.RawMessage
}

func parseObject(data []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object")
	}
	o := &object{values: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected a key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.set(key, raw)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *object) get(key string) (json.RawMessage, bool) {
	raw, ok := o.values[key]
	return raw, ok
}

func (o *object) set(key string, raw json.RawMessage) {
	if o.values == nil {
		o.values = map[string]json.RawMessage{}
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = raw
}

func (o *object) remove(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *object) marshal() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		b.Write(key)
		b.WriteByte(':')
		b.Write(o.values[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}
