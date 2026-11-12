package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// geminiRepo is a registry with every kind of resource.
func (e *env) geminiRepo() string {
	e.t.Helper()
	repo := e.repo(map[string]string{"skills/review": "v1"})
	write(e.t, filepath.Join(repo, "instructions", "standards.md"), "# Standards\n\nUse tabs. v1\n")
	write(e.t, filepath.Join(repo, "instructions", "tf-style.md"), "---\npaths:\n  - \"infra/**/*.tf\"\n---\nPin provider versions.\n")
	write(e.t, filepath.Join(repo, "agents", "reviewer.md"), `---
description: Reviews pull requests
tools: Read, Grep
custom-field: kept
claude:
  model: opus
gemini:
  tools: [read_file, grep_search]
  max_turns: 12
---

You review code. v1
`)
	write(e.t, filepath.Join(repo, "prompts", "release-notes.md"), `---
description: Draft release notes
claude:
  argument-hint: "[version]"
---

Write "release notes" for $ARGUMENTS.

Keep a \ and """three quotes""" as they are.
`)
	write(e.t, filepath.Join(repo, "prompts", "standup.md"), `---
description: For the others
gemini:
  description: Summarise yesterday
---
What did I do yesterday?
`)
	write(e.t, filepath.Join(repo, "mcp", "docs.json"), `{
  "command": "npx",
  "args": ["-y", "@acme/docs-mcp"],
  "env": {"DOCS_TOKEN": "${DOCS_TOKEN}"},
  "copilot": {"tools": ["search"]},
  "gemini": {"trust": false}
}`)
	write(e.t, filepath.Join(repo, "mcp", "tracker.json"), `{"type": "http", "url": "https://mcp.example.com/mcp"}`)
	write(e.t, filepath.Join(repo, "mcp", "events.json"), `{"type": "sse", "url": "https://mcp.example.com/sse"}`)
	// Written for Claude Code: Gemini CLI has no tool called Bash.
	write(e.t, filepath.Join(repo, "hooks", "no-force-push.json"), `{
  "event": "PreToolUse",
  "matcher": "Bash",
  "command": "grep -q 'push --force' && exit 2 || exit 0"
}`)
	write(e.t, filepath.Join(repo, "hooks", "guard.json"), `{
  "event": "PreToolUse",
  "matcher": "run_shell_command",
  "command": "grep -q 'push --force' && exit 2 || exit 0",
  "timeout": 10,
  "tools": ["gemini"]
}`)
	write(e.t, filepath.Join(repo, "hooks", "greet", "hook.json"), `{
  "event": "SessionStart",
  "command": "\"${HOOK_DIR}/greet.sh\"",
  "tools": ["gemini"]
}`)
	write(e.t, filepath.Join(repo, "hooks", "greet", "greet.sh"), "#!/bin/sh\necho v1\n")
	if err := os.Chmod(filepath.Join(repo, "hooks", "greet", "greet.sh"), 0o755); err != nil {
		e.t.Fatal(err)
	}
	// Gemini CLI has no such event.
	write(e.t, filepath.Join(repo, "hooks", "on-failure.json"), `{
  "event": "PostToolUseFailure",
  "command": "true",
  "tools": ["gemini"]
}`)
	e.commit(repo, "everything")
	return repo
}

// geminiSettings is what the user has in settings.json before the sync.
const geminiSettings = `{
  "general": {"vimMode": true},
  "mcpServers": {
    "mine": {"command": "my-server"}
  },
  "hooks": {
    "BeforeTool": [{"matcher": "write_file", "hooks": [{"type": "command", "command": "echo mine"}]}]
  },
  "ui": {"theme": "GitHub"}
}
`

