package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// env is an isolated home directory with its own config and cache.
type env struct {
	t    *testing.T
	home string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("SPELLBOOK_CONFIG_DIR", "")
	t.Setenv("SPELLBOOK_CACHE_DIR", t.TempDir())
	return &env{t: t, home: home}
}

// run executes the CLI and returns its exit code and combined output.
func (e *env) run(args ...string) (int, string) {
	e.t.Helper()
	var out bytes.Buffer
	code := Execute(args, &out, &out)
	return code, out.String()
}

func (e *env) mustRun(args ...string) string {
	e.t.Helper()
	code, out := e.run(args...)
	if code != 0 {
		e.t.Fatalf("spellbook %s: exit %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

func (e *env) git(dir string, args ...string) {
	e.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repo creates a git repository with one commit holding the given skills.
// Each skill is "path/to/name" => body.
func (e *env) repo(skills map[string]string) string {
	e.t.Helper()
	dir := e.t.TempDir()
	e.git(dir, "init", "--quiet", "-b", "main")
	e.writeSkills(dir, skills)
	e.commit(dir, "init")
	return dir
}

func (e *env) writeSkills(dir string, skills map[string]string) {
	e.t.Helper()
	for rel, body := range skills {
		write(e.t, filepath.Join(dir, rel, "SKILL.md"), "---\nname: "+filepath.Base(rel)+"\ndescription: test\n---\n"+body+"\n")
	}
}

func (e *env) commit(dir, msg string) {
	e.t.Helper()
	e.git(dir, "add", "-A")
	e.git(dir, "commit", "--quiet", "-m", msg)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestSyncInstallsUpdatesAndRemovesInBothTargets(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"team/alpha": "v1", "team/beta": "v1", "gamma": "v1"})
	e.mustRun("init")
	e.mustRun("registry", "add", "acme", repo)

	claude := filepath.Join(e.home, ".claude", "skills")
	copilot := filepath.Join(e.home, ".copilot", "skills")
	for _, dir := range []string{claude, copilot} {
		for _, name := range []string{"alpha", "beta", "gamma"} {
			if !exists(filepath.Join(dir, name, "SKILL.md")) {
				t.Errorf("%s was not installed in %s", name, dir)
			}
		}
	}

	e.writeSkills(repo, map[string]string{"team/alpha": "v2", "delta": "v1"})
	if err := os.RemoveAll(filepath.Join(repo, "gamma")); err != nil {
		t.Fatal(err)
	}
	e.commit(repo, "change")
	out := e.mustRun("sync")

	for _, dir := range []string{claude, copilot} {
		if got := read(t, filepath.Join(dir, "alpha", "SKILL.md")); !strings.Contains(got, "v2") {
			t.Errorf("alpha in %s was not updated: %q", dir, got)
		}
		if !exists(filepath.Join(dir, "delta")) {
			t.Errorf("new skill delta is missing in %s", dir)
		}
		if exists(filepath.Join(dir, "gamma")) {
			t.Errorf("deleted skill gamma is still in %s", dir)
		}
	}
	if !strings.Contains(out, "1 installed, 1 updated, 1 unchanged, 1 removed, 0 skipped") {
		t.Errorf("unexpected summary:\n%s", out)
	}
}

func TestSyncLeavesUnmanagedAndLocallyEditedSkillsAlone(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1", "beta": "v1"})
	e.mustRun("init", "--target", "claude")
	claude := filepath.Join(e.home, ".claude", "skills")

	// A skill the user created by hand, with a name the registry also uses.
	write(t, filepath.Join(claude, "beta", "SKILL.md"), "mine")
	out := e.mustRun("registry", "add", "acme", repo)
	if got := read(t, filepath.Join(claude, "beta", "SKILL.md")); got != "mine" {
		t.Fatalf("unmanaged skill was overwritten: %q", got)
	}
	if !strings.Contains(out, "not managed by spellbook") {
		t.Errorf("no warning about the unmanaged skill:\n%s", out)
	}

	// A managed skill the user edited.
	write(t, filepath.Join(claude, "alpha", "SKILL.md"), "edited")
	e.writeSkills(repo, map[string]string{"alpha": "v2"})
	e.commit(repo, "v2")
	out = e.mustRun("sync")
	if got := read(t, filepath.Join(claude, "alpha", "SKILL.md")); got != "edited" {
		t.Fatalf("local edit was overwritten without --force: %q", got)
	}
	if !strings.Contains(out, "edited locally") {
		t.Errorf("no warning about the local edit:\n%s", out)
	}

	e.mustRun("sync", "--force")
	if got := read(t, filepath.Join(claude, "alpha", "SKILL.md")); !strings.Contains(got, "v2") {
		t.Errorf("--force did not overwrite the local edit: %q", got)
	}
	if got := read(t, filepath.Join(claude, "beta", "SKILL.md")); got != "mine" {
		t.Errorf("--force overwrote an unmanaged skill: %q", got)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1"})
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo, "--no-sync")

	out := e.mustRun("sync", "--dry-run")
	if !strings.Contains(out, "1 installed") {
		t.Errorf("dry run did not report the install:\n%s", out)
	}
	if exists(filepath.Join(e.home, ".claude", "skills", "alpha")) {
		t.Error("dry run installed a skill")
	}
	if exists(filepath.Join(e.home, ".config", "spellbook", "state.json")) {
		t.Error("dry run wrote the state file")
	}
}

func TestHigherPriorityRegistryWinsNameCollision(t *testing.T) {
	e := newEnv(t)
	low := e.repo(map[string]string{"shared": "from-low", "only-low": "x"})
	high := e.repo(map[string]string{"shared": "from-high"})
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "low", low)
	out := e.mustRun("registry", "add", "high", high, "--priority", "10")

	claude := filepath.Join(e.home, ".claude", "skills")
	if got := read(t, filepath.Join(claude, "shared", "SKILL.md")); !strings.Contains(got, "from-high") {
		t.Errorf("shared should come from the high priority registry: %q", got)
	}
	if !exists(filepath.Join(claude, "only-low")) {
		t.Error("the non-colliding skill of the low priority registry is missing")
	}
	if !strings.Contains(out, `skill "shared" is also provided by "high"`) {
		t.Errorf("no collision warning:\n%s", out)
	}
}

func TestRegistryOptionsPathAndSkillFilter(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"platform/a": "x", "platform/b": "x", "frontend/c": "x"})
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo, "--path", "platform", "--skill", "a")

	claude := filepath.Join(e.home, ".claude", "skills")
	if !exists(filepath.Join(claude, "a")) {
		t.Error("selected skill a is missing")
	}
	for _, name := range []string{"b", "c"} {
		if exists(filepath.Join(claude, name)) {
			t.Errorf("skill %s should not be installed", name)
		}
	}
}

