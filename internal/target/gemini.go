package target

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
)

// Gemini CLI.
//
// It reads always-on instructions only from GEMINI.md, a file the user
// writes too, so those become blocks: in ~/.gemini/GEMINI.md at home and in
// the GEMINI.md at the root of a project. It has no instructions that apply
// to some paths only, so those are skipped. A prompt becomes a custom
// command, which is a TOML file. MCP servers and hooks are keys of its
// settings.json, next to everything else the user has set there.
//
// Its hooks differ from Claude Code's in more than their names: tools are
// called run_shell_command or write_file, the input a hook receives has
// other fields, and a timeout is given in milliseconds. A hook written for
// Claude Code would be installed and then never match, or misread its
// input. So a hook reaches Gemini CLI only when its "tools" list names
// gemini; its event is then translated and its matcher passed on as it is.
//
// Verified against the documentation in google-gemini/gemini-cli (docs/ on
// main) in November 2026:
//   - skills: ~/.gemini/skills and .gemini/skills, a directory with SKILL.md
//   - GEMINI.md: ~/.gemini/GEMINI.md and the project's GEMINI.md; nothing
//     scoped to paths and no rules directory is documented
//   - custom commands: ~/.gemini/commands/<name>.toml and
//     .gemini/commands/<name>.toml with "prompt" and "description";
//     {{args}} stands for what the user typed after the command
//   - subagents: ~/.gemini/agents/<name>.md and .gemini/agents/<name>.md,
//     Markdown with frontmatter, "name" and "description" required
//   - MCP: "mcpServers" in ~/.gemini/settings.json and
//     .gemini/settings.json; "command" for stdio, "url" for SSE, "httpUrl"
//     for streamable HTTP, and no "type" field
//   - hooks: "hooks" in the same files, event -> [{matcher, hooks:
//     [{type: "command", command, timeout}]}], the event names below, and
//     CLAUDE_PROJECT_DIR as an alias of GEMINI_PROJECT_DIR
//
// Not settled by the documentation: whether an agent with frontmatter
// fields Gemini CLI does not know is rejected, and whether settings.json
// may hold comments (a file with comments is not valid JSON for Spellbook
// and is left alone).
func init() {
	base := func(loc config.Location) string { return filepath.Join(loc.Root, ".gemini") }
	settings := func(loc config.Location) string { return filepath.Join(base(loc), "settings.json") }
	// Claude Code's event names, which registries use, and what Gemini CLI
	// calls the same moment. PostToolUseFailure and SubagentStop have no
	// counterpart.
	events := map[string]string{
		"SessionStart":     "SessionStart",
		"SessionEnd":       "SessionEnd",
		"UserPromptSubmit": "BeforeAgent",
		"PreToolUse":       "BeforeTool",
		"PostToolUse":      "AfterTool",
		"Stop":             "AfterAgent",
		"PreCompact":       "PreCompress",
		"Notification":     "Notification",
	}
	Register(Spec{
		Name:          "gemini",
		HomeSkills:    ".gemini/skills",
		ProjectSkills: ".gemini/skills",
		Instruction: func(loc config.Location, name string, scoped bool) (Place, bool) {
			if scoped {
				return Place{}, false
			}
			if loc.Scope == config.ScopeProject {
				return Place{File: filepath.Join(loc.Root, "GEMINI.md"), Block: true}, true
			}
			return Place{File: filepath.Join(base(loc), "GEMINI.md"), Block: true}, true
		},
		Agent: func(loc config.Location, name string) (string, bool) {
			return filepath.Join(base(loc), "agents", name+".md"), true
		},
		Prompt: func(loc config.Location, skillsDir, name string) (PromptPlace, bool) {
			return PromptPlace{Path: filepath.Join(base(loc), "commands", name+".toml")}, true
		},
		PromptFile: func(registry, description, body string) []byte {
			var b strings.Builder
			fmt.Fprintf(&b, "# Installed by spellbook from registry %q. Change it there, not here.\n\n", registry)
			if description != "" {
				fmt.Fprintf(&b, "description = %s\n", tomlString(description, false))
			}
			// Registries write $ARGUMENTS for what the user typed.
			body = strings.ReplaceAll(body, "$ARGUMENTS", "{{args}}")
			fmt.Fprintf(&b, "prompt = %s\n", tomlString(strings.TrimRight(body, "\n")+"\n", true))
			return []byte(b.String())
		},
		MCP: func(loc config.Location) (MCPPlace, bool) {
			return MCPPlace{File: settings(loc)}, true
		},
		MCPDefaults: func(def map[string]any) {
			// Gemini CLI tells the transports apart by the name of the
			// field, not by a "type".
			kind, _ := def["type"].(string)
			delete(def, "type")
			if _, has := def["httpUrl"]; has || (kind != "http" && kind != "streamable-http") {
				return
			}
			if url, ok := def["url"]; ok {
				def["httpUrl"] = url
				delete(def, "url")
			}
		},
		Hooks: func(loc config.Location) (HookPlace, bool) {
			return HookPlace{File: settings(loc)}, true
		},
		HookEntry: func(command, matcher string, timeout int) map[string]any {
			hook := map[string]any{"type": "command", "command": command}
			if timeout > 0 {
				// Registries give seconds, Gemini CLI wants milliseconds.
				hook["timeout"] = timeout * 1000
			}
			entry := map[string]any{"hooks": []any{hook}}
			if matcher != "" {
				entry["matcher"] = matcher
			}
			return entry
		},
		HookEvent: func(event string) (string, bool) {
			own, ok := events[event]
			return own, ok
		},
		HooksByName: true,
	})
}

// tomlString returns s as a TOML basic string, over several lines when
// multiline is set.
func tomlString(s string, multiline bool) string {
	var b strings.Builder
	quote := `"`
	if multiline {
		quote = `"""`
		b.WriteString(quote + "\n")
	} else {
		b.WriteString(quote)
	}
	for _, r := range strings.ReplaceAll(s, "\r\n", "\n") {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			// Escaping every quote keeps three in a row from ending the
			// string.
			b.WriteString(`\"`)
		case r == '\n' && multiline, r == '\t':
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(quote)
	return b.String()
}
