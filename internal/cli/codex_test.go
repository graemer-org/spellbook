package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// codexRepo is a registry with every kind of resource.
func (e *env) codexRepo() string {
	e.t.Helper()
	repo := e.repo(map[string]string{"skills/review": "v1"})
	write(e.t, filepath.Join(repo, "instructions", "standards.md"), "Use tabs. v1\n")
	write(e.t, filepath.Join(repo, "instructions", "tf-style.md"), "---\npaths:\n  - \"infra/**/*.tf\"\n---\nPin provider versions.\n")
	write(e.t, filepath.Join(repo, "agents", "reviewer.md"), `---
description: Reviews pull requests
tools: Read, Grep
claude:
  model: opus
codex:
  model: gpt-5.2
  model_reasoning_effort: high
---

You review code.
Say "no" to force pushes. v1
`)
	write(e.t, filepath.Join(repo, "prompts", "release-notes.md"), "Write release notes.\n")
	write(e.t, filepath.Join(repo, "mcp", "docs.json"), `{
  "command": "npx",
  "args": ["-y", "@acme/docs-mcp"],
  "env": {"DOCS_TOKEN": "${DOCS_TOKEN}", "MODE": "fast"},
  "copilot": {"tools": ["search"]},
  "codex": {"startup_timeout_sec": 20}
}`)
	write(e.t, filepath.Join(repo, "mcp", "tracker.json"), `{
  "type": "http",
  "url": "https://mcp.example.com/mcp",
  "headers": {"Authorization": "Bearer ${TRACKER_TOKEN}", "X-Region": "eu", "X-Key": "${TRACKER_KEY}"}
}`)
	write(e.t, filepath.Join(repo, "hooks", "no-force-push.json"), `{"event": "PreToolUse", "matcher": "Bash", "command": "check-push", "timeout": 10}`)
	write(e.t, filepath.Join(repo, "hooks", "notify.json"), `{"event": "Notification", "command": "true"}`)
	write(e.t, filepath.Join(repo, "hooks", "format", "hook.json"), `{"event": "PostToolUse", "matcher": "Edit|Write", "command": "\"${HOOK_DIR}/format.sh\""}`)
	write(e.t, filepath.Join(repo, "hooks", "format", "format.sh"), "#!/bin/sh\necho v1\n")
	e.commit(repo, "everything")
	return repo
}

const (
	codexConfig = `# my settings
model = "gpt-5" # keep

[mcp_servers.own]
command = "own-server"

[tui]
notifications = true
`
	codexHooks = `{
  "description": "mine",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "echo mine"}]}]
  }
}
`
	codexAgents = "# My rules\n\nBe brief.\n"
)

// checkCodex looks at what a sync of codexRepo wrote below dir, which is
// ~/.codex or <project>/.codex.
func checkCodex(t *testing.T, dir, skills, agentsMD string) {
	t.Helper()
	if !exists(filepath.Join(skills, "review", "SKILL.md")) {
		t.Errorf("the skill is missing in %s", skills)
	}
	if exists(filepath.Join(skills, "release-notes")) {
		t.Error("a prompt must not be installed, Codex has no prompts")
	}

	got := read(t, agentsMD)
	if !strings.HasPrefix(got, codexAgents) || !strings.Contains(got, "<!-- spellbook:begin acme/standards -->\nUse tabs. v1\n<!-- spellbook:end acme/standards -->") {
		t.Errorf("AGENTS.md should keep the user's text and get a block:\n%s", got)
	}
	if strings.Contains(got, "Pin provider versions") {
		t.Errorf("an instruction for some paths must not apply everywhere:\n%s", got)
	}

	var agent map[string]any
	if _, err := toml.DecodeFile(filepath.Join(dir, "agents", "reviewer.toml"), &agent); err != nil {
		t.Fatalf("the agent is not valid TOML: %v", err)
	}
	wantAgent := map[string]any{
		"name": "reviewer", "description": "Reviews pull requests",
		"developer_instructions": "You review code.\nSay \"no\" to force pushes. v1\n",
		"model":                  "gpt-5.2", "model_reasoning_effort": "high",
	}
	if len(agent) != len(wantAgent) {
		t.Errorf("Codex rejects unknown keys in an agent file, got %v", agent)
	}
	for k, v := range wantAgent {
		if agent[k] != v {
			t.Errorf("agent: %s = %q, want %q", k, agent[k], v)
		}
	}

	config := read(t, filepath.Join(dir, "config.toml"))
	if !strings.HasPrefix(config, codexConfig) {
		t.Errorf("config.toml should begin with the user's settings unchanged:\n%s", config)
	}
	for _, want := range []string{`
# Installed by spellbook from registry "acme". Change it there, not here.
[mcp_servers.docs]
args = ["-y", "@acme/docs-mcp"]
command = "npx"
env_vars = ["DOCS_TOKEN"]
startup_timeout_sec = 20

[mcp_servers.docs.env]
MODE = "fast"
`, `
[mcp_servers.tracker]
bearer_token_env_var = "TRACKER_TOKEN"
url = "https://mcp.example.com/mcp"

[mcp_servers.tracker.env_http_headers]
X-Key = "TRACKER_KEY"

[mcp_servers.tracker.http_headers]
X-Region = "eu"
`} {
		if !strings.Contains(config, want) {
			t.Errorf("config.toml lacks\n%s\ngot:\n%s", want, config)
		}
	}
	for _, bad := range []string{"tools", "type =", "${"} {
		if strings.Contains(config, bad) {
			t.Errorf("config.toml must not contain %q, Codex does not know it:\n%s", bad, config)
		}
	}

	hooks := read(t, filepath.Join(dir, "hooks.json"))
	for _, want := range []string{`"description": "mine"`, "echo mine", `"PreToolUse"`, `"matcher": "Bash"`, "check-push # spellbook:acme/no-force-push", `"timeout": 10`, `"PostToolUse"`, "format.sh\\\" # spellbook:acme/format"} {
		if !strings.Contains(hooks, want) {
			t.Errorf("hooks.json lacks %q:\n%s", want, hooks)
		}
	}
	if strings.Contains(hooks, "Notification") || strings.Contains(hooks, "version") {
		t.Errorf("hooks.json holds something Codex rejects:\n%s", hooks)
	}
}

