package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skill(t *testing.T, dir, frontmatterName string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + frontmatterName + "\ndescription: test\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, SkillFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(skills []Skill) string {
	var out []string
	for _, s := range skills {
		out = append(out, s.Name)
	}
	return strings.Join(out, ",")
}

func TestDiscoverFindsNestedSkillsAndSkipsWhatIsNotOne(t *testing.T) {
	root := t.TempDir()
	skill(t, filepath.Join(root, "skills", "alpha"), "alpha")
	skill(t, filepath.Join(root, "teams", "platform", "beta"), "beta")
	// A skill nested inside another skill belongs to the outer one.
	skill(t, filepath.Join(root, "skills", "alpha", "examples", "inner"), "inner")
	skill(t, filepath.Join(root, ".git", "hidden"), "hidden")
	skill(t, filepath.Join(root, "node_modules", "dep"), "dep")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	skills, warnings, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(skills); got != "alpha,beta" {
		t.Errorf("found %q, want alpha,beta", got)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestDiscoverWarnsAboutInvalidAndDuplicateNames(t *testing.T) {
	root := t.TempDir()
	skill(t, filepath.Join(root, "a", "dup"), "dup")
	skill(t, filepath.Join(root, "b", "dup"), "dup")
	skill(t, filepath.Join(root, "Bad Name"), "bad")

	skills, warnings, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(skills); got != "dup" {
		t.Errorf("found %q, want dup", got)
	}
	if len(warnings) != 2 {
		t.Errorf("want 2 warnings, got %v", warnings)
	}
}

func TestDiscoverNamesARootSkillByItsFrontmatter(t *testing.T) {
	root := t.TempDir()
	skill(t, root, "single")

	skills, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(skills); got != "single" {
		t.Errorf("found %q, want single", got)
	}
}