// checkGemini syncs geminiRepo into root, the home directory or a project,
// and checks every kind, a second sync and the removal.
func checkGemini(t *testing.T, e *env, root, instructions, scripts, scriptRef string, scope ...string) {
	t.Helper()
	run := func(args ...string) string { return e.mustRun(append(args, scope...)...) }
	gemini := filepath.Join(root, ".gemini")
	settingsFile := filepath.Join(gemini, "settings.json")
	write(t, settingsFile, geminiSettings)
	write(t, instructions, "# Mine\n\nAnswer in German.\n")
	repo := e.geminiRepo()

	run("init", "--target", "gemini")
	out := run("registry", "add", "acme", repo, "--allow", "mcp", "--allow", "hooks")
	for _, want := range []string{
		"gemini: skipped 1 instructions: not supported by this tool at this level",
		"gemini: skipped 1 hooks: not supported by this tool at this level",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}

	// Skill.
	if got := read(t, filepath.Join(gemini, "skills", "review", "SKILL.md")); !strings.Contains(got, "name: review") {
		t.Errorf("skill is wrong:\n%s", got)
	}

	// Instructions: a block in GEMINI.md, the scoped one nowhere.
	md := read(t, instructions)
	for _, want := range []string{"# Mine\n\nAnswer in German.\n", "<!-- spellbook:begin acme/standards -->", "Use tabs. v1", "<!-- spellbook:end acme/standards -->"} {
		if !strings.Contains(md, want) {
			t.Errorf("GEMINI.md misses %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Pin provider versions") {
		t.Errorf("a path-scoped instruction became always-on:\n%s", md)
	}

	// Agent: Markdown with frontmatter, the gemini section merged in.
	agent := read(t, filepath.Join(gemini, "agents", "reviewer.md"))
	for _, want := range []string{"name: reviewer\n", "description: Reviews pull requests\n", "custom-field: kept\n", "tools: [read_file, grep_search]\n", "max_turns: 12\n", "You review code. v1"} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent misses %q:\n%s", want, agent)
		}
	}
	for _, bad := range []string{"opus", "claude:", "gemini:", "Read, Grep"} {
		if strings.Contains(agent, bad) {
			t.Errorf("agent should not contain %q:\n%s", bad, agent)
		}
	}

	// Prompts: TOML command files.
	var command struct {
		Description string `toml:"description"`
		Prompt      string `toml:"prompt"`
	}
	commandFile := filepath.Join(gemini, "commands", "release-notes.toml")
	md1, err := toml.DecodeFile(commandFile, &command)
	if err != nil {
		t.Fatalf("command is not valid TOML: %v\n%s", err, read(t, commandFile))
	}
	if len(md1.Undecoded()) != 0 {
		t.Errorf("command has keys Gemini CLI does not document: %v", md1.Undecoded())
	}
	if command.Description != "Draft release notes" {
		t.Errorf("description = %q", command.Description)
	}
	if want := "Write \"release notes\" for {{args}}.\n\nKeep a \\ and \"\"\"three quotes\"\"\" as they are.\n"; command.Prompt != want {
		t.Errorf("prompt = %q, want %q", command.Prompt, want)
	}
	if _, err := toml.DecodeFile(filepath.Join(gemini, "commands", "standup.toml"), &command); err != nil || command.Description != "Summarise yesterday" {
		t.Errorf("the gemini section should set the description: %q, %v", command.Description, err)
	}

	// Settings: servers and hooks added, everything else as it was.
	settings := read(t, settingsFile)
	var parsed struct {
		General    map[string]any
		UI         map[string]any
		MCPServers map[string]map[string]any `json:"mcpServers"`
		Hooks      map[string][]struct {
			Matcher string
			Hooks   []map[string]any
		}
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, settings)
	}
	if parsed.General["vimMode"] != true || parsed.UI["theme"] != "GitHub" || parsed.MCPServers["mine"]["command"] != "my-server" {
		t.Errorf("the user's settings changed:\n%s", settings)
	}
	if g, m, h, u := strings.Index(settings, `"general"`), strings.Index(settings, `"mcpServers"`), strings.Index(settings, `"hooks"`), strings.Index(settings, `"ui"`); !(g < m && m < h && h < u) {
		t.Errorf("the order of the settings changed:\n%s", settings)
	}
	docs := parsed.MCPServers["docs"]
	if docs["command"] != "npx" || docs["trust"] != false || docs["tools"] != nil || docs["gemini"] != nil || docs["copilot"] != nil {
		t.Errorf("docs server is wrong: %v", docs)
	}
	if s := parsed.MCPServers["tracker"]; s["httpUrl"] != "https://mcp.example.com/mcp" || s["url"] != nil || s["type"] != nil {
		t.Errorf("a streamable HTTP server must use httpUrl: %v", s)
	}
	if s := parsed.MCPServers["events"]; s["url"] != "https://mcp.example.com/sse" || s["httpUrl"] != nil || s["type"] != nil {
		t.Errorf("an SSE server must use url: %v", s)
	}
	before := parsed.Hooks["BeforeTool"]
	if len(before) != 2 || before[0].Hooks[0]["command"] != "echo mine" {
		t.Fatalf("BeforeTool should hold the user's hook and one more:\n%s", settings)
	}
	guard := before[1]
	if guard.Matcher != "run_shell_command" || guard.Hooks[0]["type"] != "command" || guard.Hooks[0]["timeout"] != float64(10000) ||
		!strings.HasSuffix(guard.Hooks[0]["command"].(string), " # spellbook:acme/guard") {
		t.Errorf("guard hook is wrong: %+v", guard)
	}
	start := parsed.Hooks["SessionStart"]
	if len(start) != 1 || start[0].Hooks[0]["command"] != `"`+scriptRef+`/greet.sh" # spellbook:acme/greet` {
		t.Errorf("SessionStart hook is wrong: %+v", start)
	}
	if info, err := os.Stat(filepath.Join(scripts, "greet", "greet.sh")); err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("the hook's script is missing or not executable: %v", err)
	}
	for _, bad := range []string{"PreToolUse", "PostToolUseFailure", "no-force-push", "on-failure"} {
		if strings.Contains(settings, bad) {
			t.Errorf("settings should not contain %q:\n%s", bad, settings)
		}
	}

	// A second sync changes nothing.
	files := []string{settingsFile, instructions, commandFile, filepath.Join(gemini, "agents", "reviewer.md")}
	stamps := map[string]os.FileInfo{}
	for _, f := range files {
		stamps[f], _ = os.Stat(f)
	}
	if out := run("sync"); !strings.Contains(out, "0 installed, 0 updated") || strings.Contains(out, "unchanged, 0 removed") == false {
		t.Errorf("second sync is not a no-op:\n%s", out)
	}
	for _, f := range files {
		if now, _ := os.Stat(f); !os.SameFile(stamps[f], now) {
			t.Errorf("second sync rewrote %s", f)
		}
	}
	if read(t, settingsFile) != settings || read(t, instructions) != md {
		t.Error("second sync changed a shared file")
	}

	// A local edit is kept until --force.
	write(t, commandFile, "prompt = \"mine\"\n")
	write(t, filepath.Join(repo, "prompts", "release-notes.md"), "Write release notes. v2\n")
	e.commit(repo, "v2")
	if out := run("sync"); !strings.Contains(out, "was edited locally") || read(t, commandFile) != "prompt = \"mine\"\n" {
		t.Errorf("an edited command was overwritten:\n%s", out)
	}
	run("sync", "--force")
	if got := read(t, commandFile); !strings.Contains(got, "Write release notes. v2") {
		t.Errorf("--force did not update the command:\n%s", got)
	}

	// Removal takes only what Spellbook wrote.
	run("registry", "remove", "acme")
	for _, rel := range []string{"skills/review", "agents/reviewer.md", "commands/release-notes.toml", "commands/standup.toml"} {
		if exists(filepath.Join(gemini, rel)) {
			t.Errorf("%s was not removed", rel)
		}
	}
	if exists(filepath.Join(scripts, "greet")) {
		t.Error("the hook's scripts were not removed")
	}
	if got := read(t, instructions); got != "# Mine\n\nAnswer in German.\n" {
		t.Errorf("GEMINI.md is not what the user wrote:\n%q", got)
	}
	settings = read(t, settingsFile)
	if strings.Contains(settings, "spellbook") || strings.Contains(settings, "docs") || strings.Contains(settings, "SessionStart") {
		t.Errorf("settings still hold what spellbook wrote:\n%s", settings)
	}
	var a, b any
	if json.Unmarshal([]byte(settings), &a) != nil || json.Unmarshal([]byte(geminiSettings), &b) != nil {
		t.Fatalf("settings.json is not valid JSON:\n%s", settings)
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if string(x) != string(y) {
		t.Errorf("settings differ from what the user had:\n%s", settings)
	}
}

