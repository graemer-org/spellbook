// Package push takes a resource that was created locally, in the form one
// tool reads, and turns it into the form a registry holds, so it can be
// proposed to the registry.
package push

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/fsutil"
	"github.com/graemer-org/spellbook/internal/gitx"
	"github.com/graemer-org/spellbook/internal/registry"
	"github.com/graemer-org/spellbook/internal/state"
	"github.com/graemer-org/spellbook/internal/syncer"
	"github.com/graemer-org/spellbook/internal/target"
)

// Kinds that can be pushed, as the user writes them.
const (
	KindSkill       = "skill"
	KindInstruction = "instruction"
	KindAgent       = "agent"
	KindPrompt      = "prompt"
)

// Kinds lists the kinds that can be pushed.
var Kinds = []string{KindSkill, KindInstruction, KindAgent, KindPrompt}

// Candidate is a resource found in the place where a tool reads it.
type Candidate struct {
	Kind, Name, Target string
	// Path is the file, or for a skill the directory.
	Path string
	// Managed is true when Spellbook installed the resource, Pending when
	// it was pushed and the registry does not offer it yet.
	Managed, Pending bool
	// Registry is where a managed resource came from.
	Registry string
}

// stateKind maps a kind to the kind recorded in the state file.
func (c Candidate) stateKind() string {
	switch c.Kind {
	case KindInstruction:
		return syncer.KindInstruction
	case KindAgent:
		return syncer.KindAgent
	case KindPrompt:
		return syncer.KindPrompt
	}
	return syncer.KindSkill
}

// PendingItem is what MarkPending needs to know about the candidate.
func (c Candidate) PendingItem(reg string) syncer.PendingItem {
	return syncer.PendingItem{Abs: c.Path, Kind: c.stateKind(), Name: c.Name, Target: c.Target, Registry: reg}
}

// probe stands in for a name when asking a target where it keeps a kind.
const probe = "spellbookprobe"

// Candidates lists every resource of a pushable kind in the places the
// enabled tools read, whether Spellbook installed it or not.
func Candidates(loc config.Location, cfg *config.Config) ([]Candidate, error) {
	st, err := state.Load(loc.StatePath)
	if err != nil {
		return nil, err
	}
	recorded := st.ByPath()
	var out []Candidate
	seen := map[string]bool{}
	add := func(kind, name, tool, abs string) {
		if seen[abs] {
			// Two tools sharing one directory.
			return
		}
		seen[abs] = true
		c := Candidate{Kind: kind, Name: name, Target: tool, Path: abs}
		if e, ok := recorded[pathKey(loc.Root, abs)]; ok {
			c.Managed, c.Pending, c.Registry = true, e.Pending, e.Registry
		}
		out = append(out, c)
	}
	// files lists the files in the directory of pattern that are named
	// like it, with the probe standing for the name.
	files := func(kind, tool, pattern string) {
		prefix, suffix, ok := strings.Cut(filepath.Base(pattern), probe)
		if !ok {
			return
		}
		entries, err := os.ReadDir(filepath.Dir(pattern))
		if err != nil {
			return
		}
		for _, e := range entries {
			n := e.Name()
			if !e.Type().IsRegular() || !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, suffix) || len(n) <= len(prefix)+len(suffix) {
				continue
			}
			add(kind, n[len(prefix):len(n)-len(suffix)], tool, filepath.Join(filepath.Dir(pattern), n))
		}
	}

	for _, tool := range cfg.EnabledTargets() {
		if dir, err := target.SkillsDir(tool, cfg.Targets[tool], loc); err == nil {
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				abs := filepath.Join(dir, e.Name())
				if info, err := os.Stat(filepath.Join(abs, registry.SkillFile)); err == nil && info.Mode().IsRegular() {
					add(KindSkill, e.Name(), tool, abs)
				}
			}
		}
		for _, scoped := range []bool{true, false} {
			if place, err := target.InstructionPlace(tool, loc, probe, scoped); err == nil && !place.Block {
				files(KindInstruction, tool, place.File)
			}
		}
		if file, err := target.AgentFile(tool, loc, probe); err == nil {
			files(KindAgent, tool, file)
		}
		if place, err := target.PromptFor(tool, cfg.Targets[tool], loc, probe); err == nil && !place.AsSkill {
			files(KindPrompt, tool, place.Path)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return slices.Index(Kinds, a.Kind) < slices.Index(Kinds, b.Kind)
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Target < b.Target
	})
	return out, nil
}

