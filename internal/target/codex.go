package target

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/graemer-org/spellbook/internal/config"
)

// OpenAI Codex (the Codex CLI).
//
// It reads instructions from AGENTS.md, a file the user writes as well, so
// every instruction becomes a block there: ~/.codex/AGENTS.md at home and
// AGENTS.md in the project root. It has no instructions that apply to
// some paths only, apart from an AGENTS.md in a subdirectory, so those are
// not installed. Skills are read from .agents/skills. An agent is a TOML
// file that is at the same time a layer of Codex's configuration and that
// rejects unknown keys, so only the name, the description, the text and
// the fields of the "codex:" section are written. MCP servers are tables
// in config.toml, next to all other settings. Hooks are kept in hooks.json
// in Claude Code's form. Codex no longer loads prompt files, so prompts
// are not installed.
//
// Verified against the source of github.com/openai/codex at commit
// ae50f973 (2026-11-12), because developers.openai.com could not be
// reached when this was written:
//
//   - skills: ~/.agents/skills and <dir>/.agents/skills
//     (ext/skills/src/host_roots.rs); $CODEX_HOME/skills is deprecated.
//   - instructions: $CODEX_HOME/AGENTS.md and AGENTS.md from the project
//     root down (core/src/agents_md.rs). An AGENTS.override.md beside
//     either one replaces it. No includes, no path patterns.
//   - agents: <config folder>/agents/*.toml with the required keys name,
//     description and developer_instructions (agent-roles/src/loader.rs).
//   - MCP: [mcp_servers.<name>] with command, args, env, env_vars, cwd, or
//     url, bearer_token_env_var, http_headers, env_http_headers
//     (config/src/mcp_types.rs). Unknown keys are an error.
//   - hooks: <config folder>/hooks.json with "hooks" mapping an event to
//     groups of {matcher, hooks: [{type, command, timeout}]}, timeout in
//     seconds (config/src/hook_config.rs). Commands run in the directory
//     of the session and get no variable naming the project.
//
// Codex only reads .codex/ in a project the user has marked as trusted,
// and it runs a new or changed hook only after the user has approved it.
func init() {
	base := func(loc config.Location) string { return filepath.Join(loc.Root, ".codex") }
	Register(Spec{
		Name:          "codex",
		HomeSkills:    ".agents/skills",
		ProjectSkills: ".agents/skills",
		Instruction: func(loc config.Location, name string, scoped bool) (Place, bool) {
			if scoped {
				return Place{}, false
			}
			if loc.Scope == config.ScopeProject {
				return Place{File: filepath.Join(loc.Root, "AGENTS.md"), Block: true}, true
			}
			return Place{File: filepath.Join(base(loc), "AGENTS.md"), Block: true}, true
		},
		Agent: func(loc config.Location, name string) (string, bool) {
			return filepath.Join(base(loc), "agents", name+".toml"), true
		},
		AgentFile: func(registry, name, description string, own map[string]any, body string) ([]byte, error) {
			file := map[string]any{"description": description, "developer_instructions": body}
			for k, v := range own {
				file[k] = v
			}
			file["name"] = name
			var b bytes.Buffer
			fmt.Fprintf(&b, "# Installed by spellbook from registry %q. Change it there, not here.\n", registry)
			enc := toml.NewEncoder(&b)
			enc.Indent = ""
			if err := enc.Encode(file); err != nil {
				return nil, err
			}
			return b.Bytes(), nil
		},
		MCP: func(loc config.Location) (MCPPlace, bool) {
			return MCPPlace{File: filepath.Join(base(loc), "config.toml"), TOML: true}, true
		},
		MCPDefaults: codexMCP,
		Hooks: func(loc config.Location) (HookPlace, bool) {
			return HookPlace{File: filepath.Join(base(loc), "hooks.json")}, true
		},
		HookEntry: func(command, matcher string, timeout int) map[string]any {
			// Codex names no project directory and starts a hook wherever
			// the session runs, so the root is asked from git instead.
			command = strings.ReplaceAll(command, "${CLAUDE_PROJECT_DIR:-.}", "$(git rev-parse --show-toplevel 2>/dev/null || echo .)")
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
		HookEvents: []string{
			"SessionStart", "SessionEnd", "UserPromptSubmit",
			"PreToolUse", "PostToolUse", "Stop", "SubagentStop", "PreCompact",
		},
	})
}

var envRef = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// codexMCP turns a server definition written for "mcpServers" into the
// keys of an [mcp_servers] table. Codex does not expand ${NAME} in values.
// Where a value is exactly such a reference, the key that makes Codex read
// the variable itself is used instead.
func codexMCP(def map[string]any) {
	// The transport follows from "command" or "url".
	delete(def, "type")

	if env, ok := def["env"].(map[string]any); ok {
		var forward []any
		for _, name := range sortedKeys(env) {
			if v, _ := env[name].(string); envRef.FindStringSubmatch(v) != nil && v == "${"+name+"}" {
				forward = append(forward, name)
				delete(env, name)
			}
		}
		if len(forward) > 0 {
			if _, set := def["env_vars"]; !set {
				def["env_vars"] = forward
			}
		}
		if len(env) == 0 {
			delete(def, "env")
		}
	}

	headers, ok := def["headers"].(map[string]any)
	if !ok {
		return
	}
	delete(def, "headers")
	fixed, fromEnv := map[string]any{}, map[string]any{}
	for _, name := range sortedKeys(headers) {
		v, _ := headers[name].(string)
		if m := envRef.FindStringSubmatch(v); m != nil {
			fromEnv[name] = m[1]
		} else if m := envRef.FindStringSubmatch(strings.TrimPrefix(v, "Bearer ")); m != nil && strings.EqualFold(name, "Authorization") {
			if _, set := def["bearer_token_env_var"]; !set {
				def["bearer_token_env_var"] = m[1]
			}
		} else {
			fixed[name] = headers[name]
		}
	}
	for key, values := range map[string]map[string]any{"http_headers": fixed, "env_http_headers": fromEnv} {
		if _, set := def[key]; !set && len(values) > 0 {
			def[key] = values
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
