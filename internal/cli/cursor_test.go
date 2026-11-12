package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cursorRepo is a registry with one resource of every kind.
func (e *env) cursorRepo() string {
	e.t.Helper()
	repo := e.repo(map[string]string{"skills/review": "v1"})
	write(e.t, filepath.Join(repo, "instructions", "standards.md"), "Use tabs. v1\n")
	write(e.t, filepath.Join(repo, "instructions", "tf-style.md"), "---\npaths:\n  - \"infra/**/*.tf\"\n  - \"modules/**\"\n---\nPin provider versions.\n")
	write(e.t, filepath.Join(repo, "agents", "reviewer.md"), `---
description: Reviews pull requests
custom-field: kept
claude:
  model: opus
cursor:
  model: inherit
  readonly: true
---

You review code. v1
`)
	write(e.t, filepath.Join(repo, "prompts", "release-notes.md"), "---\ndescription: Draft release notes\n---\n\nWrite release notes.\n")
	write(e.t, filepath.Join(repo, "mcp", "docs.json"), `{
  "command": "npx",
  "args": ["-y", "@acme/docs-mcp"],
  "copilot": {"tools": ["search"]},
  "cursor": {"envFile": ".env"}
}`)
	write(e.t, filepath.Join(repo, "hooks", "no-force-push.json"), `{
  "event": "PreToolUse",
  "matcher": "Shell",
  "command": "grep -q 'push --force' && exit 2 || exit 0",
  "timeout": 10
}`)
	write(e.t, filepath.Join(repo, "hooks", "format", "hook.json"), `{"event": "UserPromptSubmit", "command": "\"${HOOK_DIR}/format.sh\""}`)
	write(e.t, filepath.Join(repo, "hooks", "format", "format.sh"), "#!/bin/sh\necho v1\n")
	write(e.t, filepath.Join(repo, "hooks", "notify.json"), `{"event": "Notification", "command": "true"}`)
	e.commit(repo, "every kind")
	return repo
}

// cursorHooks returns the commands in a Cursor hooks file by event.
func cursorHooks(t *testing.T, path string) map[string][]map[string]any {
	t.Helper()
	var file struct {
		Version int                         `json:"version"`
		Hooks   map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, path)), &file); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if file.Version != 1 {
		t.Errorf("%s should carry version 1:\n%s", path, read(t, path))
	}
	return file.Hooks
}

// checkCursorCommon checks what is the same at home and in a project.
func checkCursorCommon(t *testing.T, root, scripts string) {
	t.Helper()
	cursor := filepath.Join(root, ".cursor")
	if got := read(t, filepath.Join(cursor, "skills", "review", "SKILL.md")); !strings.Contains(got, "name: review") {
		t.Errorf("skill is wrong:\n%s", got)
	}

	agent := read(t, filepath.Join(cursor, "agents", "reviewer.md"))
	for _, want := range []string{"name: reviewer", "description: Reviews pull requests", "custom-field: kept", "model: inherit", "readonly: true", "You review code. v1"} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent lacks %q:\n%s", want, agent)
		}
	}
	if strings.Contains(agent, "opus") || strings.Contains(agent, "cursor:") {
		t.Errorf("agent holds fields of another tool:\n%s", agent)
	}

	prompt := read(t, filepath.Join(cursor, "skills", "release-notes", "SKILL.md"))
	for _, want := range []string{"name: release-notes", "disable-model-invocation: true", "Write release notes."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt skill lacks %q:\n%s", want, prompt)
		}
	}

	var mcp struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(cursor, "mcp.json"))), &mcp); err != nil {
		t.Fatal(err)
	}
	docs := mcp.Servers["docs"]
	if docs["command"] != "npx" || docs["envFile"] != ".env" || docs["tools"] != nil || docs["cursor"] != nil {
		t.Errorf("MCP server is wrong: %v", docs)
	}
	if mcp.Servers["mine"]["command"] != "mine" {
		t.Errorf("the user's own server was lost: %v", mcp.Servers)
	}

	hooks := cursorHooks(t, filepath.Join(cursor, "hooks.json"))
	pre := hooks["preToolUse"]
	if len(pre) != 2 || pre[0]["command"] != "echo mine" {
		t.Fatalf("preToolUse should hold the user's hook and ours: %v", pre)
	}
	if pre[1]["matcher"] != "Shell" || pre[1]["timeout"] != float64(10) || !strings.HasSuffix(pre[1]["command"].(string), " # spellbook:acme/no-force-push") {
		t.Errorf("hook entry is wrong: %v", pre[1])
	}
	if _, nested := pre[1]["hooks"]; nested || pre[1]["type"] != nil {
		t.Errorf("Cursor entries are flat: %v", pre[1])
	}
	submit := hooks["beforeSubmitPrompt"]
	if len(submit) != 1 || !strings.Contains(submit[0]["command"].(string), scripts+"/format.sh") {
		t.Errorf("UserPromptSubmit should become beforeSubmitPrompt and name its script: %v", hooks)
	}
	for event := range hooks {
		if event != "preToolUse" && event != "beforeSubmitPrompt" {
			t.Errorf("unexpected event %q", event)
		}
	}
}

