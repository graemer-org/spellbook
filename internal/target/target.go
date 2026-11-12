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

// Place says where an instruction goes for one target.
type Place struct {
	// File is the absolute path that is written.
	File string
	// Block is true when the instruction becomes a managed block inside
	// File, which may also hold the user's own text. Otherwise File belongs
	// to the instruction alone.
	Block bool
}

// InstructionPlace returns where the named instruction is written.
//
// Claude Code loads every Markdown file in its rules directory, so each
// instruction gets a file of its own there and CLAUDE.md is never touched.
// Copilot reads path-scoped instructions from separate files, but always-on
// instructions only from copilot-instructions.md, so those become blocks.
func InstructionPlace(name string, loc config.Location, instruction string, scoped bool) (Place, error) {
	project := loc.Scope == config.ScopeProject
	switch name {
	case "claude":
		return Place{File: filepath.Join(loc.Root, ".claude", "rules", instruction+".md")}, nil
	case "copilot":
		base := filepath.Join(loc.Root, ".copilot")
		if project {
			base = filepath.Join(loc.Root, ".github")
		}
		if scoped {
			return Place{File: filepath.Join(base, "instructions", instruction+".instructions.md")}, nil
		}
		return Place{File: filepath.Join(base, "copilot-instructions.md"), Block: true}, nil
	}
	return Place{}, fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(Names(), ", "))
}

// RenderInstruction returns the content written for an instruction: the
// whole file, or for a block only its body.
func RenderInstruction(name, registry string, paths []string, body string, block bool) []byte {
	if block {
		return []byte(body)
	}
	var b strings.Builder
	if len(paths) > 0 {
		b.WriteString("---\n")
		switch name {
		case "copilot":
			fmt.Fprintf(&b, "applyTo: %q\n", strings.Join(paths, ","))
		default:
			b.WriteString("paths:\n")
			for _, p := range paths {
				fmt.Fprintf(&b, "  - %q\n", p)
			}
		}
		b.WriteString("---\n")
	}
	fmt.Fprintf(&b, "<!-- Installed by spellbook from registry %q. Change it there, not here. -->\n\n", registry)
	b.WriteString(body)
	return []byte(b.String())
}
