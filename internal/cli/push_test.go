package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graemer-org/spellbook/internal/push"
)

// pushEnv is a project with one registry subscribed, and a git identity so
// that push can commit.
func pushEnv(t *testing.T, manifest string) (e *env, repo, project string) {
	t.Helper()
	e = newEnv(t)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "test", "GIT_AUTHOR_EMAIL": "test@example.com", "GIT_COMMITTER_NAME": "test", "GIT_COMMITTER_EMAIL": "test@example.com"} {
		t.Setenv(k, v)
	}
	repo = e.repo(map[string]string{"skills/review": "v1"})
	if manifest != "" {
		write(t, filepath.Join(repo, "registry.toml"), manifest)
		e.commit(repo, "manifest")
	}
	project = t.TempDir()
	t.Chdir(project)
	e.mustRun("init", "--project", "--target", "claude", "--target", "copilot")
	e.mustRun("registry", "add", "acme", repo, "-p")
	return e, repo, project
}

// show returns a file of the registry as it is on a branch.
func show(t *testing.T, repo, branch, file string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "show", branch+":"+file).CombinedOutput()
	if err != nil {
		t.Fatalf("git show %s:%s: %v\n%s", branch, file, err, out)
	}
	return string(out)
}

func TestPushSkillThenSyncTakesTheRegistrysVersion(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	skill := filepath.Join(project, ".claude", "skills", "db-migration")
	write(t, filepath.Join(skill, "SKILL.md"), "---\nname: db-migration\ndescription: Plan a database migration\n---\nSteps. v1\n")
	write(t, filepath.Join(skill, "scripts", "check.sh"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(skill, "scripts", "check.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	if out := e.mustRun("push", "-p"); !strings.Contains(out, "db-migration") || strings.Contains(out, "review") {
		t.Errorf("push without arguments should list what is local and leave out what came from a registry:\n%s", out)
	}

	out := e.mustRun("push", ".claude/skills/db-migration", "-p", "--yes")
	if !strings.Contains(out, "skill db-migration (2 files") || !strings.Contains(out, "-> new in acme: skills/db-migration") || !strings.Contains(out, "spellbook/add-db-migration") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if got := show(t, repo, "spellbook/add-db-migration", "skills/db-migration/SKILL.md"); !strings.Contains(got, "Steps. v1") {
		t.Errorf("the branch does not hold the skill:\n%s", got)
	}
	mode, _ := exec.Command("git", "-C", repo, "ls-tree", "spellbook/add-db-migration", "skills/db-migration/scripts/check.sh").Output()
	if !strings.HasPrefix(string(mode), "100755") {
		t.Errorf("the script lost its executable bit: %s", mode)
	}
	if got := show(t, repo, "main", "skills/review/SKILL.md"); got == "" {
		t.Error("main must be untouched")
	}

	// Before the merge: the local copy stays and is not reported as foreign.
	out = e.mustRun("sync", "-p")
	if !exists(filepath.Join(skill, "SKILL.md")) || strings.Contains(out, "not managed") {
		t.Fatalf("the pushed skill must survive a sync before the merge:\n%s", out)
	}
	if out := e.mustRun("list", "-p"); !strings.Contains(out, "pushed") {
		t.Errorf("list should mark the pushed skill:\n%s", out)
	}

	// A reviewer changes it and merges.
	e.git(repo, "merge", "--quiet", "spellbook/add-db-migration")
	write(t, filepath.Join(repo, "skills", "db-migration", "SKILL.md"), "---\nname: db-migration\ndescription: Plan a database migration\n---\nSteps. reviewed\n")
	e.commit(repo, "review")
	e.mustRun("sync", "-p")
	if got := read(t, filepath.Join(skill, "SKILL.md")); !strings.Contains(got, "reviewed") {
		t.Errorf("after the merge the sync should bring the registry's version: %q", got)
	}
	if !exists(filepath.Join(project, ".github", "skills", "db-migration", "SKILL.md")) {
		t.Error("after the merge the skill should reach the other tool too")
	}
	if out := e.mustRun("list", "-p"); strings.Contains(out, "pushed") {
		t.Errorf("after the merge nothing is pending any more:\n%s", out)
	}
}

func TestPushedSkillEditedAfterwardsIsNotOverwritten(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	skill := filepath.Join(project, ".claude", "skills", "notes", "SKILL.md")
	write(t, skill, "---\nname: notes\ndescription: Take notes\n---\nv1\n")
	e.mustRun("push", "skill", "notes", "-p", "--yes")
	write(t, skill, "---\nname: notes\ndescription: Take notes\n---\nmy later edit\n")
	e.git(repo, "merge", "--quiet", "spellbook/add-notes")
	out := e.mustRun("sync", "-p")
	if !strings.Contains(read(t, skill), "my later edit") || !strings.Contains(out, "edited locally") {
		t.Errorf("an edit made after the push must be kept:\n%s", out)
	}
}

func TestPushTranslatesInstructionsAgentsAndPrompts(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	write(t, filepath.Join(project, ".github", "instructions", "csharp.instructions.md"), "---\napplyTo: \"**/*.cs,**/*.csproj\"\n---\nUse file-scoped namespaces.\n")
	write(t, filepath.Join(project, ".claude", "rules", "tone.md"), "Be brief.\n")
	write(t, filepath.Join(project, ".claude", "agents", "planner.md"), "---\nname: planner\ndescription: Plans work\ntools: Read, Grep\nmodel: opus\n---\nYou plan.\n")
	write(t, filepath.Join(project, ".github", "prompts", "standup.prompt.md"), "---\ndescription: Write a standup\nmode: agent\n---\nWrite my standup.\n")

	out := e.mustRun("push", "-p", "--yes",
		".github/instructions/csharp.instructions.md", ".claude/rules/tone.md", "agent", "planner", "prompt", "standup")
	if !strings.Contains(out, "spellbook/add-csharp-and-3-more") {
		t.Errorf("several resources should share one branch:\n%s", out)
	}
	branch := "spellbook/add-csharp-and-3-more"

	ins := show(t, repo, branch, "instructions/csharp.md")
	if !strings.Contains(ins, "paths:") || !strings.Contains(ins, `- "**/*.cs"`) || strings.Contains(ins, "applyTo") {
		t.Errorf("Copilot's applyTo should become paths:\n%s", ins)
	}
	if got := show(t, repo, branch, "instructions/tone.md"); got != "Be brief.\n" {
		t.Errorf("an always-on rule should be pushed as plain text: %q", got)
	}
	agent := show(t, repo, branch, "agents/planner.md")
	if strings.Contains(agent, "name: planner") || !strings.Contains(agent, "description: Plans work") || !strings.Contains(agent, "tools: Read, Grep") || !strings.Contains(agent, "claude:\n    model: opus") {
		t.Errorf("the agent should drop its name and keep the model for Claude Code only:\n%s", agent)
	}
	if !strings.Contains(out, "the field model now only applies to claude") {
		t.Errorf("the user should be told what was moved:\n%s", out)
	}
	prompt := show(t, repo, branch, "prompts/standup.md")
	if !strings.Contains(prompt, "description: Write a standup") || !strings.Contains(prompt, "copilot:\n    mode: agent") || !strings.Contains(prompt, "Write my standup.") {
		t.Errorf("the prompt is wrong:\n%s", prompt)
	}

	// What was pushed can be synced back as it is.
	e.git(repo, "merge", "--quiet", branch)
	out = e.mustRun("sync", "-p")
	if strings.Contains(out, "warning") {
		t.Errorf("the pushed resources should sync back without warnings:\n%s", out)
	}
	if got := read(t, filepath.Join(project, ".claude", "agents", "planner.md")); !strings.Contains(got, "name: planner") || !strings.Contains(got, "model: opus") {
		t.Errorf("round trip changed the agent for Claude Code:\n%s", got)
	}
}

func TestPushRefusesWhatItShouldNotSend(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	write(t, filepath.Join(project, ".claude", "skills", "review-copy", "SKILL.md"), "---\nname: review-copy\ndescription: x\n---\nv1\n")
	write(t, filepath.Join(project, ".claude", "skills", "nodesc", "SKILL.md"), "no frontmatter\n")
	write(t, filepath.Join(project, "README.md"), "not a resource\n")

	for name, args := range map[string][]string{
		"a resource that came from the registry": {"push", "skill", "review", "-p", "--yes"},
		"a skill without description":            {"push", "skill", "nodesc", "-p", "--yes"},
		"a file that is no resource":             {"push", "README.md", "-p", "--yes"},
		"a path that does not exist":             {"push", "nope", "-p", "--yes"},
		"an unknown registry":                    {"push", "skill", "review-copy", "-p", "--yes", "--to", "ghost"},
		"an unknown group":                       {"push", "skill", "review-copy", "-p", "--yes", "--group", "nope"},
		"no confirmation":                        {"push", "skill", "review-copy", "-p"},
	} {
		if code, out := e.run(args...); code == 0 {
			t.Errorf("%s should be refused:\n%s", name, out)
		}
	}
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "spellbook/*").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("a refused push created a branch: %s", out)
	}
	if out := e.mustRun("list", "-p"); strings.Contains(out, "pushed") {
		t.Errorf("a refused push marked something as pushed:\n%s", out)
	}
}

