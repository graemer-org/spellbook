package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/fsutil"
)

// HooksDir is the directory of a registry that holds hooks.
const HooksDir = "hooks"

// HookFile describes a hook that comes as a directory with its scripts.
const HookFile = "hook.json"

// HookEvents are the events both tools fire under the same name. Copilot
// accepts Claude Code's spelling and then also uses its matcher rules and
// its input format, so one hook serves both.
var HookEvents = []string{
	"SessionStart", "SessionEnd", "UserPromptSubmit",
	"PreToolUse", "PostToolUse", "PostToolUseFailure",
	"Stop", "SubagentStop", "PreCompact", "Notification",
}

// Hook is a command a tool runs when something happens in a session.
type Hook struct {
	Name        string   `json:"-"`
	Description string   `json:"description"`
	Event       string   `json:"event"`
	Matcher     string   `json:"matcher"`
	Command     string   `json:"command"`
	Timeout     int      `json:"timeout"`
	Tools       []string `json:"tools"`
	// Dir is the directory with the hook's scripts, or "" for a hook that
	// is a single JSON file.
	Dir  string `json:"-"`
	hash string
}

// Hash identifies the hook including its scripts.
func (h Hook) Hash() string { return h.hash }

// For reports whether the hook is meant for the named tool.
func (h Hook) For(tool string) bool {
	return len(h.Tools) == 0 || slices.Contains(h.Tools, tool)
}

// DiscoverHooks reads root/hooks. A hook is either a file <name>.json or a
// directory <name> with a hook.json and the scripts it runs.
func DiscoverHooks(root string, tools []string) ([]Hook, []string, error) {
	dir := filepath.Join(root, HooksDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var out []Hook
	var warnings []string
	seen := map[string]bool{}
	for _, e := range entries {
		var h Hook
		var file string
		switch {
		case e.IsDir():
			h.Name, h.Dir = e.Name(), filepath.Join(dir, e.Name())
			file = filepath.Join(h.Dir, HookFile)
			if _, err := os.Stat(file); err != nil {
				continue
			}
		case e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json"):
			h.Name = strings.TrimSuffix(e.Name(), ".json")
			file = filepath.Join(dir, e.Name())
		default:
			continue
		}
		rel, _ := filepath.Rel(root, file)
		rel = filepath.ToSlash(rel)
		skip := func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %s", rel, fmt.Sprintf(format, args...)))
		}
		if !config.ValidResourceName(h.Name) {
			skip("%q is not a valid hook name", h.Name)
			continue
		}
		if seen[h.Name] {
			skip("name %q is used twice", h.Name)
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&h); err != nil {
			skip("%v", err)
			continue
		}
		switch {
		case strings.TrimSpace(h.Command) == "":
			skip("a hook needs a \"command\"")
			continue
		case !slices.Contains(HookEvents, h.Event):
			skip("\"event\" must be one of %s", strings.Join(HookEvents, ", "))
			continue
		case h.Timeout < 0:
			skip("\"timeout\" must not be negative")
			continue
		}
		bad := ""
		for _, t := range h.Tools {
			if !slices.Contains(tools, t) {
				bad = t
			}
		}
		if bad != "" {
			skip("\"tools\" names %q, which is not one of %s", bad, strings.Join(tools, ", "))
			continue
		}
		sum := sha256.New()
		sum.Write(data)
		if h.Dir != "" {
			dirHash, err := fsutil.HashDir(h.Dir)
			if err != nil {
				return nil, nil, err
			}
			sum.Write([]byte(dirHash))
		}
		h.hash = hex.EncodeToString(sum.Sum(nil))
		seen[h.Name] = true
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings, nil
}