// Item is a candidate in the form the registry holds.
type Item struct {
	Candidate
	// Dest is where it goes in the registry, relative to the registry's
	// root: a directory for a skill, a file otherwise.
	Dest  string
	Files []gitx.File
	// Notes tell the user what was changed on the way.
	Notes []string
}

// Select picks the candidates the arguments name. An argument is a path,
// or a kind followed by a name. from limits the choice to one tool.
func Select(all []Candidate, args []string, from string) ([]Item, error) {
	var items []Item
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var matches []Candidate
		what := arg
		if slices.Contains(Kinds, arg) && i+1 < len(args) {
			i++
			what = arg + " " + args[i]
			for _, c := range all {
				if c.Kind == arg && c.Name == args[i] && (from == "" || c.Target == from) {
					matches = append(matches, c)
				}
			}
		} else {
			abs, err := filepath.Abs(arg)
			if err != nil {
				return nil, err
			}
			if filepath.Base(abs) == registry.SkillFile {
				abs = filepath.Dir(abs)
			}
			for _, c := range all {
				if c.Path == abs {
					matches = append(matches, c)
				}
			}
			if len(matches) == 0 {
				if _, err := os.Stat(abs); err != nil {
					return nil, fmt.Errorf("%s does not exist", arg)
				}
				return nil, fmt.Errorf("%s is not in a place where an enabled tool reads skills, instructions, agents or prompts; run 'spellbook push' without arguments to see what can be pushed", arg)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("found no %s; run 'spellbook push' without arguments to see what can be pushed", what)
		}
		var built []Item
		for _, c := range matches {
			if c.Managed && !c.Pending {
				return nil, fmt.Errorf("%s came from registry %s and is managed by spellbook; pushing changes to a resource the registry already has is not supported yet", what, c.Registry)
			}
			it, err := build(c)
			if err != nil {
				return nil, fmt.Errorf("%s (%s): %w", what, c.Path, err)
			}
			built = append(built, it)
		}
		for _, other := range built[1:] {
			if !sameFiles(built[0].Files, other.Files) {
				var tools []string
				for _, b := range built {
					tools = append(tools, b.Target)
				}
				return nil, fmt.Errorf("%s exists for %s with different content; choose one with --from", what, strings.Join(tools, ", "))
			}
		}
		items = append(items, built[0])
	}
	return items, nil
}

func sameFiles(a, b []gitx.File) bool {
	return slices.EqualFunc(a, b, func(x, y gitx.File) bool {
		return x.Path == y.Path && x.Exec == y.Exec && string(x.Data) == string(y.Data)
	})
}

// installedNote is the line Spellbook puts into instruction files it
// writes. It does not belong in the registry.
var installedNote = regexp.MustCompile(`(?m)^<!-- Installed by spellbook from registry [^\n]*-->\n\n?`)

func build(c Candidate) (Item, error) {
	it := Item{Candidate: c}
	valid := config.ValidResourceName
	if c.Kind == KindSkill {
		valid = config.ValidName
	}
	if !valid(c.Name) {
		return it, fmt.Errorf("%q is not a valid %s name", c.Name, c.Kind)
	}

	if c.Kind == KindSkill {
		it.Dest = "skills/" + c.Name
		files, err := fsutil.List(c.Path)
		if err != nil {
			return it, err
		}
		for _, f := range files {
			data, err := os.ReadFile(filepath.Join(c.Path, filepath.FromSlash(f.Rel)))
			if err != nil {
				return it, err
			}
			it.Files = append(it.Files, gitx.File{Path: it.Dest + "/" + f.Rel, Data: data, Exec: f.Exec})
		}
		doc, err := registry.ParseDoc(c.Name, string(mustRead(filepath.Join(c.Path, registry.SkillFile))))
		if err != nil {
			return it, fmt.Errorf("%s: %w", registry.SkillFile, err)
		}
		if doc.Field("description") == "" {
			return it, fmt.Errorf("%s needs a 'description' in its frontmatter, so an agent knows when to use the skill", registry.SkillFile)
		}
		if n := doc.Field("name"); n != "" && n != c.Name {
			return it, fmt.Errorf("the name in %s is %q, but the directory is %q; make them the same", registry.SkillFile, n, c.Name)
		}
		return it, nil
	}

	data, err := os.ReadFile(c.Path)
	if err != nil {
		return it, err
	}
	text := string(data)
	var out []byte
	switch c.Kind {
	case KindInstruction:
		it.Dest = registry.InstructionsDir + "/" + c.Name + ".md"
		ins, err := registry.ParseInstruction(c.Name, installedNote.ReplaceAllString(text, ""))
		if err != nil {
			return it, err
		}
		front := &yaml.Node{Kind: yaml.MappingNode}
		if ins.Description != "" {
			front.Content = append(front.Content, str("description"), str(ins.Description))
		}
		if len(ins.Paths) > 0 {
			seq := &yaml.Node{Kind: yaml.SequenceNode}
			for _, p := range ins.Paths {
				seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p, Style: yaml.DoubleQuotedStyle})
			}
			front.Content = append(front.Content, str("paths"), seq)
		}
		if out, err = render(front, strings.TrimLeft(ins.Body, "\n")); err != nil {
			return it, err
		}
	case KindAgent, KindPrompt:
		dir := registry.AgentsDir
		if c.Kind == KindPrompt {
			dir = registry.PromptsDir
		}
		it.Dest = dir + "/" + c.Name + ".md"
		doc, err := registry.ParseDoc(c.Name, text)
		if err != nil {
			return it, err
		}
		if c.Kind == KindAgent && doc.Field("description") == "" {
			return it, fmt.Errorf("an agent needs a 'description' in its frontmatter")
		}
		front, notes := generalise(c, doc.Front)
		it.Notes = notes
		if out, err = render(front, doc.Body); err != nil {
			return it, err
		}
	default:
		return it, fmt.Errorf("a %s cannot be pushed", c.Kind)
	}
	it.Files = []gitx.File{{Path: it.Dest, Data: out}}
	return it, nil
}