func TestCodexGetsEveryKindAtHome(t *testing.T) {
	e := newEnv(t)
	repo := e.codexRepo()
	dir := filepath.Join(e.home, ".codex")
	skills := filepath.Join(e.home, ".agents", "skills")
	write(t, filepath.Join(dir, "config.toml"), codexConfig)
	write(t, filepath.Join(dir, "hooks.json"), codexHooks)
	write(t, filepath.Join(dir, "AGENTS.md"), codexAgents)

	e.mustRun("init", "--target", "codex")
	e.mustRun("registry", "add", "acme", repo)
	out := e.mustRun("registry", "allow", "acme", "mcp", "hooks")
	// Skill, block and agent came with the registry; now two servers, two
	// hooks and the scripts of one of them.
	if !strings.Contains(out, "5 installed, 0 updated, 3 unchanged, 0 removed, 0 skipped") {
		t.Errorf("unexpected summary:\n%s", out)
	}
	for _, want := range []string{"codex: skipped 1 instructions", "codex: skipped 1 prompts", "codex: skipped 1 hooks"} {
		if !strings.Contains(out, want) {
			t.Errorf("the user is not told %q:\n%s", want, out)
		}
	}
	checkCodex(t, dir, skills, filepath.Join(dir, "AGENTS.md"))
	if hooks := read(t, filepath.Join(dir, "hooks.json")); !strings.Contains(hooks, filepath.Join(e.home, ".config", "spellbook", "hooks", "format", "format.sh")) {
		t.Errorf("the hook should run its script from the config directory:\n%s", hooks)
	}

	// A second sync changes nothing and rewrites no file.
	files := []string{"config.toml", "hooks.json", "AGENTS.md", "agents/reviewer.toml"}
	before := map[string]os.FileInfo{}
	for _, f := range files {
		before[f], _ = os.Stat(filepath.Join(dir, f))
	}
	if out := e.mustRun("sync"); !strings.Contains(out, "0 installed, 0 updated, 8 unchanged, 0 removed, 0 skipped") {
		t.Errorf("the second sync is not a no-op:\n%s", out)
	}
	for _, f := range files {
		if after, _ := os.Stat(filepath.Join(dir, f)); !os.SameFile(before[f], after) || !after.ModTime().Equal(before[f].ModTime()) {
			t.Errorf("%s was rewritten by a sync that changed nothing", f)
		}
	}
	if out := e.mustRun("doctor"); strings.Contains(out, "edited") || strings.Contains(out, "missing") {
		t.Errorf("doctor finds problems after a clean sync:\n%s", out)
	}

	// Updates arrive, and the table keeps its place in the file.
	write(t, filepath.Join(repo, "mcp", "docs.json"), `{"command": "npx", "args": ["-y", "@acme/docs-mcp@2"]}`)
	write(t, filepath.Join(repo, "instructions", "standards.md"), "Use tabs. v2\n")
	e.commit(repo, "v2")
	if out := e.mustRun("sync"); !strings.Contains(out, "0 installed, 2 updated, 6 unchanged") {
		t.Errorf("unexpected summary after an update:\n%s", out)
	}
	config := read(t, filepath.Join(dir, "config.toml"))
	if !strings.Contains(config, "[mcp_servers.docs]\nargs = [\"-y\", \"@acme/docs-mcp@2\"]\ncommand = \"npx\"\n\n#") || strings.Contains(config, "MODE") {
		t.Errorf("the server was not replaced in place:\n%s", config)
	}
	if d, tr := strings.Index(config, "[mcp_servers.docs]"), strings.Index(config, "[mcp_servers.tracker]"); d > tr {
		t.Errorf("the order of the tables changed:\n%s", config)
	}

	// Removing the registry gives the user's files back as they were.
	e.mustRun("registry", "remove", "acme")
	for file, want := range map[string]string{"config.toml": codexConfig, "AGENTS.md": codexAgents} {
		if got := read(t, filepath.Join(dir, file)); got != want {
			t.Errorf("%s after removal:\n%s\nwant:\n%s", file, got, want)
		}
	}
	if hooks := read(t, filepath.Join(dir, "hooks.json")); strings.Contains(hooks, "spellbook") || !strings.Contains(hooks, "echo mine") || !strings.Contains(hooks, `"description": "mine"`) {
		t.Errorf("hooks.json after removal:\n%s", hooks)
	}
	if exists(filepath.Join(dir, "agents", "reviewer.toml")) || exists(filepath.Join(skills, "review")) {
		t.Error("agent and skill should be removed")
	}
}

