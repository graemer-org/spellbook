package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// importing creates a registry with one skill of its own that imports
// from the repository other, as described by the import's body.
func (e *env) importing(other, body string) string {
	e.t.Helper()
	repo := e.repo(map[string]string{"skills/own": "v1"})
	write(e.t, filepath.Join(repo, "registry.toml"), "version = 1\n\n[[import]]\nurl = \""+other+"\"\n"+body)
	e.commit(repo, "import")
	return repo
}

func TestRegistryImportsNamedSkillsOfAnotherRepository(t *testing.T) {
	e := newEnv(t)
	other := e.repo(map[string]string{"deep/down/grill-me": "v1", "deep/unwanted": "v1"})
	write(e.t, filepath.Join(other, "agents", "helper.md"), "---\ndescription: Helps\n---\n\nHelp.\n")
	e.commit(other, "agent")
	repo := e.importing(other, "skills = [\"grill-me\"]\n")

	e.mustRun("init", "--target", "claude")
	out := e.mustRun("registry", "add", "mine", repo)
	if !strings.Contains(out, "2 skills") {
		t.Fatalf("expected the own and the imported skill:\n%s", out)
	}
	skills := filepath.Join(e.home, ".claude", "skills")
	if got := installed(t, skills); got != "grill-me,own" {
		t.Fatalf("installed %q", got)
	}
	// Naming skills imports nothing of the other kinds.
	if got := installed(t, filepath.Join(e.home, ".claude", "agents")); got != "" {
		t.Fatalf("agents installed: %q", got)
	}
	out = e.mustRun("sync")
	if !strings.Contains(out, "0 installed, 0 updated, 2 unchanged") {
		t.Fatalf("second sync changed something:\n%s", out)
	}

	// The imported repository moves on, the registry itself does not.
	e.writeSkills(other, map[string]string{"deep/down/grill-me": "v2"})
	e.commit(other, "v2")
	out = e.mustRun("sync")
	if !strings.Contains(out, "1 updated") {
		t.Fatalf("expected the imported skill to update:\n%s", out)
	}
	if !strings.Contains(read(t, filepath.Join(skills, "grill-me", "SKILL.md")), "v2") {
		t.Fatal("imported skill was not updated")
	}

	// Dropping the import removes what it brought.
	write(e.t, filepath.Join(repo, "registry.toml"), "version = 1\n")
	e.commit(repo, "no import")
	e.mustRun("sync")
	if got := installed(t, skills); got != "own" {
		t.Fatalf("installed %q after the import was dropped", got)
	}
}

func TestImportWithoutNamesTakesEverythingButGuardedKinds(t *testing.T) {
	e := newEnv(t)
	other := e.repo(map[string]string{"skills/a": "v1", "skills/own": "theirs"})
	write(e.t, filepath.Join(other, "agents", "helper.md"), "---\ndescription: Helps\n---\n\nHelp.\n")
	write(e.t, filepath.Join(other, "mcp", "docs.json"), `{"command": "docs-server"}`)
	write(e.t, filepath.Join(other, "registry.toml"), "version = 1\ndefault_groups = [\"none\"]\n[groups.none]\n\n[[import]]\nurl = \"nowhere\"\n")
	e.commit(other, "more")
	repo := e.importing(other, "")

	e.mustRun("init", "--target", "claude")
	out := e.mustRun("registry", "add", "mine", repo, "--allow", "mcp")
	if got := installed(t, filepath.Join(e.home, ".claude", "skills")); got != "a,own" {
		t.Fatalf("installed %q", got)
	}
	if got := installed(t, filepath.Join(e.home, ".claude", "agents")); got != "helper.md" {
		t.Fatalf("agents %q", got)
	}
	// The registry's own skill wins over an imported one of the same name.
	if strings.Contains(read(t, filepath.Join(e.home, ".claude", "skills", "own", "SKILL.md")), "theirs") {
		t.Fatal("the imported skill replaced the registry's own")
	}
	for _, want := range []string{`skill "own" is also in the registry itself`, "its own imports are not followed", "0 MCP servers"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestImportedSkillCanBeInAGroup(t *testing.T) {
	e := newEnv(t)
	other := e.repo(map[string]string{"skills/a": "v1", "skills/b": "v1"})
	repo := e.repo(map[string]string{"skills/own": "v1"})
	write(e.t, filepath.Join(repo, "registry.toml"), "version = 1\ndefault_groups = [\"base\"]\n\n[groups.base]\nskills = [\"a\"]\n\n[[import]]\nurl = \""+other+"\"\n")
	e.commit(repo, "import")

	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "mine", repo)
	if got := installed(t, filepath.Join(e.home, ".claude", "skills")); got != "a" {
		t.Fatalf("installed %q", got)
	}
}

func TestFailedImportRemovesNothing(t *testing.T) {
	e := newEnv(t)
	other := e.repo(map[string]string{"skills/a": "v1"})
	repo := e.importing(other, "")
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "mine", repo)

	write(e.t, filepath.Join(repo, "registry.toml"), "version = 1\n\n[[import]]\nurl = \""+filepath.Join(e.home, "gone")+"\"\n")
	e.commit(repo, "broken import")
	code, out := e.run("sync")
	if code == 0 || !strings.Contains(out, "import ") {
		t.Fatalf("expected a failure that names the import, got %d:\n%s", code, out)
	}
	if got := installed(t, filepath.Join(e.home, ".claude", "skills")); got != "a,own" {
		t.Fatalf("installed %q after a failed import", got)
	}
}

func TestPushRefusesAChangeToAnImportedSkill(t *testing.T) {
	e := newEnv(t)
	other := e.repo(map[string]string{"skills/a": "v1"})
	repo := e.importing(other, "")
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "mine", repo)

	skill := filepath.Join(e.home, ".claude", "skills", "a")
	write(e.t, filepath.Join(skill, "SKILL.md"), "---\nname: a\ndescription: test\n---\nmine\n")
	code, out := e.run("push", skill, "--to", "mine", "--yes")
	if code == 0 || !strings.Contains(out, "imports skill \"a\" from") {
		t.Fatalf("expected a refusal, got %d:\n%s", code, out)
	}
}