func TestPushDryRunAndInto(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	write(t, filepath.Join(project, ".claude", "skills", "notes", "SKILL.md"), "---\nname: notes\ndescription: Take notes\n---\nv1\n")

	out := e.mustRun("push", "skill", "notes", "-p", "--dry-run")
	if !strings.Contains(out, "Dry run: nothing was sent.") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if b, _ := exec.Command("git", "-C", repo, "branch", "--list", "spellbook/*").Output(); strings.TrimSpace(string(b)) != "" {
		t.Errorf("a dry run pushed a branch: %s", b)
	}

	clone := t.TempDir()
	e.mustRun("push", "skill", "notes", "-p", "--into", clone)
	if got := read(t, filepath.Join(clone, "skills", "notes", "SKILL.md")); !strings.Contains(got, "v1") {
		t.Errorf("--into did not write the skill: %q", got)
	}
	if b, _ := exec.Command("git", "-C", repo, "branch", "--list", "spellbook/*").Output(); strings.TrimSpace(string(b)) != "" {
		t.Errorf("--into pushed a branch: %s", b)
	}
}

func TestPushAddsToAGroup(t *testing.T) {
	manifest := `# Our registry.
version = 1
default_groups = ["base"]

[groups.base]
description = "Everyone"   # keep this comment
skills = ["review"]

[groups.platform]
include = ["base"]
skills = [
  "review",
]

[groups.empty]
description = "Nothing yet"
`
	e, repo, project := pushEnv(t, manifest)
	for _, name := range []string{"one", "two", "three"} {
		write(t, filepath.Join(project, ".claude", "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: x\n---\nv1\n")
	}
	write(t, filepath.Join(project, ".claude", "agents", "planner.md"), "---\ndescription: Plans\n---\nYou plan.\n")

	out := e.mustRun("push", "skill", "one", "-p", "--yes")
	if !strings.Contains(out, "sorts its resources into groups (base, empty, platform)") {
		t.Errorf("without --group the user should be told about groups:\n%s", out)
	}

	e.mustRun("push", "skill", "two", "agent", "planner", "-p", "--yes", "--group", "base", "--branch", "b1")
	got := show(t, repo, "b1", "registry.toml")
	if !strings.Contains(got, `skills = ["review", "two"]`) || !strings.Contains(got, `agents = ["planner"]`) || !strings.Contains(got, "# keep this comment") || !strings.Contains(got, "# Our registry.") {
		t.Errorf("the group was not extended, or the file lost its comments:\n%s", got)
	}

	e.mustRun("push", "skill", "three", "-p", "--yes", "--group", "platform", "--branch", "b2")
	if got := show(t, repo, "b2", "registry.toml"); !strings.Contains(got, "\"review\",\n  \"three\",\n]") {
		t.Errorf("a list over several lines was not extended in its style:\n%s", got)
	}

	// The result is a registry that still works, with the group in effect.
	e.git(repo, "merge", "--quiet", "b1")
	if out := e.mustRun("sync", "-p"); strings.Contains(out, "error") || !exists(filepath.Join(project, ".github", "skills", "two")) {
		t.Errorf("after merging, the default group should deliver the new skill:\n%s", out)
	}
}

func TestPullRequestURL(t *testing.T) {
	for repo, want := range map[string]string{
		"git@github.com:acme/ai-registry.git":        "https://github.com/acme/ai-registry/compare/spellbook/add-x?expand=1",
		"https://github.com/acme/ai-registry":        "https://github.com/acme/ai-registry/compare/spellbook/add-x?expand=1",
		"https://gitlab.com/acme/tools/registry.git": "https://gitlab.com/acme/tools/registry/-/merge_requests/new?merge_request%5Bsource_branch%5D=spellbook%2Fadd-x",
		"/tmp/local/registry":                        "",
		"https://git.example.com/acme/registry":      "",
	} {
		if got := push.PullRequestURL(repo, "spellbook/add-x"); got != want {
			t.Errorf("%s:\n got %s\nwant %s", repo, got, want)
		}
	}
}

func TestPushLocalChangeToSomethingFromTheRegistry(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	skill := filepath.Join(project, ".claude", "skills", "review", "SKILL.md")
	original := read(t, skill)

	if code, out := e.run("push", "skill", "review", "-p", "--yes"); code == 0 || !strings.Contains(out, "nothing to push") {
		t.Errorf("an unchanged resource has nothing to push: exit %d\n%s", code, out)
	}

	write(t, skill, strings.Replace(original, "v1", "my improvement", 1))
	write(t, filepath.Join(project, ".claude", "skills", "review", "extra.md"), "more\n")
	if out := e.mustRun("push", "-p"); !strings.Contains(out, "changed, from acme") {
		t.Errorf("push should list the changed skill:\n%s", out)
	}
	out := e.mustRun("push", "skill", "review", "-p", "--yes")
	if !strings.Contains(out, "replaces what is in acme: skills/review") || !strings.Contains(out, "spellbook/update-review") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if got := show(t, repo, "spellbook/update-review", "skills/review/SKILL.md"); !strings.Contains(got, "my improvement") {
		t.Errorf("the branch does not hold the change:\n%s", got)
	}
	show(t, repo, "spellbook/update-review", "skills/review/extra.md")

	// Until the registry has something new, the local change stays, without
	// the warning about a local edit.
	out = e.mustRun("sync", "-p")
	if !strings.Contains(read(t, skill), "my improvement") || strings.Contains(out, "edited locally") {
		t.Fatalf("the pushed change must be kept quietly:\n%s", out)
	}
	// The registry moving on elsewhere does not change that.
	e.writeSkills(repo, map[string]string{"skills/other": "v1"})
	e.commit(repo, "unrelated")
	out = e.mustRun("sync", "-p")
	if !strings.Contains(read(t, skill), "my improvement") || strings.Contains(out, "edited locally") {
		t.Fatalf("an unrelated change in the registry must not undo the pushed change:\n%s", out)
	}

	e.git(repo, "merge", "--quiet", "spellbook/update-review")
	out = e.mustRun("sync", "-p")
	if !strings.Contains(read(t, skill), "my improvement") || strings.Contains(out, "warning") {
		t.Errorf("after the merge everything should be in step:\n%s", out)
	}
	if out := e.mustRun("list", "-p"); strings.Contains(out, "pushed") {
		t.Errorf("after the merge nothing is pending:\n%s", out)
	}
	if !strings.Contains(read(t, filepath.Join(project, ".github", "skills", "review", "SKILL.md")), "my improvement") {
		t.Error("the other tool should get the merged change")
	}
}

func TestPushKeepsWhatTheLocalFormCannotCarry(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	write(t, filepath.Join(repo, "agents", "reviewer.md"), "---\ndescription: Reviews code\ntools: Read, Grep\nclaude:\n  model: opus\ncopilot:\n  model: gpt-5\n---\nYou review code. v1\n")
	write(t, filepath.Join(repo, "instructions", "tf.md"), "---\ndescription: Terraform style\npaths:\n  - \"**/*.tf\"\n---\nPin versions.\n")
	e.commit(repo, "agent and instruction")
	e.mustRun("sync", "-p")

	agent := filepath.Join(project, ".claude", "agents", "reviewer.md")
	write(t, agent, strings.Replace(read(t, agent), "v1", "v2 from the project", 1))
	rule := filepath.Join(project, ".claude", "rules", "tf.md")
	write(t, rule, strings.Replace(read(t, rule), "Pin versions.", "Pin versions and providers.", 1))

	out := e.mustRun("push", ".claude/agents/reviewer.md", ".claude/rules/tf.md", "-p", "--yes", "--branch", "b")
	got := show(t, repo, "b", "agents/reviewer.md")
	for _, want := range []string{"v2 from the project", "copilot:\n    model: gpt-5", "claude:\n    model: opus", "description: Reviews code"} {
		if !strings.Contains(got, want) {
			t.Errorf("the agent in the registry misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "name: reviewer") {
		t.Errorf("the name Claude Code needs does not belong in the registry:\n%s", got)
	}
	if !strings.Contains(out, "kept from the registry's version, because your claude copy does not hold them: copilot") {
		t.Errorf("the user should be told what was kept:\n%s", out)
	}
	ins := show(t, repo, "b", "instructions/tf.md")
	if !strings.Contains(ins, "description: Terraform style") || !strings.Contains(ins, "Pin versions and providers.") || strings.Contains(ins, "Installed by spellbook") {
		t.Errorf("the instruction lost its description or kept the install note:\n%s", ins)
	}
}

func TestPushRestoresAPrefixedName(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	e.mustRun("registry", "set", "acme", "--prefix", "all", "-p")
	skill := filepath.Join(project, ".claude", "skills", "acme-review", "SKILL.md")
	write(t, skill, strings.Replace(read(t, skill), "v1", "v2", 1))
	e.mustRun("push", "skill", "acme-review", "-p", "--yes", "--branch", "b")
	got := show(t, repo, "b", "skills/review/SKILL.md")
	if !strings.Contains(got, "name: review\n") || !strings.Contains(got, "v2") {
		t.Errorf("the change should go to the skill under its own name:\n%s", got)
	}
}

func TestPushReplacesAResourceOfTheSameName(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	write(t, filepath.Join(repo, "skills", "notes", "SKILL.md"), "---\nname: notes\ndescription: Theirs\n---\ntheirs\n")
	e.commit(repo, "notes")
	// Not synced yet, and the project has its own skill of that name.
	write(t, filepath.Join(project, ".claude", "skills", "notes", "SKILL.md"), "---\nname: notes\ndescription: Mine\n---\nmine\n")
	out := e.mustRun("push", "skill", "notes", "-p", "--yes")
	if !strings.Contains(out, "replaces what is in acme") || !strings.Contains(show(t, repo, "spellbook/update-notes", "skills/notes/SKILL.md"), "mine") {
		t.Errorf("a local resource with a name the registry uses is an update, and says so:\n%s", out)
	}
}

func TestPushFromCursorCodexAndGemini(t *testing.T) {
	e := newEnv(t)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "test", "GIT_AUTHOR_EMAIL": "test@example.com", "GIT_COMMITTER_NAME": "test", "GIT_COMMITTER_EMAIL": "test@example.com"} {
		t.Setenv(k, v)
	}
	repo := e.repo(map[string]string{"skills/review": "v1"})
	write(t, filepath.Join(repo, "agents", "reviewer.md"), "---\ndescription: Reviews code\ntools: Read, Grep\nclaude:\n  model: opus\n---\nYou review code. v1\n")
	e.commit(repo, "agent")
	project := t.TempDir()
	t.Chdir(project)
	e.mustRun("init", "--project", "--target", "cursor", "--target", "codex", "--target", "gemini")
	e.mustRun("registry", "add", "acme", repo, "-p")

	// Cursor: a rule with unquoted globs, the way Cursor writes it.
	write(t, filepath.Join(project, ".cursor", "rules", "ts.mdc"), "---\nglobs: **/*.ts,**/*.tsx\nalwaysApply: false\n---\nUse strict mode.\n")
	// Gemini CLI: a command as TOML.
	write(t, filepath.Join(project, ".gemini", "commands", "standup.toml"), "description = \"Write a standup\"\nprompt = \"\"\"\nStandup for {{args}}.\n\"\"\"\n")
	// Codex: a new agent as TOML, and a change to the one from the registry.
	write(t, filepath.Join(project, ".codex", "agents", "planner.toml"), "name = \"planner\"\ndescription = \"Plans work\"\ndeveloper_instructions = \"You plan.\"\nmodel_reasoning_effort = \"high\"\n")
	codexAgent := filepath.Join(project, ".codex", "agents", "reviewer.toml")
	write(t, codexAgent, strings.Replace(read(t, codexAgent), "v1", "v2 from codex", 1))

	e.mustRun("push", "-p", "--yes", "--branch", "b",
		".cursor/rules/ts.mdc", ".gemini/commands/standup.toml", "agent", "planner", "agent", "reviewer")

	if got := show(t, repo, "b", "instructions/ts.md"); !strings.Contains(got, `- "**/*.ts"`) || !strings.Contains(got, `- "**/*.tsx"`) || strings.Contains(got, "globs") || strings.Contains(got, "alwaysApply") {
		t.Errorf("Cursor's globs should become paths:\n%s", got)
	}
	if got := show(t, repo, "b", "prompts/standup.md"); !strings.Contains(got, "description: Write a standup") || !strings.Contains(got, "Standup for $ARGUMENTS.") {
		t.Errorf("the Gemini command should become a prompt:\n%s", got)
	}
	planner := show(t, repo, "b", "agents/planner.md")
	if !strings.Contains(planner, "description: Plans work") || !strings.Contains(planner, "codex:\n    model_reasoning_effort: high") || !strings.Contains(planner, "You plan.") || strings.Contains(planner, "name:") {
		t.Errorf("the Codex agent should become Markdown with its own fields under codex:\n%s", planner)
	}
	// Codex's copy holds neither the tools nor Claude Code's model.
	reviewer := show(t, repo, "b", "agents/reviewer.md")
	for _, want := range []string{"v2 from codex", "tools: Read, Grep", "claude:\n    model: opus", "description: Reviews code"} {
		if !strings.Contains(reviewer, want) {
			t.Errorf("the updated agent misses %q:\n%s", want, reviewer)
		}
	}
}

func TestPushMCPServerRefusesSecretsAndSaysHowToFixThem(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	mcp := filepath.Join(project, ".mcp.json")
	write(t, mcp, `{
  "mcpServers": {
    "docs": {
      "type": "http",
      "url": "https://mcp.example.com/mcp?api_key=abc123",
      "headers": {"Authorization": "Bearer sk-live-123", "X-Team": "platform"}
    },
    "tracker": {
      "command": "npx",
      "args": ["-y", "@acme/tracker", "--token", "t0p-secret"],
      "env": {"TRACKER_TOKEN": "t0p-secret", "LOG_LEVEL": "debug"}
    }
  }
}
`)
	code, out := e.run("push", "mcp", "docs", "-p", "--yes")
	if code == 0 {
		t.Fatalf("a definition with secrets must be refused:\n%s", out)
	}
	for _, want := range []string{`"Authorization": "Bearer ${API_TOKEN}"`, "headers.X-Team is written out", "--plain X-Team", "url carries api_key", "push again"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal should explain the fix, missing %q:\n%s", want, out)
		}
	}
	code, out = e.run("push", "mcp", "tracker", "-p", "--yes")
	if code == 0 || !strings.Contains(out, `"TRACKER_TOKEN": "${TRACKER_TOKEN}"`) || !strings.Contains(out, "the value after --token is written out") || !strings.Contains(out, "env.LOG_LEVEL") {
		t.Errorf("env values and flag values should be named:\n%s", out)
	}
	if b, _ := exec.Command("git", "-C", repo, "branch", "--list", "spellbook/*").Output(); strings.TrimSpace(string(b)) != "" {
		t.Fatalf("a refused push created a branch: %s", b)
	}

	// Fixed as instructed.
	write(t, mcp, `{
  "mcpServers": {
    "docs": {
      "type": "http",
      "url": "https://mcp.example.com/mcp",
      "headers": {"Authorization": "Bearer ${API_TOKEN}", "X-Team": "platform"}
    },
    "tracker": {
      "command": "npx",
      "args": ["-y", "@acme/tracker", "--token", "${TRACKER_TOKEN}"],
      "env": {"TRACKER_TOKEN": "${TRACKER_TOKEN}", "LOG_LEVEL": "debug"}
    }
  }
}
`)
	out = e.mustRun("push", "mcp", "docs", "mcp", "tracker", "-p", "--yes", "--plain", "X-Team", "--plain", "LOG_LEVEL", "--branch", "b")
	if !strings.Contains(out, "registry allow") {
		t.Errorf("the user should be reminded that MCP servers need permission:\n%s", out)
	}
	docs := show(t, repo, "b", "mcp/docs.json")
	if !strings.Contains(docs, "Bearer ${API_TOKEN}") || !strings.Contains(docs, `"X-Team": "platform"`) {
		t.Errorf("the pushed definition is wrong:\n%s", docs)
	}

	// The servers stay where they are, before and after the merge.
	e.mustRun("sync", "-p")
	if got := read(t, mcp); !strings.Contains(got, `"tracker"`) || !strings.Contains(got, `"docs"`) {
		t.Fatalf("a sync removed pushed servers:\n%s", got)
	}
	e.git(repo, "merge", "--quiet", "b")
	e.mustRun("registry", "allow", "acme", "mcp", "-p")
	out = e.mustRun("sync", "-p")
	if strings.Contains(out, "does not manage") || !strings.Contains(read(t, mcp), `"tracker"`) {
		t.Errorf("after the merge the servers should be the registry's, without complaints:\n%s", out)
	}
	if !exists(filepath.Join(project, ".github", "mcp.json")) {
		t.Error("after the merge the other tool should get the servers too")
	}
}

func TestPushHookAdoptsTheLocalEntry(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	settings := filepath.Join(project, ".claude", "settings.json")
	write(t, settings, `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "API_TOKEN=abc123 ./check.sh", "timeout": 10}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "grep -q 'push --force' && exit 2 || exit 0", "timeout": 10}]}
    ]
  }
}
`)
	if out := e.mustRun("push", "-p"); !strings.Contains(out, "PreToolUse:1") || !strings.Contains(out, "PreToolUse:2") {
		t.Errorf("push should list the local hooks:\n%s", out)
	}
	if code, out := e.run("push", "hook", "PreToolUse:2", "-p", "--yes"); code == 0 || !strings.Contains(out, "--name") {
		t.Errorf("a hook needs a name: exit %d\n%s", code, out)
	}
	if code, out := e.run("push", "hook", "PreToolUse:1", "-p", "--yes", "--name", "check"); code == 0 || !strings.Contains(out, "API_TOKEN is set to a value") {
		t.Errorf("a secret in the command must be refused with the fix: exit %d\n%s", code, out)
	}

	out := e.mustRun("push", "hook", "PreToolUse:2", "-p", "--yes", "--name", "no-force-push")
	got := show(t, repo, "spellbook/add-no-force-push", "hooks/no-force-push.json")
	for _, want := range []string{`"event": "PreToolUse"`, `"matcher": "Bash"`, `"timeout": 10`, "push --force"} {
		if !strings.Contains(got, want) {
			t.Errorf("the registry hook misses %s:\n%s", want, got)
		}
	}
	if !strings.Contains(out, "registry allow") {
		t.Errorf("the user should be reminded that hooks need permission:\n%s", out)
	}
	local := read(t, settings)
	if strings.Count(local, "# spellbook:acme/no-force-push") != 1 || !strings.Contains(local, `"model": "opus"`) || !strings.Contains(local, "API_TOKEN=abc123") {
		t.Errorf("the local entry should carry the marker, and nothing else should change:\n%s", local)
	}

	// Before the merge the entry stays; after it, there is still one.
	e.mustRun("sync", "-p")
	if !strings.Contains(read(t, settings), "acme/no-force-push") {
		t.Fatal("a sync removed the pushed hook")
	}
	e.git(repo, "merge", "--quiet", "spellbook/add-no-force-push")
	e.mustRun("registry", "allow", "acme", "hooks", "-p")
	if got := read(t, settings); strings.Count(got, "push --force") != 1 {
		t.Errorf("after the merge the hook must be there once, not twice:\n%s", got)
	}
}

func TestPushWholeInstructionFile(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	write(t, filepath.Join(project, "CLAUDE.md"), "# Project notes\n\nAlways run the tests.\n")
	if code, out := e.run("push", "CLAUDE.md", "-p", "--yes"); code == 0 || !strings.Contains(out, "--name") {
		t.Errorf("a whole file needs a name: exit %d\n%s", code, out)
	}
	out := e.mustRun("push", "CLAUDE.md", "-p", "--yes", "--name", "project-notes")
	if got := show(t, repo, "spellbook/add-project-notes", "instructions/project-notes.md"); got != "# Project notes\n\nAlways run the tests.\n" {
		t.Errorf("the instruction should be the file's text: %q", got)
	}
	if !strings.Contains(out, "remove the text from CLAUDE.md") || strings.Contains(out, "Your local copy stays") {
		t.Errorf("the user should be told to remove the text later:\n%s", out)
	}
	if read(t, filepath.Join(project, "CLAUDE.md")) != "# Project notes\n\nAlways run the tests.\n" {
		t.Error("the file must not be changed")
	}
}

func TestPushChangeInsideACopilotBlock(t *testing.T) {
	e, repo, project := pushEnv(t, "")
	write(t, filepath.Join(repo, "instructions", "standards.md"), "---\ndescription: Company standards\n---\nUse tabs.\n")
	e.commit(repo, "instruction")
	e.mustRun("sync", "-p")
	shared := filepath.Join(project, ".github", "copilot-instructions.md")
	write(t, shared, strings.Replace(read(t, shared), "Use tabs.", "Use spaces.", 1))

	e.mustRun("push", "instruction", "standards", "-p", "--yes", "--from", "copilot")
	got := show(t, repo, "spellbook/update-standards", "instructions/standards.md")
	if !strings.Contains(got, "Use spaces.") || !strings.Contains(got, "description: Company standards") {
		t.Errorf("the change from the block should reach the registry, with the description kept:\n%s", got)
	}
	out := e.mustRun("sync", "-p")
	if !strings.Contains(read(t, shared), "Use spaces.") || strings.Contains(out, "edited locally") {
		t.Errorf("the pushed change in the block must be kept quietly:\n%s", out)
	}
	e.git(repo, "merge", "--quiet", "spellbook/update-standards")
	out = e.mustRun("sync", "-p")
	if strings.Contains(out, "warning") || !strings.Contains(read(t, filepath.Join(project, ".claude", "rules", "standards.md")), "Use spaces.") {
		t.Errorf("after the merge the other tool should get the change:\n%s", out)
	}
}

func TestPushMCPServerFromCodex(t *testing.T) {
	e := newEnv(t)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "test", "GIT_AUTHOR_EMAIL": "test@example.com", "GIT_COMMITTER_NAME": "test", "GIT_COMMITTER_EMAIL": "test@example.com"} {
		t.Setenv(k, v)
	}
	repo := e.repo(map[string]string{"skills/review": "v1"})
	project := t.TempDir()
	t.Chdir(project)
	e.mustRun("init", "--project", "--target", "codex")
	e.mustRun("registry", "add", "acme", repo, "-p")
	config := filepath.Join(project, ".codex", "config.toml")
	write(t, config, "model = \"gpt-5\"\n\n[mcp_servers.docs]\nurl = \"https://mcp.example.com/mcp\"\nbearer_token_env_var = \"DOCS_TOKEN\"\n\n[mcp_servers.tracker]\ncommand = \"npx\"\nargs = [\"-y\", \"@acme/tracker\"]\nenv_vars = [\"TRACKER_TOKEN\"]\n")

	e.mustRun("push", "mcp", "docs", "mcp", "tracker", "-p", "--yes", "--branch", "b")
	docs := show(t, repo, "b", "mcp/docs.json")
	if !strings.Contains(docs, `"Authorization": "Bearer ${DOCS_TOKEN}"`) || !strings.Contains(docs, `"type": "http"`) {
		t.Errorf("Codex's bearer token setting should become a header for every tool:\n%s", docs)
	}
	if got := show(t, repo, "b", "mcp/tracker.json"); !strings.Contains(got, `"TRACKER_TOKEN": "${TRACKER_TOKEN}"`) {
		t.Errorf("Codex's env_vars should become env references:\n%s", got)
	}
	e.mustRun("sync", "-p")
	if got := read(t, config); !strings.Contains(got, "[mcp_servers.docs]") || !strings.Contains(got, `model = "gpt-5"`) {
		t.Errorf("a sync changed the pushed servers or the rest of the file:\n%s", got)
	}
}

func TestPushCanOpenThePullRequest(t *testing.T) {
	e, _, project := pushEnv(t, "")
	write(t, filepath.Join(project, ".claude", "skills", "notes", "SKILL.md"), "---\nname: notes\ndescription: Take notes\n---\nv1\n")
	// The test registry is a local path, which gh cannot serve.
	if code, out := e.run("push", "skill", "notes", "-p", "--yes", "--pr"); code == 0 || !strings.Contains(out, "not on GitHub") {
		t.Errorf("--pr for a registry that is not on GitHub should be refused before anything is pushed: exit %d\n%s", code, out)
	}
	if got, ok := push.GitHubRepo("git@github.com:acme/ai-registry.git"); !ok || got != "acme/ai-registry" {
		t.Errorf("GitHubRepo = %q, %v", got, ok)
	}
	if got, ok := push.GitHubRepo("https://github.example.com/acme/reg"); !ok || got != "github.example.com/acme/reg" {
		t.Errorf("GitHubRepo for an enterprise host = %q, %v", got, ok)
	}
}