func TestRegistryRemoveDeletesItsSkills(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1"})
	e.mustRun("init")
	e.mustRun("registry", "add", "acme", repo)
	e.mustRun("registry", "remove", "acme")

	for _, dir := range []string{".claude", ".copilot"} {
		if exists(filepath.Join(e.home, dir, "skills", "alpha")) {
			t.Errorf("alpha is still installed in %s", dir)
		}
	}
	if out := e.mustRun("list"); !strings.Contains(out, "Nothing installed") {
		t.Errorf("list still shows entries:\n%s", out)
	}
}

func TestFailedFetchKeepsInstalledSkillsAndExitsNonZero(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1"})
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo)

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	code, out := e.run("sync")
	if code == 0 {
		t.Errorf("sync should fail when a registry cannot be fetched:\n%s", out)
	}
	if !exists(filepath.Join(e.home, ".claude", "skills", "alpha")) {
		t.Error("skill was removed although its registry could not be fetched")
	}
}

func TestAddWithBadURLDoesNotChangeConfig(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init")
	code, _ := e.run("registry", "add", "bad", filepath.Join(e.home, "does-not-exist"))
	if code == 0 {
		t.Fatal("adding an unreachable registry should fail")
	}
	if out := e.mustRun("registry", "list"); !strings.Contains(out, "No registries") {
		t.Errorf("failed add left a registry behind:\n%s", out)
	}
}

