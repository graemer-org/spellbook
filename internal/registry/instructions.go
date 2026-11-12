package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/graemer-org/spellbook/internal/config"
)

// InstructionsDir is the directory of a registry that holds instructions.
const InstructionsDir = "instructions"

// Instruction is standing guidance for an agent, written once in the
// registry and rendered for each tool.
type Instruction struct {
	Name        string
	Description string
	// Paths holds globs. When set, the instruction only applies to matching
	// files; otherwise it always applies.
	Paths []string
	Body  string
}

// Scoped reports whether the instruction is limited to certain paths.
func (i Instruction) Scoped() bool { return len(i.Paths) > 0 }

// Hash identifies the content of the instruction.
func (i Instruction) Hash() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", strings.Join(i.Paths, "\x01"), i.Body)
	return hex.EncodeToString(h.Sum(nil))
}

// DiscoverInstructions reads every Markdown file below root/instructions.
// The file name without extension is the name of the instruction.
func DiscoverInstructions(root string) ([]Instruction, []string, error) {
	dir := filepath.Join(root, InstructionsDir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, nil, nil
	}
	var out []Instruction
	var warnings []string
	seen := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		name := strings.TrimSuffix(d.Name(), ".md")
		if !config.ValidName(name) {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %q is not a valid instruction name", rel, name))
			return nil
		}
		if other := seen[name]; other != "" {
			warnings = append(warnings, fmt.Sprintf("skipped %s: name %q is already used by %s", rel, name, other))
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		ins, err := parseInstruction(name, string(data))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %v", rel, err))
			return nil
		}
		seen[name] = rel
		out = append(out, ins)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings, nil
}

func parseInstruction(name, text string) (Instruction, error) {
	ins := Instruction{Name: name}
	front, body, has, err := splitFrontmatter(text)
	if err != nil {
		return ins, err
	}
	if has {
		var meta struct {
			Description string    `yaml:"description"`
			Paths       yaml.Node `yaml:"paths"`
		}
		if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
			return ins, fmt.Errorf("invalid frontmatter: %v", err)
		}
		ins.Description = meta.Description
		switch meta.Paths.Kind {
		case 0:
		case yaml.ScalarNode:
			// A single string may hold several globs separated by commas.
			for _, p := range strings.Split(meta.Paths.Value, ",") {
				if p = strings.TrimSpace(p); p != "" {
					ins.Paths = append(ins.Paths, p)
				}
			}
		case yaml.SequenceNode:
			if err := meta.Paths.Decode(&ins.Paths); err != nil {
				return ins, fmt.Errorf("paths must be a list of globs: %v", err)
			}
		default:
			return ins, fmt.Errorf("paths must be a list of globs or a comma-separated string")
		}
	}
	ins.Body = strings.Trim(body, "\n") + "\n"
	if strings.TrimSpace(ins.Body) == "" {
		return ins, fmt.Errorf("the instruction is empty")
	}
	return ins, nil
}