func TestGeminiAtHome(t *testing.T) {
	e := newEnv(t)
	scripts := filepath.Join(e.home, ".config", "spellbook", "hooks")
	checkGemini(t, e, e.home, filepath.Join(e.home, ".gemini", "GEMINI.md"), scripts, filepath.Join(scripts, "greet"))
	if exists(filepath.Join(e.home, "GEMINI.md")) {
		t.Error("at home the instructions belong in ~/.gemini/GEMINI.md")
	}
}

func TestGeminiInAProject(t *testing.T) {
	e := newEnv(t)
	project := t.TempDir()
	t.Chdir(project)
	checkGemini(t, e, project, filepath.Join(project, "GEMINI.md"), filepath.Join(project, ".spellbook", "hooks"), "${CLAUDE_PROJECT_DIR:-.}/.spellbook/hooks/greet", "--project")
	if exists(filepath.Join(e.home, ".gemini")) {
		t.Error("project sync wrote into the home directory")
	}
}

func TestGeminiFilesWithOnlySpellbookContentGoWithIt(t *testing.T) {
	e := newEnv(t)
	repo := e.geminiRepo()
	e.mustRun("init", "--target", "gemini")
	e.mustRun("registry", "add", "acme", repo, "--allow", "mcp", "--allow", "hooks")
	gemini := filepath.Join(e.home, ".gemini")
	if !exists(filepath.Join(gemini, "settings.json")) || !exists(filepath.Join(gemini, "GEMINI.md")) {
		t.Fatal("settings.json and GEMINI.md should have been created")
	}
	e.mustRun("registry", "remove", "acme")
	if exists(filepath.Join(gemini, "settings.json")) || exists(filepath.Join(gemini, "GEMINI.md")) {
		t.Error("files that held nothing of the user's should be gone")
	}
}

