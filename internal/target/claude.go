package target

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
)

// Claude Code.
//
// It loads every Markdown file in its rules directory, so each instruction
// gets a file of its own there and CLAUDE.md is never touched. It runs a
// skill as a slash command, so a prompt becomes a skill. User-level MCP
// servers live in ~/.claude.json, a file it rewrites itself, so those are
// changed through 'claude mcp' and only read from the file.
func init() {
	Register(Spec{
		Name:          "claude",
		HomeDir:       ".claude",
		HomeSkills:    ".claude/skills",
		ProjectSkills: ".claude/skills",
		Instruction: func(loc config.Location, name string, scoped bool) (Place, bool) {
			return Place{File: filepath.Join(loc.Root, ".claude", "rules", name+".md")}, true
		},
		InstructionFile: func(registry string, paths []string, body string) []byte {
			var b strings.Builder
			if len(paths) > 0 {
				b.WriteString("---\npaths:\n")
				for _, p := range paths {
					fmt.Fprintf(&b, "  - %q\n", p)
				}
				b.WriteString("---\n")
			}
			b.WriteString(InstructionNote(registry))
			b.WriteString(body)
			return []byte(b.String())
		},
		Agent: func(loc config.Location, name string) (string, bool) {
			return filepath.Join(loc.Root, ".claude", "agents", name+".md"), true
		},
		Prompt: func(loc config.Location, skillsDir, name string) (PromptPlace, bool) {
			if skillsDir == "" {
				return PromptPlace{}, false
			}
			return PromptPlace{Path: filepath.Join(skillsDir, name), AsSkill: true}, true
		},
		MCP: func(loc config.Location) (MCPPlace, bool) {
			if loc.Scope == config.ScopeProject {
				return MCPPlace{File: filepath.Join(loc.Root, ".mcp.json")}, true
			}
			return MCPPlace{File: filepath.Join(loc.Root, ".claude.json"), ViaCLI: true}, true
		},
		MCPDefaults: func(def map[string]any) {
			// Definitions written for Copilot call a local process "local"
			// and list the tools to enable. Claude Code calls the former
			// "stdio" and has no such list.
			if def["type"] == "local" {
				def["type"] = "stdio"
			}
			delete(def, "tools")
		},
		Hooks: func(loc config.Location) (HookPlace, bool) {
			return HookPlace{File: filepath.Join(loc.Root, ".claude", "settings.json")}, true
		},
		HookEntry: func(command, matcher string, timeout int) map[string]any {
			hook := map[string]any{"type": "command", "command": command}
			if timeout > 0 {
				hook["timeout"] = timeout
			}
			entry := map[string]any{"hooks": []any{hook}}
			if matcher != "" {
				entry["matcher"] = matcher
			}
			return entry
		},
	})
}
