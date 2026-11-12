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

// Directories of a registry that hold agents and prompts.
const (
	AgentsDir  = "agents"
	PromptsDir = "prompts"
)

// Doc is a Markdown file with optional YAML frontmatter: an agent or a
// prompt. The frontmatter is kept as written, so fields Spellbook does not
// know reach the tools unchanged.
type Doc struct {
	Name string
	// Front is the frontmatter mapping, or nil when the file has none.
	Front *yaml.Node
	Body  string
	raw   string
}

// Hash identifies the content of the file.
func (d Doc) Hash() string {
	sum := sha256.Sum256([]byte(d.raw))
	return hex.EncodeToString(sum[:])
}

// Field returns a top-level scalar of the frontmatter, or "".
func (d Doc) Field(key string) string {
	if d.Front == nil {
		return ""
	}
	for i := 0; i+1 < len(d.Front.Content); i += 2 {
		if d.Front.Content[i].Value == key && d.Front.Content[i+1].Kind == yaml.ScalarNode {
			return d.Front.Content[i+1].Value
		}
	}
	return ""
}

// splitFrontmatter separates YAML frontmatter from the text after it.
func splitFrontmatter(text string) (front, body string, has bool, err error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return "", text, false, nil
	}
	if f, after, found := strings.Cut(rest, "\n---\n"); found {
		return f, after, true, nil
	}
	if f, ok := strings.CutSuffix(rest, "\n---"); ok {
		return f, "", true, nil
	}
	return "", "", false, fmt.Errorf("the frontmatter is not closed with '---'")
}

func parseDoc(name, text string) (Doc, error) {
	d := Doc{Name: name, raw: text}
	front, body, has, err := splitFrontmatter(text)
	if err != nil {
		return d, err
	}
	if has {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(front), &node); err != nil {
			return d, fmt.Errorf("invalid frontmatter: %v", err)
		}
		if len(node.Content) == 1 {
			if node.Content[0].Kind != yaml.MappingNode {
				return d, fmt.Errorf("the frontmatter must be a list of 'key: value' lines")
			}
			d.Front = node.Content[0]
		}
	}
	d.Body = strings.Trim(body, "\n") + "\n"
	if strings.TrimSpace(d.Body) == "" {
		return d, fmt.Errorf("the file has no text after the frontmatter")
	}
	return d, nil
}

// discoverDocs reads every Markdown file below root/dir. The file name
// without extension is the name. check may reject a file with a reason.
func discoverDocs(root, dir, kind string, check func(Doc) error) ([]Doc, []string, error) {
	base := filepath.Join(root, dir)
	if info, err := os.Stat(base); err != nil || !info.IsDir() {
		return nil, nil, nil
	}
	var out []Doc
	var warnings []string
	seen := map[string]string{}
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(d.Name(), ".md"), ".agent"), ".prompt")
		if !config.ValidResourceName(name) {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %q is not a valid %s name", rel, name, kind))
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
		doc, err := parseDoc(name, string(data))
		if err == nil && check != nil {
			err = check(doc)
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %v", rel, err))
			return nil
		}
		seen[name] = rel
		out = append(out, doc)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings, nil
}

// DiscoverAgents reads the agents of a registry. Both tools require an
// agent to have a description.
func DiscoverAgents(root string) ([]Doc, []string, error) {
	return discoverDocs(root, AgentsDir, "agent", func(d Doc) error {
		if d.Field("description") == "" {
			return fmt.Errorf("an agent needs a 'description' in its frontmatter")
		}
		return nil
	})
}

// DiscoverPrompts reads the prompts of a registry.
func DiscoverPrompts(root string) ([]Doc, []string, error) {
	return discoverDocs(root, PromptsDir, "prompt", nil)
}
