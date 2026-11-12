// Package target knows where each supported AI tool reads its files.
package target

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/graemer-org/spellbook/internal/config"
)

type paths struct {
	homeSkills    string
	projectSkills string
}

// known lists the default skill directories, relative to the home directory
// or to the project root.
var known = map[string]paths{
	"claude":  {homeSkills: ".claude/skills", projectSkills: ".claude/skills"},
	"copilot": {homeSkills: ".copilot/skills", projectSkills: ".github/skills"},
}

// Names returns the supported target names in a stable order.
func Names() []string {
	names := make([]string, 0, len(known))
	for n := range known {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Known reports whether name is a supported target.
func Known(name string) bool {
	_, ok := known[name]
	return ok
}

// SkillsDir returns the absolute directory skills are written to for the
// named target in the given location, honouring a configured override.
func SkillsDir(name string, t config.Target, loc config.Location) (string, error) {
	p, ok := known[name]
	if !ok {
		return "", fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(Names(), ", "))
	}
	dir := p.homeSkills
	if loc.Scope == config.ScopeProject {
		dir = p.projectSkills
	}
	if t.SkillsPath != "" {
		dir = t.SkillsPath
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	dir = filepath.FromSlash(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(loc.Root, dir)
	}
	return filepath.Clean(dir), nil
}

// Place says where an instruction goes for one target.
type Place struct {
	// File is the absolute path that is written.
	File string
	// Block is true when the instruction becomes a managed block inside
	// File, which may also hold the user's own text. Otherwise File belongs
	// to the instruction alone.
	Block bool
}

// InstructionPlace returns where the named instruction is written.
//
// Claude Code loads every Markdown file in its rules directory, so each
// instruction gets a file of its own there and CLAUDE.md is never touched.
// Copilot reads path-scoped instructions from separate files, but always-on
// instructions only from copilot-instructions.md, so those become blocks.
func InstructionPlace(name string, loc config.Location, instruction string, scoped bool) (Place, error) {
	project := loc.Scope == config.ScopeProject
	switch name {
	case "claude":
		return Place{File: filepath.Join(loc.Root, ".claude", "rules", instruction+".md")}, nil
	case "copilot":
		base := filepath.Join(loc.Root, ".copilot")
		if project {
			base = filepath.Join(loc.Root, ".github")
		}
		if scoped {
			return Place{File: filepath.Join(base, "instructions", instruction+".instructions.md")}, nil
		}
		return Place{File: filepath.Join(base, "copilot-instructions.md"), Block: true}, nil
	}
	return Place{}, fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(Names(), ", "))
}

// RenderInstruction returns the content written for an instruction: the
// whole file, or for a block only its body.
func RenderInstruction(name, registry string, paths []string, body string, block bool) []byte {
	if block {
		return []byte(body)
	}
	var b strings.Builder
	if len(paths) > 0 {
		b.WriteString("---\n")
		switch name {
		case "copilot":
			fmt.Fprintf(&b, "applyTo: %q\n", strings.Join(paths, ","))
		default:
			b.WriteString("paths:\n")
			for _, p := range paths {
				fmt.Fprintf(&b, "  - %q\n", p)
			}
		}
		b.WriteString("---\n")
	}
	fmt.Fprintf(&b, "<!-- Installed by spellbook from registry %q. Change it there, not here. -->\n\n", registry)
	b.WriteString(body)
	return []byte(b.String())
}

// AgentFile returns the file an agent is written to.
func AgentFile(name string, loc config.Location, agent string) (string, error) {
	switch name {
	case "claude":
		return filepath.Join(loc.Root, ".claude", "agents", agent+".md"), nil
	case "copilot":
		if loc.Scope == config.ScopeProject {
			return filepath.Join(loc.Root, ".github", "agents", agent+".agent.md"), nil
		}
		return filepath.Join(loc.Root, ".copilot", "agents", agent+".agent.md"), nil
	}
	return "", fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(Names(), ", "))
}

// PromptPlace says where a prompt goes for one target.
type PromptPlace struct {
	// Path is the file, or for AsSkill the skill directory.
	Path string
	// AsSkill is true when the prompt is installed as a skill directory
	// holding a SKILL.md.
	AsSkill bool
	// Supported is false when the target has no place for prompts in this
	// scope.
	Supported bool
}