func TestProjectScopeWritesIntoTheRepository(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1"})
	project := t.TempDir()
	t.Chdir(project)
	e.mustRun("init", "--project")
	e.mustRun("registry", "add", "acme", repo, "--project")

	for _, rel := range []string{".claude/skills/alpha/SKILL.md", ".github/skills/alpha/SKILL.md", ".spellbook/state.json", "spellbook.toml"} {
		if !exists(filepath.Join(project, rel)) {
			t.Errorf("%s is missing in the project", rel)
		}
	}
	if exists(filepath.Join(e.home, ".claude")) {
		t.Error("project sync wrote into the home directory")
	}
	// The committed state must not contain machine-specific paths.
	if st := read(t, filepath.Join(project, ".spellbook", "state.json")); strings.Contains(st, project) {
		t.Errorf("project state contains an absolute path:\n%s", st)
	}

	// The project is found from a subdirectory.
	sub := filepath.Join(project, "src", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if out := e.mustRun("list", "--project"); !strings.Contains(out, "alpha") {
		t.Errorf("list from a subdirectory did not find the project:\n%s", out)
	}
}

func TestCopilotSkillsPathIsConfigurable(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1"})
	e.mustRun("init", "--target", "copilot")
	e.mustRun("registry", "add", "acme", repo)

	cfgPath := filepath.Join(e.home, ".config", "spellbook", "config.toml")
	cfg := strings.Replace(read(t, cfgPath), "[targets.copilot]", "[targets.copilot]\nskills_path = \"~/.agents/skills\"", 1)
	write(t, cfgPath, cfg)
	e.mustRun("sync")

	if !exists(filepath.Join(e.home, ".agents", "skills", "alpha", "SKILL.md")) {
		t.Error("skill was not installed in the configured directory")
	}
	if exists(filepath.Join(e.home, ".copilot", "skills", "alpha")) {
		t.Error("skill was left behind in the previous directory")
	}
}

const teamManifest = `version = 1
default_groups = ["base"]

[groups.base]
description = "Everyone"
skills = ["review"]

[groups.platform]
include = ["base"]
skills = ["terraform"]

[groups.frontend]
skills = ["a11y"]
`

// teamRepo is a registry with a manifest and one skill per group, plus one
// skill that is in no group.
func (e *env) teamRepo() string {
	e.t.Helper()
	repo := e.repo(map[string]string{"skills/review": "v1", "skills/terraform": "v1", "skills/a11y": "v1", "skills/loose": "v1"})
	write(e.t, filepath.Join(repo, "registry.toml"), teamManifest)
	e.commit(repo, "manifest")
	return repo
}

func installed(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

func TestGroupsSelectSkillsWithIncludesAndDefaults(t *testing.T) {
	e := newEnv(t)
	repo := e.teamRepo()
	e.mustRun("init", "--target", "claude")
	claude := filepath.Join(e.home, ".claude", "skills")

	e.mustRun("registry", "add", "acme", repo)
	if got := installed(t, claude); got != "review" {
		t.Errorf("without a chosen group the default group applies: got %q, want review", got)
	}

	e.mustRun("group", "join", "acme", "frontend")
	if got := installed(t, claude); got != "a11y,review" {
		t.Errorf("joining a group must keep the default group: got %q, want a11y,review", got)
	}

	e.mustRun("group", "leave", "acme", "base", "frontend")
	e.mustRun("group", "join", "acme", "platform")
	if got := installed(t, claude); got != "review,terraform" {
		t.Errorf("platform includes base: got %q, want review,terraform", got)
	}

	out := e.mustRun("group", "list", "acme")
	for _, want := range []string{"platform", "yes", "frontend", "Everyone"} {
		if !strings.Contains(out, want) {
			t.Errorf("group list misses %q:\n%s", want, out)
		}
	}
}

func TestUnknownGroupIsRejectedAndChangesNothing(t *testing.T) {
	e := newEnv(t)
	repo := e.teamRepo()
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo)

	code, out := e.run("group", "join", "acme", "nope")
	if code == 0 || !strings.Contains(out, `no group "nope"`) {
		t.Errorf("joining an unknown group should fail clearly: exit %d\n%s", code, out)
	}
	if got := installed(t, filepath.Join(e.home, ".claude", "skills")); got != "review" {
		t.Errorf("failed join changed the installed skills: %q", got)
	}

	plain := e.repo(map[string]string{"alpha": "v1"})
	code, out = e.run("registry", "add", "plain", plain, "--group", "base")
	if code == 0 || !strings.Contains(out, "registry.toml") {
		t.Errorf("a group on a registry without manifest should fail clearly: exit %d\n%s", code, out)
	}
}

func TestManifestWithIncludeCycleIsAnError(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1"})
	write(t, filepath.Join(repo, "registry.toml"), "version = 1\n[groups.a]\ninclude = [\"b\"]\n[groups.b]\ninclude = [\"a\"]\n")
	e.commit(repo, "cycle")
	e.mustRun("init", "--target", "claude")

	code, out := e.run("registry", "add", "acme", repo)
	if code == 0 || !strings.Contains(out, "cycle") {
		t.Errorf("an include cycle should be reported: exit %d\n%s", code, out)
	}
}

func TestPrefixAllRenamesDirectoryAndFrontmatter(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1"})
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo, "--prefix", "all")

	skill := filepath.Join(e.home, ".claude", "skills", "acme-alpha", "SKILL.md")
	if got := read(t, skill); !strings.Contains(got, "name: acme-alpha\n") {
		t.Errorf("frontmatter name was not rewritten:\n%s", got)
	}
	// A second sync must see the rewritten copy as unchanged.
	if out := e.mustRun("sync"); !strings.Contains(out, "0 installed, 0 updated, 1 unchanged") {
		t.Errorf("prefixed skill is not stable across syncs:\n%s", out)
	}
}