func TestCodexGetsEveryKindInAProject(t *testing.T) {
	e := newEnv(t)
	repo := e.codexRepo()
	project := t.TempDir()
	t.Chdir(project)
	dir := filepath.Join(project, ".codex")
	write(t, filepath.Join(dir, "config.toml"), codexConfig)
	write(t, filepath.Join(dir, "hooks.json"), codexHooks)
	write(t, filepath.Join(project, "AGENTS.md"), codexAgents)

	e.mustRun("init", "--project", "--target", "codex")
	e.mustRun("registry", "add", "acme", repo, "--project")
	out := e.mustRun("registry", "allow", "acme", "mcp", "hooks", "--project")
	if !strings.Contains(out, "5 installed, 0 updated, 3 unchanged") {
		t.Errorf("unexpected summary:\n%s", out)
	}
	checkCodex(t, dir, filepath.Join(project, ".agents", "skills"), filepath.Join(project, "AGENTS.md"))

	// Codex starts a hook in the directory of the session, so the script
	// is found from the root of the repository.
	hooks := read(t, filepath.Join(dir, "hooks.json"))
	if !strings.Contains(hooks, "git rev-parse --show-toplevel") || !strings.Contains(hooks, "/.spellbook/hooks/format/format.sh") || strings.Contains(hooks, "CLAUDE_PROJECT_DIR") {
		t.Errorf("the hook does not find its script in the project:\n%s", hooks)
	}
	if !exists(filepath.Join(project, ".spellbook", "hooks", "format", "format.sh")) {
		t.Error("the script of the hook is missing")
	}
	if state := read(t, filepath.Join(project, ".spellbook", "state.json")); strings.Contains(state, project) || !strings.Contains(state, `".codex/config.toml#docs"`) {
		t.Errorf("the project state is not portable:\n%s", state)
	}
	if exists(filepath.Join(e.home, ".codex")) || exists(filepath.Join(e.home, ".agents")) {
		t.Error("a project sync wrote into the home directory")
	}

	if out := e.mustRun("sync", "--project"); !strings.Contains(out, "0 installed, 0 updated, 8 unchanged, 0 removed, 0 skipped") {
		t.Errorf("the second sync is not a no-op:\n%s", out)
	}
	e.mustRun("registry", "remove", "acme", "--project")
	if got := read(t, filepath.Join(dir, "config.toml")); got != codexConfig {
		t.Errorf("config.toml after removal:\n%s", got)
	}
	if got := read(t, filepath.Join(project, "AGENTS.md")); got != codexAgents {
		t.Errorf("AGENTS.md after removal:\n%s", got)
	}
}

