package cli

import (
	"os"
	"os/exec"
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

// head returns the commit the repository in dir is at.
func (e *env) head(dir string) string {
	e.t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestRegistryImportsReportsAndMovesAnOutdatedPin(t *testing.T) {
	e := newEnv(t)
	other := e.repo(map[string]string{"grill-me": "v1", "unwanted": "v1"})
	pinned := e.head(other)
	manifest := "version = 1\n\n# Pinned on purpose.\n[[import]]\nurl = \"" + other + "\"\nref = \"" + pinned + "\" # moved by hand\nskills = [\"grill-me\"]\n\n[groups.all]\nskills = [\"grill-me\"]\n"
	repo := e.repo(map[string]string{"skills/own": "v1"})
	file := filepath.Join(repo, "registry.toml")
	write(t, file, manifest)

	if out := e.mustRun("registry", "imports", repo); !strings.Contains(out, "up to date at "+pinned[:7]) {
		t.Fatalf("a pin at the newest commit should be up to date:\n%s", out)
	}

	// A commit that does not touch what is imported is no reason to move.
	e.writeSkills(other, map[string]string{"unwanted": "v2"})
	e.commit(other, "elsewhere")
	out := e.mustRun("registry", "imports", repo, "--check")
	if !strings.Contains(out, "newer commits change nothing of it") {
		t.Fatalf("expected the import to count as up to date:\n%s", out)
	}
	e.mustRun("registry", "imports", repo, "--update")
	if read(t, file) != manifest {
		t.Fatalf("--update changed a manifest with nothing outdated:\n%s", read(t, file))
	}

	e.writeSkills(other, map[string]string{"grill-me": "v2"})
	e.commit(other, "v2")
	newest := e.head(other)
	code, out := e.run("registry", "imports", repo, "--check")
	if code == 0 || !strings.Contains(out, pinned[:7]+" -> "+newest[:7]) || !strings.Contains(out, "~ grill-me") || strings.Contains(out, "unwanted") {
		t.Fatalf("--check: exit %d\n%s", code, out)
	}
	if read(t, file) != manifest {
		t.Fatal("--check changed the manifest")
	}
	if out := e.mustRun("registry", "imports", repo); !strings.Contains(out, "--update") {
		t.Fatalf("expected a hint at --update:\n%s", out)
	}

	out = e.mustRun("registry", "imports", repo, "--update")
	if !strings.Contains(out, "Moved 1 of 1") {
		t.Fatalf("--update:\n%s", out)
	}
	// Only the ref changed: comments and the rest of the file stay.
	if want := strings.Replace(manifest, pinned, newest, 1); read(t, file) != want {
		t.Fatalf("manifest after --update:\n%s\nwant:\n%s", read(t, file), want)
	}
	if out := e.mustRun("registry", "imports", repo, "--check"); !strings.Contains(out, "up to date at "+newest[:7]) {
		t.Fatalf("after --update:\n%s", out)
	}
}

func TestRegistryImportsLeavesUnpinnedAndBrokenImportsAlone(t *testing.T) {
	e := newEnv(t)
	other := e.repo(map[string]string{"a": "v1", "b": "v1"})
	pinned := e.head(other)
	e.git(other, "tag", "v1")
	tagged := e.repo(map[string]string{"c": "v1"})
	e.git(tagged, "tag", "-a", "v1", "-m", "v1")
	e.writeSkills(tagged, map[string]string{"c": "v2", "d": "v1"})
	e.commit(tagged, "v2")
	// The repository drops a skill the import names.
	if err := os.RemoveAll(filepath.Join(other, "b")); err != nil {
		t.Fatal(err)
	}
	e.writeSkills(other, map[string]string{"a": "v2"})
	e.commit(other, "drop b")

	manifest := "version = 1\n" +
		"[[import]]\nurl = \"" + other + "\"\n" +
		"[[import]]\nurl = \"" + other + "\"\nref = \"main\"\n" +
		"[[import]]\nurl = \"" + other + "\"\nref = \"" + pinned + "\"\nskills = [\"a\", \"b\"]\n" +
		"[[import]]\nurl = \"" + tagged + "\"\nref = 'v1'\n" +
		"[[import]]\nurl = \"" + filepath.Join(e.home, "nowhere") + "\"\nref = \"v1\"\n"
	repo := t.TempDir()
	file := filepath.Join(repo, "registry.toml")
	write(t, file, manifest)

	code, out := e.run("registry", "imports", repo, "--update")
	if code == 0 {
		t.Fatalf("an import that cannot be reached should fail the command:\n%s", out)
	}
	for _, want := range []string{
		"not pinned, follows the default branch",
		"not pinned, follows branch main",
		"~ a", "- b", "--update leaves this import alone",
		"+ d", "~ c", "Moved 1 of 2",
		"error: import " + filepath.Join(e.home, "nowhere"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	// Only the tag moved, to the newest commit of its repository.
	if want := strings.Replace(manifest, "'v1'", `"`+e.head(tagged)+`"`, 1); read(t, file) != want {
		t.Fatalf("manifest after --update:\n%s\nwant:\n%s", read(t, file), want)
	}
}

func TestRegistryImportsNeedsAManifest(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	if code, out := e.run("registry", "imports", dir); code == 0 || !strings.Contains(out, "has no registry.toml") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	write(t, filepath.Join(dir, "registry.toml"), "version = 1\n")
	if out := e.mustRun("registry", "imports", dir); !strings.Contains(out, "imports nothing") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