func TestPrefixCollisionsKeepsBothSkills(t *testing.T) {
	e := newEnv(t)
	first := e.repo(map[string]string{"shared": "from-first"})
	second := e.repo(map[string]string{"shared": "from-second", "other": "x"})
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "first", first, "--priority", "10")
	e.mustRun("registry", "add", "second", second, "--prefix", "collisions")

	claude := filepath.Join(e.home, ".claude", "skills")
	if got := installed(t, claude); got != "other,second-shared,shared" {
		t.Fatalf("got %q, want other,second-shared,shared", got)
	}
	if got := read(t, filepath.Join(claude, "shared", "SKILL.md")); !strings.Contains(got, "from-first") {
		t.Errorf("the plain name should belong to the higher priority registry: %q", got)
	}
	if got := read(t, filepath.Join(claude, "second-shared", "SKILL.md")); !strings.Contains(got, "from-second") || !strings.Contains(got, "name: second-shared") {
		t.Errorf("the prefixed copy is wrong: %q", got)
	}
}

func TestLockedSyncInstallsTheRecordedCommit(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1"})
	project := t.TempDir()
	t.Chdir(project)
	e.mustRun("init", "--project", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo, "-p")
	lockFile := filepath.Join(project, "spellbook.lock")
	locked := read(t, lockFile)
	if !strings.Contains(locked, `name = "acme"`) || !strings.Contains(locked, "commit = ") {
		t.Fatalf("lock file is incomplete:\n%s", locked)
	}

	e.writeSkills(repo, map[string]string{"alpha": "v2"})
	e.commit(repo, "v2")

	// A teammate with a fresh checkout: config and lock, nothing installed.
	if err := os.RemoveAll(filepath.Join(project, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(project, ".spellbook")); err != nil {
		t.Fatal(err)
	}
	e.mustRun("sync", "-p", "--locked")
	skill := filepath.Join(project, ".claude", "skills", "alpha", "SKILL.md")
	if got := read(t, skill); !strings.Contains(got, "v1") {
		t.Errorf("--locked installed a newer commit than the lock records: %q", got)
	}
	if read(t, lockFile) != locked {
		t.Error("--locked changed the lock file")
	}

	out := e.mustRun("sync", "-p")
	if got := read(t, skill); !strings.Contains(got, "v2") {
		t.Errorf("a plain sync should move to the newest commit: %q", got)
	}
	if !strings.Contains(out, "(was ") {
		t.Errorf("sync should say which commit it moved from:\n%s", out)
	}
	if read(t, lockFile) == locked {
		t.Error("a plain sync should update the lock file")
	}
}

func TestLockedSyncFailsForARegistryThatIsNotLocked(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1"})
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo, "--no-sync")

	code, out := e.run("sync", "--locked")
	if code == 0 || !strings.Contains(out, "spellbook.lock") {
		t.Errorf("--locked without a lock entry should fail clearly: exit %d\n%s", code, out)
	}
}

func TestDiffShowsUpstreamChangesWithoutInstallingThem(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(map[string]string{"alpha": "v1", "beta": "v1", "gamma": "v1"})
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo)
	if out := e.mustRun("diff"); !strings.Contains(out, "up to date") {
		t.Errorf("diff right after a sync should be up to date:\n%s", out)
	}

	e.writeSkills(repo, map[string]string{"alpha": "v2", "delta": "v1"})
	if err := os.RemoveAll(filepath.Join(repo, "gamma")); err != nil {
		t.Fatal(err)
	}
	e.commit(repo, "change")

	out := e.mustRun("diff", "acme", "--patch")
	for _, want := range []string{"+ delta", "~ alpha", "- gamma", "+v2"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "beta") {
		t.Errorf("diff lists an unchanged skill:\n%s", out)
	}
	claude := filepath.Join(e.home, ".claude", "skills")
	if got := read(t, filepath.Join(claude, "alpha", "SKILL.md")); !strings.Contains(got, "v1") {
		t.Errorf("diff changed an installed skill: %q", got)
	}
	// The sync after a diff still applies the changes.
	e.mustRun("sync")
	if got := installed(t, claude); got != "alpha,beta,delta" {
		t.Errorf("after sync got %q, want alpha,beta,delta", got)
	}
}

