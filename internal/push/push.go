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

	"github.com/BurntSushi/toml"
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
var Kinds = []string{KindSkill, KindInstruction, KindAgent, KindPrompt, KindMCP, KindHook}

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
	// Hash is the recorded hash of a managed resource, and StateKind the
	// kind it is recorded as.
	Hash, StateKind string
	// entry is the definition of an MCP server, and base the hash of the
	// registry's version of it as this tool stores it.
	entry []byte
	base  string
	// tomlHash is the hash of a Codex server as its TOML file holds it.
	tomlHash string
}

// Current returns the hash of the resource as it is now.
func (c Candidate) Current() string {
	if c.Kind == KindMCP {
		return entryHash(c.entry)
	}
	if c.StateKind == syncer.KindBlock {
		return syncer.HashBytes(c.entry)
	}
	h, _ := syncer.HashPath(c.Path)
	return h
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

// Pending returns what to record in the state for a pushed item, and false
// when nothing is to be recorded. For a hook it also marks the user's
// entry in the hooks file as the pushed hook.
func (it Item) Pending(loc config.Location, reg string) (syncer.PendingItem, bool, error) {
	c := it.Candidate
	p := syncer.PendingItem{Abs: c.Path, Kind: c.stateKind(), Name: it.Name, Target: c.Target, Registry: reg}
	if c.Managed && !c.Pending {
		// The registry's version it was changed from.
		p.Base = c.Hash
	}
	switch {
	case it.noPending:
		return p, false, nil
	case c.Kind == KindHook:
		// The item carries its registry name; the path still ends in the
		// position of the entry.
		file := c.Path[:strings.LastIndex(c.Path, "#")]
		id := reg + "/" + it.Name
		hash, err := syncer.AdoptHook(file, it.hookEvent, it.hookIndex, id)
		if err != nil {
			return p, false, err
		}
		p.Abs, p.Kind, p.Key, p.Hash = file, syncer.KindHook, syncer.StateKey(loc, file)+"#"+id, hash
	case c.Kind == KindMCP:
		file := strings.TrimSuffix(c.Path, "#"+c.Name)
		p.Abs, p.Kind, p.Key, p.Hash = file, c.StateKind, syncer.StateKey(loc, file)+"#"+c.Name, entryHash(c.entry)
		if c.tomlHash != "" {
			p.Hash = c.tomlHash
		}
		if c.Managed && !c.Pending {
			p.Base = c.base
		}
	case c.StateKind == syncer.KindBlock:
		i := strings.LastIndex(c.Path, "#")
		p.Abs, p.Kind, p.Key, p.Hash = c.Path[:i], syncer.KindBlock, syncer.StateKey(loc, c.Path[:i])+c.Path[i:], syncer.HashBytes(c.entry)
		p.Name = c.Name
	}
	return p, true, nil
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
			c.Managed, c.Pending, c.Registry, c.Hash, c.StateKind = true, e.Pending, e.Registry, e.Hash, e.Kind
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
	more := func(c Candidate) { out = append(out, c) }
	mcpCandidates(loc, cfg, recorded, more)
	hookCandidates(loc, cfg, more)
	blockCandidates(loc, recorded, more)
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
	// Update is true when the registry already has the resource, and
	// Remove then lists what is deleted before Files are written.
	Update bool
	Remove []string
	// Lossy is true when the tool's form holds only part of what the
	// registry holds, so an update must keep the rest from the registry.
	Lossy bool
	// noPending is true for something that is not one resource of a tool,
	// such as a whole hand-written file; nothing is recorded for it.
	noPending bool
	// hookEvent and hookIndex say which entry of the hooks file a hook is.
	hookEvent string
	hookIndex int
}

// Select picks the candidates the arguments name. An argument is a path,
// or a kind followed by a name. from limits the choice to one tool.
func Select(all []Candidate, args []string, opts Options) ([]Item, error) {
	from := opts.From
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
			if len(matches) == 0 && slices.Contains(instructionFiles, filepath.Base(abs)) {
				it, err := buildWholeFile(abs, opts)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", arg, err)
				}
				items = append(items, it)
				continue
			}
			if len(matches) == 0 {
				if _, err := os.Stat(abs); err != nil {
					return nil, fmt.Errorf("%s does not exist", arg)
				}
				return nil, fmt.Errorf("%s is not in a place where an enabled tool reads skills, instructions, agents or prompts; run 'spellbook push' without arguments to see what can be pushed (an MCP server is named as 'mcp <name>', a hook as 'hook <Event>:<number>')", arg)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("found no %s; run 'spellbook push' without arguments to see what can be pushed", what)
		}
		// The same resource is usually installed for several tools. Only
		// the copies that differ from the registry's version matter.
		var changed []Candidate
		for _, c := range matches {
			if c.Managed && !c.Pending && c.Current() == c.Hash {
				continue
			}
			changed = append(changed, c)
		}
		if len(changed) == 0 {
			return nil, fmt.Errorf("%s is exactly what registry %s has; there is nothing to push", what, matches[0].Registry)
		}
		var built []Item
		for _, c := range changed {
			if c.Kind != KindMCP && c.StateKind != syncer.KindBlock && c.Managed && !c.Pending && c.StateKind != c.stateKind() {
				return nil, fmt.Errorf("%s was installed from a %s in registry %s; pushing changes to it is not supported yet", what, c.StateKind, c.Registry)
			}
			var it Item
			var err error
			switch {
			case c.Kind == KindMCP:
				it, err = buildMCP(c, opts.Plain)
			case c.Kind == KindHook:
				it, err = buildHook(c, opts)
			case c.StateKind == syncer.KindBlock:
				it = Item{Candidate: c, Dest: registry.InstructionsDir + "/" + c.Name + ".md"}
				it.Files = []gitx.File{{Path: it.Dest, Data: c.entry}}
			default:
				it, err = build(c)
			}
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
		if out, err = renderInstruction(ins); err != nil {
			return it, err
		}
	case KindAgent, KindPrompt:
		dir := registry.AgentsDir
		if c.Kind == KindPrompt {
			dir = registry.PromptsDir
		}
		it.Dest = dir + "/" + c.Name + ".md"
		if strings.HasSuffix(c.Path, ".toml") {
			// Codex agents and Gemini CLI commands are TOML files.
			front, body, err := fromTOML(c, text)
			if err != nil {
				return it, err
			}
			it.Lossy = true
			if out, err = render(front, body); err != nil {
				return it, err
			}
			break
		}
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

// fromTOML reads an agent or prompt that a tool keeps as a TOML file. The
// text is the key the tool reads its instructions from; a description is
// shared with the other tools; every other key belongs to this tool alone.
func fromTOML(c Candidate, text string) (*yaml.Node, string, error) {
	fields := map[string]any{}
	if _, err := toml.Decode(text, &fields); err != nil {
		return nil, "", fmt.Errorf("not valid TOML: %w", err)
	}
	bodyKey := "developer_instructions"
	if c.Kind == KindPrompt {
		bodyKey = "prompt"
	}
	body, _ := fields[bodyKey].(string)
	if strings.TrimSpace(body) == "" {
		return nil, "", fmt.Errorf("it has no %q", bodyKey)
	}
	if c.Kind == KindPrompt {
		// Registries write $ARGUMENTS for what the user typed.
		body = strings.ReplaceAll(body, "{{args}}", "$ARGUMENTS")
	}
	description, _ := fields["description"].(string)
	if c.Kind == KindAgent && description == "" {
		return nil, "", fmt.Errorf("an agent needs a description")
	}
	delete(fields, bodyKey)
	delete(fields, "description")
	delete(fields, "name")
	front := &yaml.Node{Kind: yaml.MappingNode}
	if description != "" {
		front.Content = append(front.Content, str("description"), str(description))
	}
	if len(fields) > 0 {
		own := &yaml.Node{}
		if err := own.Encode(fields); err != nil {
			return nil, "", err
		}
		front.Content = append(front.Content, str(c.Target), own)
	}
	return front, strings.Trim(body, "\n") + "\n", nil
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

var frontmatterName = regexp.MustCompile(`(?m)^name:.*$`)

// Reconcile fits an item to what the registry already holds. When the
// registry has a resource of that kind and name, the item becomes an
// update of it: it goes where the registry keeps it, and what the tool's
// form cannot carry (another tool's settings, a description the tool does
// not store) is taken over from the registry's version. regName is the
// registry's name, used to recognise a name that was installed with it as
// a prefix.
func Reconcile(snap *registry.Snapshot, regName string, it *Item) error {
	names := []string{it.Name}
	if n, ok := strings.CutPrefix(it.Name, regName+"-"); ok && it.Managed {
		names = append(names, n)
	}
	pick := func(has func(name string) bool) string {
		for _, n := range names {
			if has(n) {
				return n
			}
		}
		return ""
	}
	switch it.Kind {
	case KindHook:
		if slices.ContainsFunc(snap.AllHooks, func(h registry.Hook) bool { return h.Name == it.Name }) {
			return fmt.Errorf("the registry already has a hook named %q; choose another name with --name", it.Name)
		}
		return nil
	case KindMCP:
		return reconcileMCP(snap, it)
	case KindSkill:
		var orig registry.Skill
		name := pick(func(n string) bool {
			i := slices.IndexFunc(snap.All, func(s registry.Skill) bool { return s.Name == n })
			if i >= 0 {
				orig = snap.All[i]
			}
			return i >= 0
		})
		if name == "" {
			return nil
		}
		rel, err := filepath.Rel(snap.Root, orig.Dir)
		if err != nil {
			return err
		}
		dest := filepath.ToSlash(rel)
		for i := range it.Files {
			f := &it.Files[i]
			f.Path = dest + strings.TrimPrefix(f.Path, it.Dest)
			if name != it.Name && f.Path == dest+"/"+registry.SkillFile {
				// Installed under a prefixed name; the registry knows it
				// by its own.
				f.Data = frontmatterName.ReplaceAll(f.Data, []byte("name: "+name))
			}
		}
		it.Dest, it.Update, it.Remove = dest, true, []string{dest}
	case KindInstruction:
		i := -1
		if pick(func(n string) bool {
			i = slices.IndexFunc(snap.AllInstructions, func(x registry.Instruction) bool { return x.Name == n })
			return i >= 0
		}) == "" {
			return nil
		}
		orig := snap.AllInstructions[i]
		local, err := registry.ParseInstruction(it.Name, string(it.Files[0].Data))
		if err != nil {
			return err
		}
		if local.Description == "" {
			local.Description = orig.Description
		}
		data, err := renderInstruction(local)
		if err != nil {
			return err
		}
		it.Dest, it.Update = orig.File, true
		it.Files = []gitx.File{{Path: orig.File, Data: data}}
	case KindAgent, KindPrompt:
		docs := snap.AllAgents
		if it.Kind == KindPrompt {
			docs = snap.AllPrompts
		}
		i := -1
		if pick(func(n string) bool {
			i = slices.IndexFunc(docs, func(d registry.Doc) bool { return d.Name == n })
			return i >= 0
		}) == "" {
			return nil
		}
		orig := docs[i]
		local, err := registry.ParseDoc(it.Name, string(it.Files[0].Data))
		if err != nil {
			return err
		}
		front := local.Front
		if front == nil {
			front = &yaml.Node{Kind: yaml.MappingNode}
		}
		// The local file was written for one tool. What the registry holds
		// for the other tools is not in it and must not be lost.
		var kept []string
		if orig.Front != nil {
			for j := 0; j+1 < len(orig.Front.Content); j += 2 {
				key := orig.Front.Content[j]
				has := false
				for k := 0; k+1 < len(front.Content); k += 2 {
					has = has || front.Content[k].Value == key.Value
				}
				// Another tool's section is never in the local file. With
				// a lossy form the shared fields are missing too.
				if !has && key.Value != it.Target && (target.Known(key.Value) || it.Lossy) {
					front.Content = append(front.Content, key, orig.Front.Content[j+1])
					kept = append(kept, key.Value)
				}
			}
		}
		if len(kept) > 0 {
			it.Notes = append(it.Notes, fmt.Sprintf("kept from the registry's version, because your %s copy does not hold them: %s", it.Target, strings.Join(kept, ", ")))
		}
		data, err := render(front, local.Body)
		if err != nil {
			return err
		}
		it.Dest, it.Update = orig.File, true
		it.Files = []gitx.File{{Path: orig.File, Data: data}}
	}
	return nil
}

func renderInstruction(ins registry.Instruction) ([]byte, error) {
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
	return render(front, strings.TrimLeft(ins.Body, "\n"))
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

// GitHubRepo returns the repository in the form the gh command takes,
// "owner/name" or "host/owner/name", and false when the URL is not a
// GitHub one.
func GitHubRepo(repo string) (string, bool) {
	link := PullRequestURL(repo, "x")
	rest, ok := strings.CutPrefix(link, "https://")
	if !ok || !strings.Contains(link, "/compare/") {
		return "", false
	}
	rest = rest[:strings.Index(rest, "/compare/")]
	if slug, ok := strings.CutPrefix(rest, "github.com/"); ok {
		return slug, true
	}
	return rest, true
}