func TestGeminiUnmanagedFilesAndServersAreLeftAlone(t *testing.T) {
	e := newEnv(t)
	repo := e.geminiRepo()
	gemini := filepath.Join(e.home, ".gemini")
	write(t, filepath.Join(gemini, "commands", "standup.toml"), "prompt = \"mine\"\n")
	write(t, filepath.Join(gemini, "agents", "reviewer.md"), "mine\n")
	write(t, filepath.Join(gemini, "settings.json"), `{"mcpServers": {"docs": {"command": "mine"}}}`+"\n")
	e.mustRun("init", "--target", "gemini")
	e.mustRun("registry", "add", "acme", repo, "--allow", "mcp")
	// Not even --force may take what the state file does not record.
	out := e.mustRun("sync", "--force")
	for _, want := range []string{`skipped prompt "standup"`, `skipped agent "reviewer"`, `skipped MCP server "docs"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	if read(t, filepath.Join(gemini, "commands", "standup.toml")) != "prompt = \"mine\"\n" || read(t, filepath.Join(gemini, "agents", "reviewer.md")) != "mine\n" {
		t.Error("a file of the user was overwritten")
	}
	if got := read(t, filepath.Join(gemini, "settings.json")); !strings.Contains(got, `"command": "mine"`) || strings.Contains(got, "npx") {
		t.Errorf("a server of the user was overwritten:\n%s", got)
	}
}

func TestGeminiBrokenSettingsAreNotTouched(t *testing.T) {
	e := newEnv(t)
	repo := e.geminiRepo()
	settings := filepath.Join(e.home, ".gemini", "settings.json")
	broken := "{\n  // my comment\n  \"ui\": {}\n}\n"
	write(t, settings, broken)
	e.mustRun("init", "--target", "gemini")
	code, out := e.run("registry", "add", "acme", repo, "--allow", "mcp")
	if code == 0 || !strings.Contains(out, "not valid JSON") {
		t.Errorf("exit %d, expected an error about the file:\n%s", code, out)
	}
	if read(t, settings) != broken {
		t.Error("a settings file that is not valid JSON was changed")
	}
}

func TestGeminiAutosyncHook(t *testing.T) {
	e := newEnv(t)
	settings := filepath.Join(e.home, ".gemini", "settings.json")
	write(t, settings, `{
  "general": {"vimMode": true},
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "echo mine"}]}]
  },
  "ui": {"theme": "GitHub"}
}
`)
	e.mustRun("init", "--target", "gemini")
	if out := e.mustRun("autosync", "enable"); !strings.Contains(out, "gemini   hook installed in "+settings) {
		t.Errorf("enable did not report the hook:\n%s", out)
	}
	e.mustRun("autosync", "enable")
	got := read(t, settings)
	if strings.Count(got, "spellbook sync --auto") != 1 {
		t.Errorf("expected exactly one spellbook hook:\n%s", got)
	}
	for _, want := range []string{`"vimMode": true`, "echo mine", `"theme": "GitHub"`, `"matcher": "startup"`, `"command": "spellbook sync --auto"`, `"timeout": 120000`} {
		if !strings.Contains(got, want) {
			t.Errorf("settings miss %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "async") {
		t.Errorf("Gemini CLI has no async hooks:\n%s", got)
	}
	if g, h, u := strings.Index(got, `"general"`), strings.Index(got, `"hooks"`), strings.Index(got, `"ui"`); !(g < h && h < u) {
		t.Errorf("the order of the settings changed:\n%s", got)
	}
	if out := e.mustRun("autosync", "status"); !strings.Contains(out, "gemini   hook installed") {
		t.Errorf("status should show the hook:\n%s", out)
	}
	e.mustRun("autosync", "disable")
	got = read(t, settings)
	if strings.Contains(got, "spellbook") || !strings.Contains(got, "echo mine") || !strings.Contains(got, `"theme": "GitHub"`) {
		t.Errorf("disable should remove only spellbook's hook:\n%s", got)
	}
}
