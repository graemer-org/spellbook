// Package target knows where each supported AI tool reads its files and in
// which format.
//
// Every tool is described by one Spec in a file of its own, registered from
// an init function. Adding a tool means adding such a file; the functions
// in this file, and the packages that call them, do not change.
package target

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/graemer-org/spellbook/internal/config"
)

// ErrUnsupported is returned when a tool has no place for a kind of
// resource in the given scope. Callers skip the resource and tell the user.
var ErrUnsupported = errors.New("not supported by this tool at this level")

// Place says where an instruction goes for one target.
type Place struct {
	// File is the absolute path that is written.
	File string
	// Block is true when the instruction becomes a managed block inside
	// File, which may also hold the user's own text. Otherwise File belongs
	// to the instruction alone.
	Block bool
}

// PromptPlace says where a prompt goes for one target.
type PromptPlace struct {
	// Path is the file, or for AsSkill the skill directory.
	Path string
	// AsSkill is true when the prompt is installed as a skill directory
	// holding a SKILL.md.
	AsSkill bool
}

// MCPPlace says where a target keeps its MCP servers.
type MCPPlace struct {
	// File is the JSON file with the top-level "mcpServers" object.
	File string
	// TOML is true when File is a TOML file instead, with one
	// [mcp_servers.<name>] table for each server.
	TOML bool
	// ViaCLI is true when the file belongs to the tool and must only be
	// changed through the tool's own command line.
	ViaCLI bool
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

// Spec describes one tool. A nil function, or an empty skills directory,
// means the tool does not support that kind of resource. A function
// returns false when the tool supports the kind, but not in this scope.
type Spec struct {
	// Name is what the user writes in the configuration and in the
	// frontmatter sections of agents, prompts and MCP servers.
	Name string

	// HomeDir is the directory the tool creates in the home directory when
	// it is installed, such as ".claude". It lets 'doctor' notice a tool
	// that is enabled but not there.
	HomeDir string

	// HomeSkills and ProjectSkills are the default skill directories,
	// relative to the home directory and to the project root.
	HomeSkills, ProjectSkills string

	// Instruction returns where an instruction is written.
	Instruction func(loc config.Location, name string, scoped bool) (Place, bool)
	// InstructionFile returns the content of an instruction that has a
	// file of its own. A block only ever holds the body.
	InstructionFile func(registry string, paths []string, body string) []byte

	// Agent returns the file an agent is written to.
	Agent func(loc config.Location, name string) (string, bool)
	// AgentNamedByFile is true when the tool identifies an agent by its
	// file name. The "name" in the frontmatter is then only a display name
	// and is left as the author wrote it. Otherwise the tool identifies an
	// agent by that field, which is set to the installed name.
	AgentNamedByFile bool
	// AgentFile returns the content of an agent file for a tool that does
	// not read Markdown with frontmatter. own is the frontmatter section
	// under the tool's name.
	AgentFile func(registry, name, description string, own map[string]any, body string) ([]byte, error)

	// Prompt returns where a prompt is written. skillsDir is the target's
	// skills directory, for tools that run prompts as skills.
	Prompt func(loc config.Location, skillsDir, name string) (PromptPlace, bool)

	// MCP returns where MCP servers are stored.
	MCP func(loc config.Location) (MCPPlace, bool)
	// MCPDefaults may add fields the tool requires to a server definition.
	MCPDefaults func(def map[string]any)

	// Hooks returns where hooks are stored, and HookEntry one entry in the
	// tool's form. The command already carries the marker.
	Hooks     func(loc config.Location) (HookPlace, bool)
	HookEntry func(command, matcher string, timeout int) map[string]any
	// HookEvents lists the events the tool fires. Empty means all of them.
	HookEvents []string
	// ReadsProjectHooksOf names tools whose project hooks file this tool
	// reads as well. A hook written there is not written again for this
	// tool, so it does not run twice.
	ReadsProjectHooksOf []string
}

var specs = map[string]Spec{}

// Register adds a tool. It is called from the init function of the file
// that describes the tool.
func Register(s Spec) {
	if _, dup := specs[s.Name]; dup {
		panic("target registered twice: " + s.Name)
	}
	specs[s.Name] = s
}

func spec(name string) (Spec, error) {
	s, ok := specs[name]
	if !ok {
		return Spec{}, fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(Names(), ", "))
	}
	return s, nil
}