func TestRegistrySetupCreatesAUsableRegistry(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(t.TempDir(), "my-registry")
	e.mustRun("registry", "setup", dir)
	e.git(dir, "init", "--quiet", "-b", "main")
	e.commit(dir, "init")

	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "mine", dir)
	if got := installed(t, filepath.Join(e.home, ".claude", "skills")); got != "example" {
		t.Errorf("got %q, want example", got)
	}
	if code, _ := e.run("registry", "setup", dir); code == 0 {
		t.Error("setup should refuse to overwrite an existing registry")
	}
}

const instructionManifest = `version = 1
default_groups = ["team"]

[groups.base]
instructions = ["standards"]

[groups.team]
include = ["base"]
skills = ["review"]
instructions = ["tf-style", "team-rules"]
`

// instructionRepo is a registry with always-on and path-scoped instructions.
func (e *env) instructionRepo() string {
	e.t.Helper()
	repo := e.repo(map[string]string{"skills/review": "v1"})
	write(e.t, filepath.Join(repo, "registry.toml"), instructionManifest)
	write(e.t, filepath.Join(repo, "instructions", "standards.md"), "---\ndescription: Company\n---\n# Standards\n\nUse tabs. v1\n")
	write(e.t, filepath.Join(repo, "instructions", "team-rules.md"), "Ask the platform team. v1\n")
	write(e.t, filepath.Join(repo, "instructions", "tf-style.md"), "---\npaths:\n  - \"infra/**/*.tf\"\n  - \"modules/**\"\n---\nPin provider versions.\n")
	e.commit(repo, "instructions")
	return repo
}

func TestInstructionsAreWrittenInEachToolsFormat(t *testing.T) {
	e := newEnv(t)
	repo := e.instructionRepo()
	e.mustRun("init")
	out := e.mustRun("registry", "add", "acme", repo)
	if !strings.Contains(out, "1 skills, 3 instructions") {
		t.Errorf("summary does not count the instructions:\n%s", out)
	}

	// Claude Code: one rule file per instruction, CLAUDE.md untouched.
	rules := filepath.Join(e.home, ".claude", "rules")
	if got := read(t, filepath.Join(rules, "standards.md")); !strings.Contains(got, "Use tabs. v1") || strings.HasPrefix(got, "---") {
		t.Errorf("always-on rule is wrong:\n%s", got)
	}
	if got := read(t, filepath.Join(rules, "tf-style.md")); !strings.HasPrefix(got, "---\npaths:\n  - \"infra/**/*.tf\"\n  - \"modules/**\"\n---\n") {
		t.Errorf("path-scoped rule has the wrong frontmatter:\n%s", got)
	}
	if exists(filepath.Join(e.home, ".claude", "CLAUDE.md")) {
		t.Error("CLAUDE.md must not be created")
	}

	// Copilot: always-on instructions as blocks, base group first.
	shared := read(t, filepath.Join(e.home, ".copilot", "copilot-instructions.md"))
	first := strings.Index(shared, "<!-- spellbook:begin acme/standards -->")
	second := strings.Index(shared, "<!-- spellbook:begin acme/team-rules -->")
	if first < 0 || second < 0 || first > second {
		t.Errorf("blocks are missing or the included group does not come first:\n%s", shared)
	}
	if strings.Contains(shared, "Pin provider versions") {
		t.Errorf("a path-scoped instruction must not be in the always-on file:\n%s", shared)
	}
	scoped := read(t, filepath.Join(e.home, ".copilot", "instructions", "tf-style.instructions.md"))
	if !strings.HasPrefix(scoped, "---\napplyTo: \"infra/**/*.tf,modules/**\"\n---\n") {
		t.Errorf("Copilot path-scoped file has the wrong frontmatter:\n%s", scoped)
	}

	// A second sync changes nothing, byte for byte.
	out = e.mustRun("sync")
	if !strings.Contains(out, "claude       0 installed, 0 updated, 4 unchanged") || !strings.Contains(out, "copilot      0 installed, 0 updated, 4 unchanged") {
		t.Errorf("second sync is not a no-op:\n%s", out)
	}
	if read(t, filepath.Join(e.home, ".copilot", "copilot-instructions.md")) != shared {
		t.Error("second sync rewrote the shared file")
	}
}