func mustRead(path string) []byte {
	data, _ := os.ReadFile(path)
	return data
}

func str(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

// generalise turns the frontmatter of one tool's agent or prompt into
// frontmatter for every tool: fields that only mean something to the tool
// it came from move into the section named after that tool.
func generalise(c Candidate, front *yaml.Node) (*yaml.Node, []string) {
	out := &yaml.Node{Kind: yaml.MappingNode}
	own := &yaml.Node{Kind: yaml.MappingNode}
	var moved, notes []string
	// shared are the fields every tool reads the same way.
	shared := []string{"description", "tools"}
	if c.Kind == KindPrompt {
		shared = []string{"description"}
	}
	if front != nil {
		for i := 0; i+1 < len(front.Content); i += 2 {
			key, value := front.Content[i], front.Content[i+1]
			switch {
			case key.Value == "name" && len(target.AgentFields(c.Target, c.Name)) > 0:
				// This tool identifies an agent by its name, which is set
				// again on every install.
			case key.Value == "name" && c.Kind == KindAgent:
				out.Content = append(out.Content, key, value)
			case key.Value == c.Target && value.Kind == yaml.MappingNode:
				own.Content = append(own.Content, value.Content...)
			case target.Known(key.Value) || slices.Contains(shared, key.Value):
				out.Content = append(out.Content, key, value)
			default:
				own.Content = append(own.Content, key, value)
				moved = append(moved, key.Value)
			}
		}
	}
	if len(own.Content) > 0 {
		out.Content = append(out.Content, str(c.Target), own)
	}
	if len(moved) > 0 {
		what := "the field " + moved[0] + " now only applies"
		if len(moved) > 1 {
			what = "the fields " + strings.Join(moved, ", ") + " now only apply"
		}
		notes = append(notes, fmt.Sprintf("%s to %s (moved under '%s:'), because other tools may read them differently", what, c.Target, c.Target))
	}
	return out, notes
}

func render(front *yaml.Node, body string) ([]byte, error) {
	var b strings.Builder
	if front != nil && len(front.Content) > 0 {
		data, err := yaml.Marshal(front)
		if err != nil {
			return nil, err
		}
		b.WriteString("---\n")
		b.Write(data)
		b.WriteString("---\n\n")
	}
	b.WriteString(body)
	return []byte(b.String()), nil
}

// Exists reports whether the registry already has a resource of the
// item's kind and name.
func Exists(snap *registry.Snapshot, it Item) bool {
	switch it.Kind {
	case KindSkill:
		return slices.ContainsFunc(snap.All, func(s registry.Skill) bool { return s.Name == it.Name })
	case KindInstruction:
		return slices.ContainsFunc(snap.AllInstructions, func(i registry.Instruction) bool { return i.Name == it.Name })
	case KindAgent:
		return slices.ContainsFunc(snap.AllAgents, func(d registry.Doc) bool { return d.Name == it.Name })
	case KindPrompt:
		return slices.ContainsFunc(snap.AllPrompts, func(d registry.Doc) bool { return d.Name == it.Name })
	}
	return false
}

// groupKey is the key of a group in registry.toml that lists a kind.
func groupKey(kind string) string { return kind + "s" }

// AddToGroup adds the items to a group in the registry.toml in dir. The
// file is edited as text so its comments and layout stay; the result is
// read back, and the edit is refused if the group does not end up listing
// every item.
func AddToGroup(dir, group string, items []Item) error {
	file := filepath.Join(dir, registry.ManifestFile)
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("the registry has no %s, so it has no groups", registry.ManifestFile)
		}
		return err
	}
	text := string(data)
	header := regexp.MustCompile(`(?m)^\[groups\.` + regexp.QuoteMeta(group) + `\][ \t]*(#.*)?$`)
	loc := header.FindStringIndex(text)
	if loc == nil {
		m, _, _ := registry.LoadManifest(dir)
		var names []string
		if m != nil {
			names = m.GroupNames()
		}
		return fmt.Errorf("the registry has no group %q (available: %s)", group, strings.Join(names, ", "))
	}
	// The group's own lines end where the next table starts.
	end := len(text)
	if next := regexp.MustCompile(`(?m)^\[`).FindStringIndex(text[loc[1]:]); next != nil {
		end = loc[1] + next[0]
	}
	section := text[loc[1]:end]
	for _, it := range items {
		key := groupKey(it.Kind)
		list := regexp.MustCompile(`(?ms)^(` + key + `[ \t]*=[ \t]*\[)(.*?)(\])`)
		if m := list.FindStringSubmatchIndex(section); m != nil {
			inner := strings.TrimRight(section[m[4]:m[5]], " \t\n")
			sep := ", "
			if strings.Contains(inner, "\n") {
				sep = ",\n  "
			}
			inner = strings.TrimSuffix(inner, ",")
			if strings.TrimSpace(inner) == "" {
				sep = ""
			}
			tail := ""
			if strings.Contains(section[m[4]:m[5]], "\n") {
				tail = ",\n"
			}
			section = section[:m[4]] + inner + sep + fmt.Sprintf("%q", it.Name) + tail + section[m[5]:]
		} else {
			section = "\n" + fmt.Sprintf("%s = [%q]", key, it.Name) + section
		}
	}
	edited := text[:loc[1]] + section + text[end:]
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		return err
	}
	m, _, err := registry.LoadManifest(dir)
	if err == nil {
		for _, it := range items {
			var names []string
			switch it.Kind {
			case KindSkill:
				names = m.Groups[group].Skills
			case KindInstruction:
				names = m.Groups[group].Instructions
			case KindAgent:
				names = m.Groups[group].Agents
			case KindPrompt:
				names = m.Groups[group].Prompts
			}
			if !slices.Contains(names, it.Name) {
				err = errors.New("the entry did not end up in the group")
			}
		}
	}
	if err != nil {
		// Put the file back; a broken manifest must not be proposed.
		os.WriteFile(file, data, 0o644)
		return fmt.Errorf("could not add to group %q in %s automatically (%v); add it by hand in the pull request", group, registry.ManifestFile, err)
	}
	return nil
}

