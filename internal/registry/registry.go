// Package registry fetches a subscribed repository and finds the resources
// in it.
package registry

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/gitx"
)

// SkillFile marks a directory as a skill.
const SkillFile = "SKILL.md"

// Skill is one skill directory found in a registry.
type Skill struct {
	Name string
	// Dir is the absolute directory that holds SKILL.md.
	Dir string
}

// Snapshot is the fetched state of one registry.
type Snapshot struct {
	Registry config.Registry
	Commit   string
	Skills   []Skill
	Warnings []string
}

// CacheRoot returns the directory registry checkouts are kept in.
// SPELLBOOK_CACHE_DIR overrides the default user cache directory.
func CacheRoot() (string, error) {
	if dir := os.Getenv("SPELLBOOK_CACHE_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find cache directory: %w", err)
	}
	return filepath.Join(base, "spellbook"), nil
}

// CheckoutDir returns where the checkout of r lives. The URL is part of the
// key so that two scopes using one name for different repositories, or a
// registry whose URL changed, never share a checkout.
func CheckoutDir(r config.Registry) (string, error) {
	root, err := CacheRoot()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(r.URL))
	return filepath.Join(root, "registries", r.Name+"-"+hex.EncodeToString(sum[:4])), nil
}

// Fetch updates the checkout of r and lists what it offers.
func Fetch(ctx context.Context, r config.Registry) (*Snapshot, error) {
	dir, err := CheckoutDir(r)
	if err != nil {
		return nil, err
	}
	commit, err := gitx.Fetch(ctx, r.URL, r.Ref, dir)
	if err != nil {
		return nil, err
	}
	root := dir
	if r.Path != "" {
		root = filepath.Join(dir, filepath.FromSlash(r.Path))
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("path %q does not exist in the repository", r.Path)
		}
	}
	skills, warnings, err := Discover(root)
	if err != nil {
		return nil, err
	}
	if len(r.Skills) > 0 {
		skills, warnings = filter(skills, r.Skills, warnings)
	}
	return &Snapshot{Registry: r, Commit: commit, Skills: skills, Warnings: warnings}, nil
}

func filter(skills []Skill, want []string, warnings []string) ([]Skill, []string) {
	byName := map[string]Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	var out []Skill
	for _, name := range want {
		s, ok := byName[name]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skill %q is configured but not in the registry", name))
			continue
		}
		out = append(out, s)
		delete(byName, name)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings
}

// Discover finds every directory below root that contains a SKILL.md. It
// does not look inside a skill for nested skills.
func Discover(root string) ([]Skill, []string, error) {
	var skills []Skill
	var warnings []string
	seen := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		info, err := os.Lstat(filepath.Join(p, SkillFile))
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		name := d.Name()
		if p == root {
			// The directory name of a checkout root is arbitrary, so a
			// repository that is itself one skill is named by its frontmatter.
			name = frontmatterName(filepath.Join(p, SkillFile))
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case !config.ValidName(name):
			warnings = append(warnings, fmt.Sprintf("skipped skill in %q: %q is not a valid skill name", filepath.ToSlash(rel), name))
		case seen[name] != "":
			warnings = append(warnings, fmt.Sprintf("skipped skill in %q: name %q is already used by %q", filepath.ToSlash(rel), name, seen[name]))
		default:
			seen[name] = filepath.ToSlash(rel)
			skills = append(skills, Skill{Name: name, Dir: p})
		}
		return filepath.SkipDir
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, warnings, nil
}

// frontmatterName reads the name field from the YAML frontmatter of a
// SKILL.md. It returns "" when there is none.
func frontmatterName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return ""
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if v, ok := strings.CutPrefix(line, "name:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