func TestBlocksLeaveTheUsersTextAlone(t *testing.T) {
	e := newEnv(t)
	repo := e.instructionRepo()
	shared := filepath.Join(e.home, ".copilot", "copilot-instructions.md")
	write(t, shared, "# My own notes\n\nAlways answer in German.\n")
	e.mustRun("init", "--target", "copilot")
	e.mustRun("registry", "add", "acme", repo)

	got := read(t, shared)
	if !strings.HasPrefix(got, "# My own notes\n\nAlways answer in German.\n\n<!-- spellbook:begin") {
		t.Fatalf("the user's text was changed or the blocks are not appended:\n%s", got)
	}

	// Upstream changes one instruction and drops another.
	write(t, filepath.Join(repo, "instructions", "standards.md"), "Use spaces. v2\n")
	if err := os.Remove(filepath.Join(repo, "instructions", "team-rules.md")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "registry.toml"), strings.Replace(instructionManifest, `, "team-rules"`, "", 1))
	e.commit(repo, "v2")
	e.mustRun("sync")

	got = read(t, shared)
	if !strings.Contains(got, "Use spaces. v2") || strings.Contains(got, "Use tabs. v1") {
		t.Errorf("block was not updated:\n%s", got)
	}
	if strings.Contains(got, "team-rules") {
		t.Errorf("removed instruction still has a block:\n%s", got)
	}

	e.mustRun("registry", "remove", "acme")
	if got := read(t, shared); got != "# My own notes\n\nAlways answer in German.\n" {
		t.Errorf("after removing the registry only the user's text should remain, got:\n%q", got)
	}
}

func TestSharedFileWithOnlyBlocksIsRemovedWithThem(t *testing.T) {
	e := newEnv(t)
	repo := e.instructionRepo()
	e.mustRun("init", "--target", "copilot")
	e.mustRun("registry", "add", "acme", repo)
	shared := filepath.Join(e.home, ".copilot", "copilot-instructions.md")
	if !exists(shared) {
		t.Fatal("shared file was not created")
	}
	e.mustRun("registry", "remove", "acme")
	if exists(shared) {
		t.Errorf("a file spellbook created should not be left behind empty: %q", read(t, shared))
	}
}

func TestLocallyEditedBlockAndRuleAreKept(t *testing.T) {
	e := newEnv(t)
	repo := e.instructionRepo()
	e.mustRun("init")
	e.mustRun("registry", "add", "acme", repo)

	shared := filepath.Join(e.home, ".copilot", "copilot-instructions.md")
	rule := filepath.Join(e.home, ".claude", "rules", "standards.md")
	write(t, shared, strings.Replace(read(t, shared), "Use tabs. v1", "Use tabs. MY EDIT", 1))
	write(t, rule, "MY RULE EDIT\n")

	write(t, filepath.Join(repo, "instructions", "standards.md"), "Use spaces. v2\n")
	e.commit(repo, "v2")
	out := e.mustRun("sync")
	if !strings.Contains(read(t, shared), "MY EDIT") || read(t, rule) != "MY RULE EDIT\n" {
		t.Fatal("local edits were overwritten without --force")
	}
	if strings.Count(out, "edited locally") != 2 {
		t.Errorf("expected a warning for the block and for the rule:\n%s", out)
	}

	e.mustRun("sync", "--force")
	if got := read(t, shared); !strings.Contains(got, "Use spaces. v2") || strings.Contains(got, "MY EDIT") {
		t.Errorf("--force did not overwrite the block:\n%s", got)
	}
	if got := read(t, rule); !strings.Contains(got, "Use spaces. v2") {
		t.Errorf("--force did not overwrite the rule:\n%s", got)
	}
}

func TestUnmanagedRuleFileIsNotOverwritten(t *testing.T) {
	e := newEnv(t)
	repo := e.instructionRepo()
	rule := filepath.Join(e.home, ".claude", "rules", "standards.md")
	write(t, rule, "mine\n")
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo)
	e.mustRun("sync", "--force")
	if got := read(t, rule); got != "mine\n" {
		t.Errorf("a rule file the user wrote was overwritten: %q", got)
	}
}