func TestCodexFilesThatHeldOnlySpellbookContentAreRemoved(t *testing.T) {
	e := newEnv(t)
	repo := e.codexRepo()
	dir := filepath.Join(e.home, ".codex")
	e.mustRun("init", "--target", "codex")
	e.mustRun("registry", "add", "acme", repo, "--allow", "mcp", "--allow", "hooks")
	for _, f := range []string{"config.toml", "hooks.json", "AGENTS.md"} {
		if !exists(filepath.Join(dir, f)) {
			t.Fatalf("%s was not created", f)
		}
	}
	e.mustRun("registry", "remove", "acme")
	for _, f := range []string{"config.toml", "hooks.json", "AGENTS.md"} {
		if exists(filepath.Join(dir, f)) {
			t.Errorf("%s is left behind:\n%s", f, read(t, filepath.Join(dir, f)))
		}
	}
}

func TestCodexLeavesTheUsersServersAndLocalEditsAlone(t *testing.T) {
	e := newEnv(t)
	repo := e.codexRepo()
	config := filepath.Join(e.home, ".codex", "config.toml")
	// The user has a server called "docs" already, written inline.
	write(t, config, "[mcp_servers]\ndocs = { command = \"mine\" }\n")
	e.mustRun("init", "--target", "codex")
	e.mustRun("registry", "add", "acme", repo)
	out := e.mustRun("registry", "allow", "acme", "mcp")
	if !strings.Contains(out, `skipped MCP server "docs"`) || !strings.Contains(out, "does not manage") {
		t.Errorf("the user's server should be reported:\n%s", out)
	}
	got := read(t, config)
	if !strings.Contains(got, "docs = { command = \"mine\" }") || !strings.Contains(got, "[mcp_servers.tracker]") {
		t.Errorf("the user's server was touched, or the other one is missing:\n%s", got)
	}

	// A managed server that was edited is neither updated nor removed.
	edited := strings.Replace(got, "https://mcp.example.com/mcp", "https://staging.example.com/mcp", 1)
	write(t, config, edited)
	write(t, filepath.Join(repo, "mcp", "tracker.json"), `{"url": "https://mcp.example.com/v2"}`)
	e.commit(repo, "v2")
	if out := e.mustRun("sync"); !strings.Contains(out, `skipped MCP server "tracker": it was edited`) {
		t.Errorf("the edit should be reported:\n%s", out)
	}
	if code, out := e.run("doctor"); code == 0 && !strings.Contains(out, "tracker") {
		t.Errorf("doctor should notice the edited server:\n%s", out)
	}
	if err := os.Remove(filepath.Join(repo, "mcp", "tracker.json")); err != nil {
		t.Fatal(err)
	}
	e.commit(repo, "gone")
	if out := e.mustRun("sync"); !strings.Contains(out, `kept MCP server "tracker"`) {
		t.Errorf("the edited server should be kept:\n%s", out)
	}
	if got := read(t, config); got != edited {
		t.Errorf("config.toml changed although the server was edited:\n%s", got)
	}
	e.mustRun("sync", "--force")
	if got := read(t, config); got != "[mcp_servers]\ndocs = { command = \"mine\" }\n" {
		t.Errorf("--force should remove the managed server and nothing else:\n%s", got)
	}

	// A file that is not valid TOML is not touched.
	write(t, config, "model = \n")
	write(t, filepath.Join(repo, "mcp", "tracker.json"), `{"url": "https://mcp.example.com/v3"}`)
	e.commit(repo, "back")
	if code, out := e.run("sync"); code == 0 || !strings.Contains(out, "not valid TOML") {
		t.Errorf("a broken config.toml should stop the sync (exit %d):\n%s", code, out)
	}
	if got := read(t, config); got != "model = \n" {
		t.Errorf("a broken config.toml was changed:\n%s", got)
	}
}

func TestCodexAutosyncHook(t *testing.T) {
	e := newEnv(t)
	hooks := filepath.Join(e.home, ".codex", "hooks.json")
	write(t, hooks, codexHooks)
	e.mustRun("init", "--target", "codex")
	if out := e.mustRun("autosync", "enable"); !strings.Contains(out, hooks) {
		t.Errorf("enable should name the file:\n%s", out)
	}
	got := read(t, hooks)
	for _, want := range []string{`"SessionStart"`, `"matcher": "startup"`, `"command": "spellbook sync --auto"`, `"async": true`, "echo mine", `"description": "mine"`} {
		if !strings.Contains(got, want) {
			t.Errorf("hooks.json lacks %q:\n%s", want, got)
		}
	}
	if out := e.mustRun("autosync", "status"); !strings.Contains(out, "hook installed") {
		t.Errorf("status should show the hook:\n%s", out)
	}
	e.mustRun("autosync", "disable")
	if got := read(t, hooks); strings.Contains(got, "spellbook") || !strings.Contains(got, "echo mine") {
		t.Errorf("disable should remove only its own hook:\n%s", got)
	}
}
