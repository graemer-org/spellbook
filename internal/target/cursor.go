package target

import (
	"path/filepath"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
)

// Cursor.
//
// Everything lives below .cursor, in a project and in the home directory.
// A rule is an .mdc file whose frontmatter says when it applies; rules
// exist as files in a project only, because user rules are kept in
// Cursor's settings. A skill runs as a slash command, so a prompt becomes
// a skill. Hooks share one hooks.json with the user's own, under Cursor's
// event names, which are Claude Code's with a small first letter. Cursor
// can also run the hooks in Claude Code's settings files, so a hook
// written there is not written again.
//
// cursor.com could not be reached when this was written. The paths and
// fields below follow search summaries of cursor.com/docs/{skills,rules,
// subagents,mcp,hooks} and docs/reference/third-party-hooks, not the pages
// themselves, and none of them was confirmed word for word. Open in
// particular: whether "globs" may be quoted, whether hooks.json needs
// "version", what a matcher is compared with for events other than the
// tool events, and whether reading Claude Code's hooks is on by default.
func init() {
	base := func(loc config.Location) string { return filepath.Join(loc.Root, ".cursor") }
	// Cursor's name for each of Claude Code's events. It has no
	// Notification event.
	events := map[string]string{
		"SessionStart":       "sessionStart",
		"SessionEnd":         "sessionEnd",
		"UserPromptSubmit":   "beforeSubmitPrompt",
		"PreToolUse":         "preToolUse",
		"PostToolUse":        "postToolUse",
		"PostToolUseFailure": "postToolUseFailure",
		"Stop":               "stop",
		"SubagentStop":       "subagentStop",
		"PreCompact":         "preCompact",
	}
	Register(Spec{
		Name:          "cursor",
		HomeSkills:    ".cursor/skills",
		ProjectSkills: ".cursor/skills",
		Instruction: func(loc config.Location, name string, scoped bool) (Place, bool) {
			if loc.Scope != config.ScopeProject {
				return Place{}, false
			}
			return Place{File: filepath.Join(base(loc), "rules", name+".mdc")}, true
		},
		InstructionFile: func(registry string, paths []string, body string) []byte {
			var b strings.Builder
			b.WriteString("---\n")
			if len(paths) > 0 {
				// Written the way Cursor writes it itself: one line, the
				// patterns separated by commas, no quotes.
				b.WriteString("globs: " + strings.Join(paths, ",") + "\nalwaysApply: false\n")
			} else {
				b.WriteString("alwaysApply: true\n")
			}
			b.WriteString("---\n")
			b.WriteString(InstructionNote(registry))
			b.WriteString(body)
			return []byte(b.String())
		},
		Agent: func(loc config.Location, name string) (string, bool) {
			return filepath.Join(base(loc), "agents", name+".md"), true
		},
		Prompt: func(loc config.Location, skillsDir, name string) (PromptPlace, bool) {
			if skillsDir == "" {
				return PromptPlace{}, false
			}
			return PromptPlace{Path: filepath.Join(skillsDir, name), AsSkill: true}, true
		},
		MCP: func(loc config.Location) (MCPPlace, bool) {
			return MCPPlace{File: filepath.Join(base(loc), "mcp.json")}, true
		},
		Hooks: func(loc config.Location) (HookPlace, bool) {
			return HookPlace{File: filepath.Join(base(loc), "hooks.json"), Version: true}, true
		},
		HookEvent: func(event string) (string, bool) {
			name, ok := events[event]
			return name, ok
		},
		HookEntry: func(command, matcher string, timeout int) map[string]any {
			entry := map[string]any{"command": command}
			if timeout > 0 {
				entry["timeout"] = timeout
			}
			if matcher != "" {
				entry["matcher"] = matcher
			}
			return entry
		},
		ReadsProjectHooksOf: []string{"claude"},
		ReadsHomeHooksOf:    []string{"claude"},
	})
}
