package target

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
)

// GitHub Copilot.
//
// It reads path-scoped instructions from separate files, but always-on
// instructions only from copilot-instructions.md, so those become blocks.
// It reads prompt files from a project's .github/prompts only. Its hooks
// file uses Claude Code's event names, which it accepts together with
// Claude Code's matcher rules, and in a project it also runs the hooks in
// Claude Code's settings file.
func init() {
	base := func(loc config.Location) string {
		if loc.Scope == config.ScopeProject {
			return filepath.Join(loc.Root, ".github")
		}
		return filepath.Join(loc.Root, ".copilot")
	}
	Register(Spec{
		Name:          "copilot",
		HomeSkills:    ".copilot/skills",
		ProjectSkills: ".github/skills",
		Instruction: func(loc config.Location, name string, scoped bool) (Place, bool) {
			if scoped {
				return Place{File: filepath.Join(base(loc), "instructions", name+".instructions.md")}, true
			}
			return Place{File: filepath.Join(base(loc), "copilot-instructions.md"), Block: true}, true
		},
		InstructionFile: func(registry string, paths []string, body string) []byte {
			var b strings.Builder
			if len(paths) > 0 {
				fmt.Fprintf(&b, "---\napplyTo: %q\n---\n", strings.Join(paths, ","))
			}
			b.WriteString(InstructionNote(registry))
			b.WriteString(body)
			return []byte(b.String())
		},
		Agent: func(loc config.Location, name string) (string, bool) {
			return filepath.Join(base(loc), "agents", name+".agent.md"), true
		},
		AgentNamedByFile: true,
		Prompt: func(loc config.Location, skillsDir, name string) (PromptPlace, bool) {
			if loc.Scope != config.ScopeProject {
				return PromptPlace{}, false
			}
			return PromptPlace{Path: filepath.Join(loc.Root, ".github", "prompts", name+".prompt.md")}, true
		},
		MCP: func(loc config.Location) (MCPPlace, bool) {
			if loc.Scope == config.ScopeProject {
				return MCPPlace{File: filepath.Join(loc.Root, ".github", "mcp.json")}, true
			}
			return MCPPlace{File: filepath.Join(loc.Root, ".copilot", "mcp-config.json")}, true
		},
		MCPDefaults: func(def map[string]any) {
			// Copilot requires the field. Without one, all tools are on.
			if _, ok := def["tools"]; !ok {
				def["tools"] = []string{"*"}
			}
		},
		Hooks: func(loc config.Location) (HookPlace, bool) {
			return HookPlace{File: filepath.Join(base(loc), "hooks", "spellbook-hooks.json"), Owned: true}, true
		},
		HookEntry: func(command, matcher string, timeout int) map[string]any {
			entry := map[string]any{"type": "command", "command": command}
			if timeout > 0 {
				entry["timeoutSec"] = timeout
			}
			if matcher != "" {
				entry["matcher"] = matcher
			}
			return entry
		},
		ReadsProjectHooksOf: []string{"claude"},
	})
}