const cursorOwnFiles = `{"mcpServers": {"mine": {"command": "mine"}}}` + "\n"

const cursorOwnHooks = `{
  "version": 1,
  "hooks": {
    "preToolUse": [{"command": "echo mine"}]
  }
}
`

func TestCursorAtHomeGetsEveryKindItSupports(t *testing.T) {
	e := newEnv(t)
	repo := e.cursorRepo()
	cursor := filepath.Join(e.home, ".cursor")
	write(t, filepath.Join(cursor, "mcp.json"), cursorOwnFiles)
	write(t, filepath.Join(cursor, "hooks.json"), cursorOwnHooks)
	e.mustRun("init", "--target", "cursor")
	out := e.mustRun("registry", "add", "acme", repo, "--allow", "mcp", "--allow", "hooks")

	checkCursorCommon(t, e.home, filepath.Join(e.home, ".config", "spellbook", "hooks", "format"))
	// User rules live in Cursor's settings, not in files.
	if exists(filepath.Join(cursor, "rules")) {
		t.Error("no rule files may be written at home")
	}
	for _, want := range []string{"cursor: skipped 2 instructions", `cursor: skipped hook "notify" of acme: the tool has no Notification event`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// A second sync changes nothing and rewrites no file.
	files := []string{"mcp.json", "hooks.json", "agents/reviewer.md", "skills/review/SKILL.md"}
	before := map[string]os.FileInfo{}
	for _, f := range files {
		info, err := os.Stat(filepath.Join(cursor, f))
		if err != nil {
			t.Fatal(err)
		}
		before[f] = info
	}
	out = e.mustRun("sync")
	if !strings.Contains(out, "0 installed, 0 updated, 7 unchanged, 0 removed, 0 skipped") {
		t.Errorf("the second sync should change nothing:\n%s", out)
	}
	for _, f := range files {
		info, _ := os.Stat(filepath.Join(cursor, f))
		if !os.SameFile(before[f], info) || !info.ModTime().Equal(before[f].ModTime()) {
			t.Errorf("%s was rewritten by the second sync", f)
		}
	}

	// Removing the registry removes what it brought and nothing else.
	e.mustRun("registry", "remove", "acme")
	for _, gone := range []string{"skills", "agents"} {
		if got := installed(t, filepath.Join(cursor, gone)); got != "" {
			t.Errorf("%s still holds %s", gone, got)
		}
	}
	if got := read(t, filepath.Join(cursor, "mcp.json")); strings.Contains(got, "docs") || !strings.Contains(got, `"mine"`) {
		t.Errorf("mcp.json after removal:\n%s", got)
	}
	if got := read(t, filepath.Join(cursor, "hooks.json")); strings.Contains(got, "spellbook") || !strings.Contains(got, "echo mine") || !strings.Contains(got, `"version"`) {
		t.Errorf("hooks.json after removal:\n%s", got)
	}
}

func TestCursorInAProjectGetsEveryKind(t *testing.T) {
	e := newEnv(t)
	repo := e.cursorRepo()
	project := t.TempDir()
	t.Chdir(project)
	cursor := filepath.Join(project, ".cursor")
	write(t, filepath.Join(cursor, "mcp.json"), cursorOwnFiles)
	write(t, filepath.Join(cursor, "hooks.json"), cursorOwnHooks)
	e.mustRun("init", "--project", "--target", "cursor")
	e.mustRun("registry", "add", "acme", repo, "-p", "--allow", "mcp", "--allow", "hooks")

	checkCursorCommon(t, project, "${CLAUDE_PROJECT_DIR:-.}/.spellbook/hooks/format")
	always := read(t, filepath.Join(cursor, "rules", "standards.mdc"))
	if !strings.HasPrefix(always, "---\nalwaysApply: true\n---\n") || !strings.Contains(always, "Use tabs. v1") {
		t.Errorf("always-on rule is wrong:\n%s", always)
	}
	scoped := read(t, filepath.Join(cursor, "rules", "tf-style.mdc"))
	if !strings.HasPrefix(scoped, "---\nglobs: infra/**/*.tf,modules/**\nalwaysApply: false\n---\n") || !strings.Contains(scoped, "Pin provider versions.") {
		t.Errorf("scoped rule is wrong:\n%s", scoped)
	}
	if exists(filepath.Join(e.home, ".cursor")) {
		t.Error("project sync wrote into the home directory")
	}
	if st := read(t, filepath.Join(project, ".spellbook", "state.json")); strings.Contains(st, project) {
		t.Errorf("project state holds absolute paths:\n%s", st)
	}

	if out := e.mustRun("sync", "-p"); !strings.Contains(out, "0 installed, 0 updated, 9 unchanged, 0 removed, 0 skipped") {
		t.Errorf("the second sync should change nothing:\n%s", out)
	}

	// A rule the user edited is kept; everything else goes with the registry.
	write(t, filepath.Join(cursor, "rules", "standards.mdc"), "mine now\n")
	e.mustRun("registry", "remove", "acme", "-p")
	if got := installed(t, filepath.Join(cursor, "rules")); got != "standards.mdc" {
		t.Errorf("rules after removal: %q", got)
	}
	if got := read(t, filepath.Join(cursor, "hooks.json")); strings.Contains(got, "spellbook") || !strings.Contains(got, "echo mine") {
		t.Errorf("hooks.json after removal:\n%s", got)
	}
	if exists(filepath.Join(project, ".spellbook", "hooks", "format")) {
		t.Error("the hook's scripts should be removed")
	}
}

func TestCursorFilesThatHeldOnlyOursAreRemovedWithThem(t *testing.T) {
	e := newEnv(t)
	repo := e.cursorRepo()
	e.mustRun("init", "--target", "cursor")
	e.mustRun("registry", "add", "acme", repo, "--allow", "mcp", "--allow", "hooks")
	cursor := filepath.Join(e.home, ".cursor")
	cursorHooks(t, filepath.Join(cursor, "hooks.json"))
	e.mustRun("registry", "remove", "acme")
	for _, f := range []string{"hooks.json", "mcp.json"} {
		if exists(filepath.Join(cursor, f)) {
			t.Errorf("%s should be gone:\n%s", f, read(t, filepath.Join(cursor, f)))
		}
	}
}

func TestCursorDoesNotRepeatHooksClaudeCodeHolds(t *testing.T) {
	e := newEnv(t)
	repo := e.cursorRepo()
	write(t, filepath.Join(repo, "hooks", "cursor-only.json"), `{"event": "Stop", "command": "true", "tools": ["cursor"]}`)
	e.commit(repo, "cursor only")
	e.mustRun("init", "--target", "claude", "--target", "cursor")
	e.mustRun("registry", "add", "acme", repo, "--allow", "hooks")

	claude := read(t, filepath.Join(e.home, ".claude", "settings.json"))
	if !strings.Contains(claude, "acme/no-force-push") || strings.Contains(claude, "cursor-only") {
		t.Errorf("Claude settings are wrong:\n%s", claude)
	}
	// Cursor runs the hooks in Claude Code's settings as well.
	hooks := cursorHooks(t, filepath.Join(e.home, ".cursor", "hooks.json"))
	if len(hooks) != 1 || len(hooks["stop"]) != 1 {
		t.Errorf("Cursor's file should hold only the hook that is not in Claude's settings: %v", hooks)
	}
}

func TestAutosyncForCursorKeepsTheUsersHooks(t *testing.T) {
	e := newEnv(t)
	file := filepath.Join(e.home, ".cursor", "hooks.json")
	write(t, file, `{
  "hooks": {
    "stop": [{"command": "echo bye"}],
    "sessionStart": [{"command": "echo hi"}]
  },
  "other": true
}
`)
	e.mustRun("init", "--target", "cursor")
	e.mustRun("autosync", "enable")
	e.mustRun("autosync", "enable")
	got := read(t, file)
	if strings.Count(got, "spellbook sync --auto") != 1 {
		t.Errorf("the hook should be there once:\n%s", got)
	}
	start := cursorHooks(t, file)["sessionStart"]
	if len(start) != 2 || start[0]["command"] != "echo hi" || start[1]["command"] != "spellbook sync --auto" {
		t.Errorf("sessionStart is wrong: %v", start)
	}
	if s, o := strings.Index(got, `"stop"`), strings.Index(got, `"other"`); !(s < strings.Index(got, `"sessionStart"`) && s < o) {
		t.Errorf("the order of the keys changed:\n%s", got)
	}
	if out := e.mustRun("autosync", "status"); !strings.Contains(out, "cursor   hook installed") {
		t.Errorf("status:\n%s", out)
	}

	e.mustRun("autosync", "disable")
	got = read(t, file)
	if strings.Contains(got, "spellbook") || !strings.Contains(got, "echo hi") || !strings.Contains(got, "echo bye") || !strings.Contains(got, `"other"`) {
		t.Errorf("disable should remove only its hook:\n%s", got)
	}

	// A file that is not JSON is not touched.
	write(t, file, "{ not json")
	if code, _ := e.run("autosync", "enable"); code == 0 {
		t.Error("enable should fail on a broken file")
	}
	if read(t, file) != "{ not json" {
		t.Error("the broken file was changed")
	}

	// In a project the command syncs the project, and a file that held
	// nothing else goes away again.
	project := t.TempDir()
	t.Chdir(project)
	e.mustRun("init", "--project", "--target", "cursor")
	e.mustRun("autosync", "enable", "-p")
	pfile := filepath.Join(project, ".cursor", "hooks.json")
	if start := cursorHooks(t, pfile)["sessionStart"]; len(start) != 1 || start[0]["command"] != "spellbook sync --auto --project" {
		t.Errorf("project hook is wrong: %v", start)
	}
	e.mustRun("autosync", "disable", "-p")
	if exists(pfile) {
		t.Errorf("the hooks file should be gone:\n%s", read(t, pfile))
	}
}
