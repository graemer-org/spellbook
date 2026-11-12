// Package target knows where each supported AI tool reads its files.
package target

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
)

type paths struct {
	homeSkills    string
	projectSkills string
}

// known lists the default skill directories, relative to the home directory
// or to the project root.
var known = map[string]paths{
	"claude":  {homeSkills: ".claude/skills", projectSkills: ".claude/skills"},
	"copilot": {homeSkills: ".copilot/skills", projectSkills: ".github/skills"},
}

// Names returns the supported target names in a stable order.
func Names() []string {
	names := make([]string, 0, len(known))
	for n := range known {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Known reports whether name is a supported target.
func Known(name string) bool {
	_, ok := known[name]
	return ok
}

// SkillsDir returns the absolute directory skills are written to for the
// named target in the given location, honouring a configured override.
func SkillsDir(name string, t config.Target, loc config.Location) (string, error) {
	p, ok := known[name]
	if !ok {
		return "", fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(Names(), ", "))
	}
	dir := p.homeSkills
	if loc.Scope == config.ScopeProject {
		dir = p.projectSkills
	}
	if t.SkillsPath != "" {
		dir = t.SkillsPath
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	dir = filepath.FromSlash(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(loc.Root, dir)
	}
	return filepath.Clean(dir), nil
}