// PromptFor returns where the named prompt is written.
//
// Claude Code runs a skill as a slash command, so a prompt becomes a skill
// that only the user invokes. Copilot reads prompt files from a project's
// .github/prompts; it has no user-level directory for them.
func PromptFor(name string, t config.Target, loc config.Location, prompt string) (PromptPlace, error) {
	switch name {
	case "claude":
		dir, err := SkillsDir(name, t, loc)
		if err != nil {
			return PromptPlace{}, err
		}
		return PromptPlace{Path: filepath.Join(dir, prompt), AsSkill: true, Supported: true}, nil
	case "copilot":
		if loc.Scope != config.ScopeProject {
			return PromptPlace{}, nil
		}
		return PromptPlace{Path: filepath.Join(loc.Root, ".github", "prompts", prompt+".prompt.md"), Supported: true}, nil
	}
	return PromptPlace{}, fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(Names(), ", "))
}

// RenderDoc returns the file content of an agent or prompt for one target.
//
// The frontmatter is passed on as written, with two changes: a mapping
// under the key "claude" or "copilot" holds fields for that tool only and
// is merged into the top level for it, and the fields in set are added or
// replaced.
func RenderDoc(name string, front *yaml.Node, body string, set [][2]string) ([]byte, error) {
	out := &yaml.Node{Kind: yaml.MappingNode}
	put := func(key, value *yaml.Node) {
		for i := 0; i+1 < len(out.Content); i += 2 {
			if out.Content[i].Value == key.Value {
				out.Content[i+1] = value
				return
			}
		}
		out.Content = append(out.Content, key, value)
	}
	scalar := func(s string) *yaml.Node {
		tag := "!!str"
		if s == "true" || s == "false" {
			// Written as a YAML boolean, not as a quoted string.
			tag = "!!bool"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: s}
	}
	for _, kv := range set {
		put(scalar(kv[0]), scalar(kv[1]))
	}
	var own *yaml.Node
	if front != nil {
		for i := 0; i+1 < len(front.Content); i += 2 {
			key, value := front.Content[i], front.Content[i+1]
			if Known(key.Value) {
				if key.Value == name {
					own = value
				}
				continue
			}
			if !isSet(set, key.Value) {
				put(key, value)
			}
		}
	}
	if own != nil {
		if own.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("the frontmatter key %q must hold 'key: value' lines", name)
		}
		for i := 0; i+1 < len(own.Content); i += 2 {
			if !isSet(set, own.Content[i].Value) {
				put(own.Content[i], own.Content[i+1])
			}
		}
	}
	var b strings.Builder
	if len(out.Content) > 0 {
		data, err := yaml.Marshal(out)
		if err != nil {
			return nil, err
		}
		b.WriteString("---\n")
		b.Write(data)
		b.WriteString("---\n\n")
	}
	b.WriteString(body)
	return []byte(b.String()), nil
}

func isSet(set [][2]string, key string) bool {
	for _, kv := range set {
		if kv[0] == key {
			return true
		}
	}
	return false
}

// MCPPlace says where a target keeps its MCP servers.
type MCPPlace struct {
	// File is the JSON file with the top-level "mcpServers" object.
	File string
	// ViaCLI is true when the file belongs to the tool and must only be
	// changed through the tool's own command line.
	ViaCLI bool
}

// MCPFor returns where MCP servers of a target are stored.
//
// Claude Code keeps user-level servers in ~/.claude.json, a file it
// rewrites itself, so those are changed through 'claude mcp' and only read
// from the file.
func MCPFor(name string, loc config.Location) (MCPPlace, error) {
	project := loc.Scope == config.ScopeProject
	switch name {
	case "claude":
		if project {
			return MCPPlace{File: filepath.Join(loc.Root, ".mcp.json")}, nil
		}
		return MCPPlace{File: filepath.Join(loc.Root, ".claude.json"), ViaCLI: true}, nil
	case "copilot":
		if project {
			return MCPPlace{File: filepath.Join(loc.Root, ".github", "mcp.json")}, nil
		}
		return MCPPlace{File: filepath.Join(loc.Root, ".copilot", "mcp-config.json")}, nil
	}
	return MCPPlace{}, fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(Names(), ", "))
}