var scpURL = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`)

// PullRequestURL returns the page on which a pull request for the branch
// can be opened, for the hosts whose address for it is known, and ""
// otherwise.
func PullRequestURL(repo, branch string) string {
	host, p := "", ""
	if u, err := url.Parse(repo); err == nil && u.Host != "" {
		host, p = u.Hostname(), strings.TrimPrefix(u.Path, "/")
	} else if m := scpURL.FindStringSubmatch(repo); m != nil && !strings.HasPrefix(repo, "/") {
		host, p = m[1], m[2]
	}
	p = strings.TrimSuffix(strings.TrimSuffix(p, "/"), ".git")
	if host == "" || p == "" {
		return ""
	}
	switch {
	case host == "github.com" || strings.HasPrefix(host, "github."):
		return "https://" + host + "/" + p + "/compare/" + branch + "?expand=1"
	case strings.Contains(host, "gitlab"):
		return "https://" + host + "/" + p + "/-/merge_requests/new?merge_request%5Bsource_branch%5D=" + url.QueryEscape(branch)
	}
	return ""
}

// Prefix puts the registry's subdirectory in front of every path.
func Prefix(sub string, files []gitx.File) []gitx.File {
	if sub == "" {
		return files
	}
	out := make([]gitx.File, len(files))
	for i, f := range files {
		f.Path = path.Join(filepath.ToSlash(sub), f.Path)
		out[i] = f
	}
	return out
}

func pathKey(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return abs
	}
	return filepath.ToSlash(rel)
}
