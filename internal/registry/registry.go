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
	// Dir is the checkout directory, the root of the git repository.
	Dir string
	// Manifest is nil when the registry has no registry.toml.
	Manifest *Manifest
	// Groups are the groups the selection is based on: the configured ones,
	// or the registry's default groups. Empty means no group filter.
	Groups []string
	// All holds every skill of the registry, Skills the selected ones.
	All    []Skill
	Skills []Skill
	// AllInstructions holds every instruction, Instructions the selected
	// ones in the order they should be read in.
	AllInstructions []Instruction
	Instructions    []Instruction
	Warnings        []string
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
	all, warnings, err := Discover(root)
	if err != nil {
		return nil, err
	}
	manifest, mwarn, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, mwarn...)
	instructions, iwarn, err := DiscoverInstructions(root)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, iwarn...)

	snap := &Snapshot{Registry: r, Commit: commit, Dir: dir, Manifest: manifest, All: all, AllInstructions: instructions}
	groups := r.Groups
	if len(groups) > 0 && manifest == nil {
		return nil, fmt.Errorf("groups are configured, but the registry has no %s", ManifestFile)
	}
	if len(groups) == 0 && manifest != nil {
		groups = manifest.DefaultGroups
	}
	snap.Groups = groups

	if r.Wants(config.KindSkills) {
		snap.Skills = all
		if len(groups) > 0 {
			names, err := manifest.Skills(groups)
			if err != nil {
				return nil, err
			}
			snap.Skills, warnings = pick(all, names, func(s Skill) string { return s.Name }, warnings, "a group lists skill %q, which is not in the registry")
		}
		if len(r.Skills) > 0 {
			snap.Skills, warnings = pick(snap.Skills, r.Skills, func(s Skill) string { return s.Name }, warnings, "skill %q is configured but not offered by the registry or the selected groups")
		}
		sort.Slice(snap.Skills, func(i, j int) bool { return snap.Skills[i].Name < snap.Skills[j].Name })
	}
	if r.Wants(config.KindInstructions) {
		snap.Instructions = instructions
		if len(groups) > 0 {
			names, err := manifest.Instructions(groups)
			if err != nil {
				return nil, err
			}
			snap.Instructions, warnings = pick(instructions, names, func(i Instruction) string { return i.Name }, warnings, "a group lists instruction %q, which is not in the registry")
		}
	}
	snap.Warnings = warnings
	return snap, nil
}

// pick returns the items with the wanted names, in the order of want, and
// warns about names that match nothing.
func pick[T any](items []T, want []string, name func(T) string, warnings []string, missing string) ([]T, []string) {
	byName := map[string]T{}
	for _, it := range items {
		byName[name(it)] = it
	}
	var out []T
	for _, n := range want {
		it, ok := byName[n]
		if !ok {
			warnings = append(warnings, fmt.Sprintf(missing, n))
			continue
		}
		out = append(out, it)
		delete(byName, n)
	}
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

// RewriteName sets the name field in the frontmatter of the SKILL.md in dir.
// A tool expects the name of a skill to match its directory, so a skill that
// is installed under a prefixed name needs its frontmatter changed too.
func RewriteName(dir, name string) error {
	path := filepath.Join(dir, SkillFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return nil
		}
		if strings.HasPrefix(lines[i], "name:") {
			lines[i] = "name: " + name
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return nil
}