// RenderMCP returns the server definition for one target as canonical
// JSON: the fields under the target's own key are merged into the top
// level, and the keys of other targets are dropped. Copilot requires a
// "tools" field, which defaults to all tools.
func RenderMCP(name string, raw []byte) ([]byte, error) {
	var def map[string]any
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, err
	}
	own, _ := def[name].(map[string]any)
	for _, t := range Names() {
		delete(def, t)
	}
	for k, v := range own {
		def[k] = v
	}
	if name == "copilot" {
		if _, ok := def["tools"]; !ok {
			def["tools"] = []string{"*"}
		}
	}
	return json.Marshal(def)
}

// HookPlace says where a target keeps its hooks.
type HookPlace struct {
	// File is a JSON file whose top-level "hooks" object maps an event name
	// to a list of entries.
	File string
	// Owned is true when the file belongs to Spellbook alone. Otherwise it
	// is the tool's settings file and holds much more than hooks.
	Owned bool
}

// HookFor returns where hooks of a target are stored.
func HookFor(name string, loc config.Location) (HookPlace, error) {
	switch name {
	case "claude":
		return HookPlace{File: filepath.Join(loc.Root, ".claude", "settings.json")}, nil
	case "copilot":
		base := filepath.Join(loc.Root, ".copilot")
		if loc.Scope == config.ScopeProject {
			base = filepath.Join(loc.Root, ".github")
		}
		return HookPlace{File: filepath.Join(base, "hooks", "spellbook-hooks.json"), Owned: true}, nil
	}
	return HookPlace{}, fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(Names(), ", "))
}

// HookMarker is put behind a hook's command as a shell comment. It is how
// Spellbook finds its entries among the user's own.
const HookMarker = " # spellbook:"

// HookEntry returns the entry for one hook in the form the target reads,
// as canonical JSON. Both forms use Claude Code's event names, which
// Copilot accepts together with Claude Code's matcher rules.
func HookEntry(name, id, command, matcher string, timeout int) ([]byte, error) {
	command += HookMarker + id
	var entry map[string]any
	switch name {
	case "claude":
		hook := map[string]any{"type": "command", "command": command}
		if timeout > 0 {
			hook["timeout"] = timeout
		}
		entry = map[string]any{"hooks": []any{hook}}
	case "copilot":
		entry = map[string]any{"type": "command", "command": command}
		if timeout > 0 {
			entry["timeoutSec"] = timeout
		}
	default:
		return nil, fmt.Errorf("unknown target %q", name)
	}
	if matcher != "" {
		entry["matcher"] = matcher
	}
	return json.Marshal(entry)
}

// HookID returns the id in the marker of a hook entry of either form, or
// "" when the entry is not one of Spellbook's.
func HookID(entry []byte) string {
	var e struct {
		Command string `json:"command"`
		Bash    string `json:"bash"`
		Hooks   []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(entry, &e) != nil {
		return ""
	}
	commands := []string{e.Command, e.Bash}
	for _, h := range e.Hooks {
		commands = append(commands, h.Command)
	}
	for _, c := range commands {
		if i := strings.LastIndex(c, HookMarker); i >= 0 {
			return strings.TrimSpace(c[i+len(HookMarker):])
		}
	}
	return ""
}

// HookFilesDir returns where the scripts of a hook are kept. In a project
// they live inside the repository so they can be committed.
func HookFilesDir(loc config.Location, hook string) string {
	if loc.Scope == config.ScopeProject {
		return filepath.Join(loc.Root, ".spellbook", "hooks", hook)
	}
	return filepath.Join(filepath.Dir(loc.ConfigPath), "hooks", hook)
}

// HookFilesRef returns how a hook command refers to its scripts. In a
// project the path is relative to the project root, which Claude Code
// names in CLAUDE_PROJECT_DIR and Copilot uses as working directory.
func HookFilesRef(loc config.Location, hook string) string {
	if loc.Scope == config.ScopeProject {
		return "${CLAUDE_PROJECT_DIR:-.}/.spellbook/hooks/" + hook
	}
	return HookFilesDir(loc, hook)
}