// Names returns the supported target names in a stable order.
func Names() []string {
	names := make([]string, 0, len(specs))
	for n := range specs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Known reports whether name is a supported target.
func Known(name string) bool {
	_, ok := specs[name]
	return ok
}

// HomeDir returns the directory the named tool keeps in the home
// directory, or "" when that is not known.
func HomeDir(name string) string { return specs[name].HomeDir }

// ReadsProjectHooksOf returns the tools whose project hooks file the named
// tool reads in addition to its own.
func ReadsProjectHooksOf(name string) []string {
	return specs[name].ReadsProjectHooksOf
}

// SkillsDir returns the absolute directory skills are written to for the
// named target in the given location, honouring a configured override.
func SkillsDir(name string, t config.Target, loc config.Location) (string, error) {
	s, err := spec(name)
	if err != nil {
		return "", err
	}
	dir := s.HomeSkills
	if loc.Scope == config.ScopeProject {
		dir = s.ProjectSkills
	}
	if t.SkillsPath != "" {
		dir = t.SkillsPath
	}
	if dir == "" {
		return "", ErrUnsupported
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

// InstructionPlace returns where the named instruction is written.
func InstructionPlace(name string, loc config.Location, instruction string, scoped bool) (Place, error) {
	s, err := spec(name)
	if err != nil {
		return Place{}, err
	}
	if s.Instruction == nil {
		return Place{}, ErrUnsupported
	}
	place, ok := s.Instruction(loc, instruction, scoped)
	if !ok {
		return Place{}, ErrUnsupported
	}
	return place, nil
}

// RenderInstruction returns the content written for an instruction: the
// whole file, or for a block only its body.
func RenderInstruction(name, registry string, paths []string, body string, block bool) []byte {
	s, ok := specs[name]
	if block || !ok || s.InstructionFile == nil {
		return []byte(body)
	}
	return s.InstructionFile(registry, paths, body)
}

// InstructionNote is the line that tells a reader of an installed
// instruction file where it comes from.
func InstructionNote(registry string) string {
	return fmt.Sprintf("<!-- Installed by spellbook from registry %q. Change it there, not here. -->\n\n", registry)
}

// AgentFile returns the file an agent is written to.
func AgentFile(name string, loc config.Location, agent string) (string, error) {
	s, err := spec(name)
	if err != nil {
		return "", err
	}
	if s.Agent == nil {
		return "", ErrUnsupported
	}
	file, ok := s.Agent(loc, agent)
	if !ok {
		return "", ErrUnsupported
	}
	return file, nil
}

// AgentFields returns the frontmatter fields that are set for an agent
// installed under the given name.
func AgentFields(name, agent string) [][2]string {
	if specs[name].AgentNamedByFile {
		return nil
	}
	return [][2]string{{"name", agent}}
}

// PromptFor returns where the named prompt is written.
func PromptFor(name string, t config.Target, loc config.Location, prompt string) (PromptPlace, error) {
	s, err := spec(name)
	if err != nil {
		return PromptPlace{}, err
	}
	if s.Prompt == nil {
		return PromptPlace{}, ErrUnsupported
	}
	skills, err := SkillsDir(name, t, loc)
	if err != nil && !errors.Is(err, ErrUnsupported) {
		return PromptPlace{}, err
	}
	place, ok := s.Prompt(loc, skills, prompt)
	if !ok {
		return PromptPlace{}, ErrUnsupported
	}
	return place, nil
}

// MCPFor returns where MCP servers of a target are stored.
func MCPFor(name string, loc config.Location) (MCPPlace, error) {
	s, err := spec(name)
	if err != nil {
		return MCPPlace{}, err
	}
	if s.MCP == nil {
		return MCPPlace{}, ErrUnsupported
	}
	place, ok := s.MCP(loc)
	if !ok {
		return MCPPlace{}, ErrUnsupported
	}
	return place, nil
}

// RenderMCP returns the server definition for one target as canonical
// JSON: the fields under the target's own key are merged into the top
// level, and the keys of other targets are dropped.
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
	if s, ok := specs[name]; ok && s.MCPDefaults != nil {
		s.MCPDefaults(def)
	}
	return json.Marshal(def)
}

// HookFor returns where hooks of a target are stored.
func HookFor(name string, loc config.Location) (HookPlace, error) {
	s, err := spec(name)
	if err != nil {
		return HookPlace{}, err
	}
	if s.Hooks == nil || s.HookEntry == nil {
		return HookPlace{}, ErrUnsupported
	}
	place, ok := s.Hooks(loc)
	if !ok {
		return HookPlace{}, ErrUnsupported
	}
	return place, nil
}

// HookFires reports whether the named tool fires the event.
func HookFires(name, event string) bool {
	events := specs[name].HookEvents
	return len(events) == 0 || slices.Contains(events, event)
}

// RenderAgent returns the content of an agent file for one target.
func RenderAgent(name, registry, agent string, front *yaml.Node, body string) ([]byte, error) {
	s, ok := specs[name]
	if !ok || s.AgentFile == nil {
		return RenderDoc(name, front, body, AgentFields(name, agent))
	}
	fields := map[string]any{}
	if front != nil {
		if err := front.Decode(&fields); err != nil {
			return nil, err
		}
	}
	description, _ := fields["description"].(string)
	own, ok := fields[name].(map[string]any)
	if _, has := fields[name]; has && !ok {
		return nil, fmt.Errorf("the frontmatter key %q must hold 'key: value' lines", name)
	}
	return s.AgentFile(registry, agent, description, own, body)
}

// HookMarker is put behind a hook's command as a shell comment. It is how
// Spellbook finds its entries among the user's own.
const HookMarker = " # spellbook:"

// HookEntry returns the entry for one hook in the form the target reads,
// as canonical JSON.
func HookEntry(name, id, command, matcher string, timeout int) ([]byte, error) {
	s, err := spec(name)
	if err != nil {
		return nil, err
	}
	if s.HookEntry == nil {
		return nil, ErrUnsupported
	}
	return json.Marshal(s.HookEntry(command+HookMarker+id, matcher, timeout))
}

// RenderDoc returns the file content of an agent or prompt for one target.
//
// The frontmatter is passed on as written, with two changes: a mapping
// under the name of a tool, such as "claude", holds fields for that tool
// only and is merged into the top level for it, and the fields in set are added or
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