func TestInstructionsFollowRegistryPriority(t *testing.T) {
	e := newEnv(t)
	team := e.repo(map[string]string{"team-skill": "x"})
	write(t, filepath.Join(team, "instructions", "team.md"), "Team guidance.\n")
	e.commit(team, "team")
	company := e.repo(map[string]string{"company-skill": "x"})
	write(t, filepath.Join(company, "instructions", "company.md"), "Company guidance.\n")
	e.commit(company, "company")

	e.mustRun("init", "--target", "copilot")
	e.mustRun("registry", "add", "team", team)
	e.mustRun("registry", "add", "company", company, "--priority", "10")

	shared := read(t, filepath.Join(e.home, ".copilot", "copilot-instructions.md"))
	if c, tm := strings.Index(shared, "Company guidance."), strings.Index(shared, "Team guidance."); c < 0 || tm < 0 || c > tm {
		t.Errorf("the higher priority registry should come first:\n%s", shared)
	}
}

func TestProjectInstructionsGoIntoTheRepository(t *testing.T) {
	e := newEnv(t)
	repo := e.instructionRepo()
	project := t.TempDir()
	t.Chdir(project)
	e.mustRun("init", "--project")
	e.mustRun("registry", "add", "acme", repo, "-p")

	for _, rel := range []string{
		".claude/rules/standards.md",
		".claude/rules/tf-style.md",
		".github/copilot-instructions.md",
		".github/instructions/tf-style.instructions.md",
	} {
		if !exists(filepath.Join(project, rel)) {
			t.Errorf("%s is missing in the project", rel)
		}
	}
	if exists(filepath.Join(e.home, ".copilot")) || exists(filepath.Join(e.home, ".claude")) {
		t.Error("project sync wrote instructions into the home directory")
	}
}

func TestOnlyLimitsTheKindsOfResources(t *testing.T) {
	e := newEnv(t)
	repo := e.instructionRepo()
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo, "--only", "skills")
	if exists(filepath.Join(e.home, ".claude", "rules")) {
		t.Error("--only skills installed instructions")
	}
	if !exists(filepath.Join(e.home, ".claude", "skills", "review")) {
		t.Error("--only skills did not install the skill")
	}
	if code, out := e.run("registry", "add", "other", repo, "--only", "nonsense"); code == 0 {
		t.Errorf("an unknown kind should be rejected:\n%s", out)
	}
}

func TestDiffListsChangedInstructions(t *testing.T) {
	e := newEnv(t)
	repo := e.instructionRepo()
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo)
	write(t, filepath.Join(repo, "instructions", "standards.md"), "Use spaces. v2\n")
	e.commit(repo, "v2")
	if out := e.mustRun("diff"); !strings.Contains(out, "~ instructions/standards") {
		t.Errorf("diff does not list the changed instruction:\n%s", out)
	}
}

// agentRepo is a registry with one agent and one prompt.
func (e *env) agentRepo() string {
	e.t.Helper()
	repo := e.repo(map[string]string{"skills/review": "v1"})
	write(e.t, filepath.Join(repo, "agents", "reviewer.md"), `---
description: Reviews pull requests
tools: Read, Grep
custom-field: kept
claude:
  model: opus
copilot:
  model: gpt-5
  target: github-copilot
---

You review code. v1
`)
	write(e.t, filepath.Join(repo, "agents", "broken.md"), "No frontmatter, so no description.\n")
	write(e.t, filepath.Join(repo, "prompts", "release-notes.md"), `---
description: Draft release notes
claude:
  argument-hint: "[version]"
---

Write release notes for $ARGUMENTS.
`)
	e.commit(repo, "agents and prompts")
	return repo
}

func TestAgentsAreTranslatedForEachTool(t *testing.T) {
	e := newEnv(t)
	repo := e.agentRepo()
	e.mustRun("init")
	out := e.mustRun("registry", "add", "acme", repo, "--prefix", "all")
	if !strings.Contains(out, "1 agents, 1 prompts") {
		t.Errorf("summary does not count agents and prompts:\n%s", out)
	}
	if !strings.Contains(out, "agents/broken.md") || !strings.Contains(out, "description") {
		t.Errorf("an agent without description should be skipped with a reason:\n%s", out)
	}

	claude := read(t, filepath.Join(e.home, ".claude", "agents", "acme-reviewer.md"))
	for _, want := range []string{"name: acme-reviewer\n", "description: Reviews pull requests\n", "tools: Read, Grep\n", "custom-field: kept\n", "model: opus\n", "You review code. v1"} {
		if !strings.Contains(claude, want) {
			t.Errorf("Claude agent misses %q:\n%s", want, claude)
		}
	}
	for _, not := range []string{"gpt-5", "copilot:", "claude:", "target:"} {
		if strings.Contains(claude, not) {
			t.Errorf("Claude agent contains %q, which is for another tool:\n%s", not, claude)
		}
	}

	copilot := read(t, filepath.Join(e.home, ".copilot", "agents", "acme-reviewer.agent.md"))
	for _, want := range []string{"name: acme-reviewer\n", "model: gpt-5\n", "target: github-copilot\n", "custom-field: kept\n"} {
		if !strings.Contains(copilot, want) {
			t.Errorf("Copilot agent misses %q:\n%s", want, copilot)
		}
	}
	if strings.Contains(copilot, "opus") {
		t.Errorf("Copilot agent contains Claude's model:\n%s", copilot)
	}

	if out := e.mustRun("sync"); strings.Contains(out, " 1 updated") || strings.Contains(out, " 1 installed") {
		t.Errorf("second sync is not a no-op:\n%s", out)
	}
}

func TestPromptsBecomeASkillForClaudeAndAPromptFileForCopilot(t *testing.T) {
	e := newEnv(t)
	repo := e.agentRepo()
	project := t.TempDir()
	t.Chdir(project)
	e.mustRun("init", "--project")
	e.mustRun("registry", "add", "acme", repo, "-p")

	skill := read(t, filepath.Join(project, ".claude", "skills", "release-notes", "SKILL.md"))
	for _, want := range []string{"name: release-notes\n", "disable-model-invocation: true\n", "description: Draft release notes\n", "argument-hint:", "Write release notes for $ARGUMENTS."} {
		if !strings.Contains(skill, want) {
			t.Errorf("Claude prompt skill misses %q:\n%s", want, skill)
		}
	}
	prompt := read(t, filepath.Join(project, ".github", "prompts", "release-notes.prompt.md"))
	if !strings.Contains(prompt, "description: Draft release notes\n") || strings.Contains(prompt, "argument-hint") || strings.Contains(prompt, "name:") {
		t.Errorf("Copilot prompt file is wrong:\n%s", prompt)
	}
	if !exists(filepath.Join(project, ".github", "agents", "reviewer.agent.md")) || !exists(filepath.Join(project, ".claude", "agents", "reviewer.md")) {
		t.Error("project agents are missing")
	}

	// Upstream removes the prompt: both copies go.
	if err := os.RemoveAll(filepath.Join(repo, "prompts")); err != nil {
		t.Fatal(err)
	}
	e.commit(repo, "drop prompt")
	e.mustRun("sync", "-p")
	if exists(filepath.Join(project, ".claude", "skills", "release-notes")) || exists(filepath.Join(project, ".github", "prompts", "release-notes.prompt.md")) {
		t.Error("removed prompt is still installed")
	}
}

func TestCopilotPromptsAreSkippedAtHomeWithAWarning(t *testing.T) {
	e := newEnv(t)
	repo := e.agentRepo()
	e.mustRun("init")
	out := e.mustRun("registry", "add", "acme", repo)
	if !strings.Contains(out, "copilot: skipped 1 prompts") {
		t.Errorf("no warning that Copilot has no user-level prompts:\n%s", out)
	}
	if !exists(filepath.Join(e.home, ".claude", "skills", "release-notes", "SKILL.md")) {
		t.Error("Claude should still get the prompt")
	}
}

func TestGroupsSelectAgentsAndPrompts(t *testing.T) {
	e := newEnv(t)
	repo := e.agentRepo()
	write(t, filepath.Join(repo, "registry.toml"), "version = 1\n[groups.reviewers]\nagents = [\"reviewer\"]\n[groups.writers]\nprompts = [\"release-notes\"]\n")
	e.commit(repo, "manifest")
	e.mustRun("init", "--target", "claude")
	e.mustRun("registry", "add", "acme", repo, "--group", "reviewers")

	if !exists(filepath.Join(e.home, ".claude", "agents", "reviewer.md")) {
		t.Error("the agent of the joined group is missing")
	}
	if exists(filepath.Join(e.home, ".claude", "skills")) {
		t.Error("skills and prompts outside the joined group were installed")
	}
}
