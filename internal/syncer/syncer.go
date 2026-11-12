// Package syncer brings the target directories in line with the subscribed
// registries.
package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/blocks"
	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/fsutil"
	"github.com/graemer-org/spellbook/internal/lock"
	"github.com/graemer-org/spellbook/internal/registry"
	"github.com/graemer-org/spellbook/internal/state"
	"github.com/graemer-org/spellbook/internal/target"
)

// Kinds of state entries.
const (
	// KindSkill is a skill directory.
	KindSkill = "skill"
	// KindInstruction is an instruction in a file of its own.
	KindInstruction = "instruction"
	// KindBlock is an instruction kept as a marked block inside a file that
	// may also hold the user's text. Its state path is "<file>#<block id>".
	KindBlock = "instruction-block"
	// KindAgent is an agent definition file.
	KindAgent = "agent"
	// KindPrompt is a prompt: a file, or a skill directory for a tool that
	// runs prompts as skills.
	KindPrompt = "prompt"
)

// FetchFunc fetches one registry. Tests replace it.
type FetchFunc func(ctx context.Context, r config.Registry) (*registry.Snapshot, error)

// Options control one sync run.
type Options struct {
	// DryRun reports what would happen without writing anything.
	DryRun bool
	// Force overwrites and removes managed resources that were edited locally.
	Force bool
	// Locked installs the commits recorded in the lock file instead of the
	// newest ones.
	Locked bool
	// Fetch defaults to registry.Fetch.
	Fetch FetchFunc
}

// RegistryReport says how fetching one registry went.
type RegistryReport struct {
	Name   string
	Commit string
	// Previous is the commit of the last sync, empty on the first one.
	Previous     string
	Skills       int
	Instructions int
	Agents       int
	Prompts      int
	Err          error
}

// Counts summarises the changes made to one target.
type Counts struct {
	Installed, Updated, Unchanged, Removed, Skipped int
}

// Report is the outcome of a sync run.
type Report struct {
	Registries []RegistryReport
	Targets    map[string]*Counts
	Warnings   []string
}

// Failed reports whether any registry could not be fetched.
func (r *Report) Failed() bool {
	for _, reg := range r.Registries {
		if reg.Err != nil {
			return true
		}
	}
	return false
}

// item is a file or directory that belongs to one resource alone.
type item struct {
	abs      string
	target   string
	kind     string
	name     string
	registry string
	commit   string
	hash     string
	// dir is the directory to copy for a skill; content is what to write
	// for a file.
	dir     string
	content []byte
}

func (it item) write() error {
	if it.dir != "" {
		return fsutil.ReplaceDir(it.dir, it.abs)
	}
	return writeFile(it.abs, it.content)
}

// block is an instruction that becomes a marked section of a shared file.
type block struct {
	id       string
	target   string
	name     string
	registry string
	commit   string
	body     string
}

// resource is a skill or instruction that won its name.
type resource struct {
	name     string
	registry string
	commit   string
	// Skills.
	dir  string
	hash string
	// Instructions.
	ins registry.Instruction
	// Agents and prompts.
	doc registry.Doc
}

// run carries the state of one sync.
type run struct {
	loc    config.Location
	opts   Options
	rep    *Report
	old    map[string]state.Entry
	failed map[string]bool
	// entries is the state after the sync.
	entries []state.Entry
}

func (r *run) counts(t string) *Counts {
	if r.rep.Targets[t] == nil {
		r.rep.Targets[t] = &Counts{}
	}
	return r.rep.Targets[t]
}

func (r *run) warnf(format string, args ...any) {
	r.rep.Warnings = append(r.rep.Warnings, fmt.Sprintf(format, args...))
}

// Run fetches every registry of cfg and installs, updates and removes
// resources so the enabled targets match. It only ever touches paths and
// blocks that are recorded in the state file of loc.
func Run(ctx context.Context, loc config.Location, cfg *config.Config, opts Options) (*Report, error) {
	fetch := opts.Fetch
	if fetch == nil {
		fetch = registry.Fetch
	}
	st, err := state.Load(loc.StatePath)
	if err != nil {
		return nil, err
	}
	lk, err := lock.Load(loc.LockPath)
	if err != nil {
		return nil, err
	}
	r := &run{loc: loc, opts: opts, rep: &Report{Targets: map[string]*Counts{}}, old: st.ByPath(), failed: map[string]bool{}}
	var locked []lock.Entry

	// Skills installed under a prefixed name are staged here first, because
	// their frontmatter has to be rewritten before they are hashed.
	staging, err := os.MkdirTemp("", "spellbook-stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	// Higher priority first; config order breaks ties.
	regs := append([]config.Registry(nil), cfg.Registries...)
	sort.SliceStable(regs, func(i, j int) bool { return regs[i].Priority > regs[j].Priority })

	var skills, instructions, agents, prompts []resource
	skillNames, instructionNames := map[string]string{}, map[string]string{}
	agentNames, promptNames := map[string]string{}, map[string]string{}
	for _, reg := range regs {
		prev, hasPrev := lk.Get(reg.Name)
		if hasPrev && prev.URL != reg.URL {
			hasPrev = false
		}
		fail := func(err error) {
			r.failed[reg.Name] = true
			r.rep.Registries = append(r.rep.Registries, RegistryReport{Name: reg.Name, Err: err})
			if hasPrev {
				locked = append(locked, prev)
			}
		}
		at := reg
		if opts.Locked {
			if !hasPrev {
				fail(fmt.Errorf("not in %s; run 'spellbook sync' without --locked first", config.LockFile))
				continue
			}
			at.Ref = prev.Commit
		}
		snap, err := fetch(ctx, at)
		if err != nil {
			fail(err)
			continue
		}
		rr := RegistryReport{Name: reg.Name, Commit: snap.Commit, Skills: len(snap.Skills), Instructions: len(snap.Instructions), Agents: len(snap.Agents), Prompts: len(snap.Prompts)}
		if hasPrev {
			rr.Previous = prev.Commit
		}
		r.rep.Registries = append(r.rep.Registries, rr)
		locked = append(locked, lock.Entry{Name: reg.Name, URL: reg.URL, Ref: reg.Ref, Commit: snap.Commit})
		for _, w := range snap.Warnings {
			r.warnf("%s: %s", reg.Name, w)
		}

		for _, s := range snap.Skills {
			name, ok := r.claim(skillNames, reg, "skill", s.Name)
			if !ok {
				continue
			}
			src := s.Dir
			if name != s.Name {
				src = filepath.Join(staging, name)
				if err := fsutil.CopyDir(s.Dir, src); err != nil {
					return nil, fmt.Errorf("stage skill %q of %s: %w", s.Name, reg.Name, err)
				}
				if err := registry.RewriteName(src, name); err != nil {
					return nil, fmt.Errorf("stage skill %q of %s: %w", s.Name, reg.Name, err)
				}
			}
			hash, err := fsutil.HashDir(src)
			if err != nil {
				return nil, fmt.Errorf("hash skill %q of %s: %w", s.Name, reg.Name, err)
			}
			skills = append(skills, resource{name: name, registry: reg.Name, commit: snap.Commit, dir: src, hash: hash})
		}
		for _, ins := range snap.Instructions {
			name, ok := r.claim(instructionNames, reg, "instruction", ins.Name)
			if !ok {
				continue
			}
			instructions = append(instructions, resource{name: name, registry: reg.Name, commit: snap.Commit, ins: ins})
		}
		for _, doc := range snap.Agents {
			if name, ok := r.claim(agentNames, reg, "agent", doc.Name); ok {
				agents = append(agents, resource{name: name, registry: reg.Name, commit: snap.Commit, doc: doc})
			}
		}
		for _, doc := range snap.Prompts {
			if name, ok := r.claim(promptNames, reg, "prompt", doc.Name); ok {
				prompts = append(prompts, resource{name: name, registry: reg.Name, commit: snap.Commit, doc: doc})
			}
		}
	}

	// Work out what each target should contain.
	want := map[string]item{}
	wantBlocks := map[string][]block{}
	add := func(it item) {
		key := pathKey(loc.Root, it.abs)
		if _, dup := want[key]; !dup {
			// A duplicate means two targets share one directory.
			want[key] = it
		}
	}
	for _, tname := range cfg.EnabledTargets() {
		r.counts(tname)
		dir, err := target.SkillsDir(tname, cfg.Targets[tname], loc)
		if err != nil {
			return nil, err
		}
		for _, s := range skills {
			add(item{abs: filepath.Join(dir, s.name), target: tname, kind: KindSkill, name: s.name, registry: s.registry, commit: s.commit, hash: s.hash, dir: s.dir})
		}
		for _, in := range instructions {
			place, err := target.InstructionPlace(tname, loc, in.name, in.ins.Scoped())
			if err != nil {
				return nil, err
			}
			content := target.RenderInstruction(tname, in.registry, in.ins.Paths, in.ins.Body, place.Block)
			if place.Block {
				wantBlocks[place.File] = append(wantBlocks[place.File], block{
					id: in.registry + "/" + in.name, target: tname, name: in.name, registry: in.registry, commit: in.commit, body: string(content),
				})
				continue
			}
			add(item{abs: place.File, target: tname, kind: KindInstruction, name: in.name, registry: in.registry, commit: in.commit, hash: hashBytes(content), content: content})
		}
		for _, a := range agents {
			file, err := target.AgentFile(tname, loc, a.name)
			if err != nil {
				return nil, err
			}
			// Claude Code identifies an agent by its name field, so it always
			// carries the name it is installed under.
			content, err := target.RenderDoc(tname, a.doc.Front, a.doc.Body, [][2]string{{"name", a.name}})
			if err != nil {
				r.warnf("%s: skipped agent %q of %s: %v", tname, a.name, a.registry, err)
				continue
			}
			add(item{abs: file, target: tname, kind: KindAgent, name: a.name, registry: a.registry, commit: a.commit, hash: hashBytes(content), content: content})
		}
		unsupported := 0
		for _, p := range prompts {
			place, err := target.PromptFor(tname, cfg.Targets[tname], loc, p.name)
			if err != nil {
				return nil, err
			}
			if !place.Supported {
				unsupported++
				continue
			}
			if !place.AsSkill {
				content, err := target.RenderDoc(tname, p.doc.Front, p.doc.Body, nil)
				if err != nil {
					r.warnf("%s: skipped prompt %q of %s: %v", tname, p.name, p.registry, err)
					continue
				}
				add(item{abs: place.Path, target: tname, kind: KindPrompt, name: p.name, registry: p.registry, commit: p.commit, hash: hashBytes(content), content: content})
				continue
			}
			if owner, clash := skillNames[p.name]; clash {
				r.warnf("%s: skipped prompt %q of %s: a skill of that name is provided by %q", tname, p.name, p.registry, owner)
				continue
			}
			set := [][2]string{{"name", p.name}}
			if p.doc.Field("disable-model-invocation") == "" {
				// A prompt is something the user runs, not something the
				// model picks up on its own.
				set = append(set, [2]string{"disable-model-invocation", "true"})
			}
			content, err := target.RenderDoc(tname, p.doc.Front, p.doc.Body, set)
			if err != nil {
				r.warnf("%s: skipped prompt %q of %s: %v", tname, p.name, p.registry, err)
				continue
			}
			src := filepath.Join(staging, "prompt-"+tname+"-"+p.name)
			if err := writeFile(filepath.Join(src, registry.SkillFile), content); err != nil {
				return nil, err
			}
			hash, err := fsutil.HashDir(src)
			if err != nil {
				return nil, err
			}
			add(item{abs: place.Path, target: tname, kind: KindPrompt, name: p.name, registry: p.registry, commit: p.commit, hash: hash, dir: src})
		}
		if unsupported > 0 {
			r.warnf("%s: skipped %d prompts: this tool only reads prompts from a project, use --project", tname, unsupported)
		}
	}

	if err := r.applyItems(want); err != nil {
		return nil, err
	}
	if err := r.applyBlocks(wantBlocks); err != nil {
		return nil, err
	}

	if !opts.DryRun {
		st.Entries = r.entries
		if err := st.Save(loc.StatePath); err != nil {
			return nil, err
		}
		lk.Registries = locked
		if err := lk.Save(loc.LockPath); err != nil {
			return nil, err
		}
	}
	sort.Strings(r.rep.Warnings)
	return r.rep, nil
}

// claim reserves the name a resource is installed under and returns it.
// A name that is already taken by a registry with priority is given up,
// unless the registry asks for a prefix in that case.
func (r *run) claim(taken map[string]string, reg config.Registry, kind, name string) (string, bool) {
	install := name
	if reg.Prefix == config.PrefixAll {
		install = reg.Name + "-" + name
	}
	if _, clash := taken[install]; clash && reg.Prefix == config.PrefixCollisions {
		install = reg.Name + "-" + name
	}
	if owner, clash := taken[install]; clash {
		r.warnf("%s: %s %q is also provided by %q, which has priority", reg.Name, kind, install, owner)
		return "", false
	}
	if !config.ValidName(install) {
		r.warnf("%s: skipped %s %q: the prefixed name %q is not a valid name", reg.Name, kind, name, install)
		return "", false
	}
	taken[install] = reg.Name
	return install, true
}

// applyItems installs and updates the wanted files and directories and
// removes the recorded ones that are no longer wanted.
func (r *run) applyItems(want map[string]item) error {
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		it := want[key]
		c := r.counts(it.target)
		entry := state.Entry{Path: key, Kind: it.kind, Name: it.name, Target: it.target, Registry: it.registry, Commit: it.commit, Hash: it.hash}
		prev, managed := r.old[key]

		if _, err := os.Lstat(it.abs); os.IsNotExist(err) {
			if !r.opts.DryRun {
				if err := it.write(); err != nil {
					return fmt.Errorf("install %s: %w", it.abs, err)
				}
			}
			c.Installed++
			r.entries = append(r.entries, entry)
			continue
		} else if err != nil {
			return err
		}

		if !managed {
			c.Skipped++
			r.warnf("%s: skipped %s %q: %s exists and is not managed by spellbook", it.target, it.kind, it.name, it.abs)
			continue
		}
		onDisk, err := hashPath(it.abs)
		if err != nil {
			return err
		}
		switch {
		case onDisk == it.hash:
			c.Unchanged++
			r.entries = append(r.entries, entry)
		case onDisk != prev.Hash && !r.opts.Force:
			c.Skipped++
			r.warnf("%s: skipped %s %q: %s was edited locally (use --force to overwrite)", it.target, it.kind, it.name, it.abs)
			r.entries = append(r.entries, prev)
		default:
			if !r.opts.DryRun {
				if err := it.write(); err != nil {
					return fmt.Errorf("update %s: %w", it.abs, err)
				}
			}
			c.Updated++
			r.entries = append(r.entries, entry)
		}
	}

	for key, e := range r.old {
		if e.Kind == KindBlock {
			continue
		}
		if _, keep := want[key]; keep {
			continue
		}
		if r.failed[e.Registry] {
			// The registry could not be read, so nothing is known about
			// what it still offers. Leave its resources alone.
			r.entries = append(r.entries, e)
			continue
		}
		abs := absPath(r.loc.Root, key)
		if _, err := os.Lstat(abs); os.IsNotExist(err) {
			continue
		}
		c := r.counts(e.Target)
		onDisk, err := hashPath(abs)
		if err != nil {
			return err
		}
		if onDisk != e.Hash && !r.opts.Force {
			c.Skipped++
			r.warnf("%s: kept %s %q: %s was edited locally (use --force to remove)", e.Target, e.Kind, e.Name, abs)
			r.entries = append(r.entries, e)
			continue
		}
		if !r.opts.DryRun {
			if err := os.RemoveAll(abs); err != nil {
				return fmt.Errorf("remove %s: %w", abs, err)
			}
		}
		c.Removed++
	}
	return nil
}

// applyBlocks rewrites the marked blocks of every shared file that should
// have blocks or that has recorded ones. Text outside the blocks, and
// blocks that are not in the state file, stay as they are.
func (r *run) applyBlocks(want map[string][]block) error {
	files := map[string]bool{}
	for abs := range want {
		files[abs] = true
	}
	for key, e := range r.old {
		if e.Kind == KindBlock {
			if i := strings.LastIndex(key, "#"); i >= 0 {
				files[absPath(r.loc.Root, key[:i])] = true
			}
		}
	}
	paths := make([]string, 0, len(files))
	for abs := range files {
		paths = append(paths, abs)
	}
	sort.Strings(paths)

	for _, abs := range paths {
		fileKey := pathKey(r.loc.Root, abs)
		data, err := os.ReadFile(abs)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		text := string(data)
		found, before, after, err := blocks.Parse(text)
		if err != nil {
			return fmt.Errorf("%s: %w", abs, err)
		}
		onDisk := map[string]string{}
		for _, b := range found {
			onDisk[b.ID] = b.Body
		}

		var out []blocks.Block
		wanted := map[string]bool{}
		for _, b := range want[abs] {
			wanted[b.id] = true
			key := fileKey + "#" + b.id
			hash := hashBytes([]byte(b.body))
			entry := state.Entry{Path: key, Kind: KindBlock, Name: b.name, Target: b.target, Registry: b.registry, Commit: b.commit, Hash: hash}
			c := r.counts(b.target)
			prev, managed := r.old[key]
			body, exists := onDisk[b.id]
			switch {
			case !exists:
				c.Installed++
			case hashBytes([]byte(body)) == hash:
				c.Unchanged++
			case managed && hashBytes([]byte(body)) != prev.Hash && !r.opts.Force:
				c.Skipped++
				r.warnf("%s: skipped instruction %q: its block in %s was edited locally (use --force to overwrite)", b.target, b.name, abs)
				out = append(out, blocks.Block{ID: b.id, Body: body})
				r.entries = append(r.entries, prev)
				continue
			default:
				c.Updated++
			}
			out = append(out, blocks.Block{ID: b.id, Body: b.body})
			r.entries = append(r.entries, entry)
		}

		for _, b := range found {
			if wanted[b.ID] {
				continue
			}
			prev, managed := r.old[fileKey+"#"+b.ID]
			switch {
			case !managed:
				// Not recorded, so not ours to remove.
				out = append(out, b)
			case r.failed[prev.Registry]:
				out = append(out, b)
				r.entries = append(r.entries, prev)
			case hashBytes([]byte(b.Body)) != prev.Hash && !r.opts.Force:
				r.counts(prev.Target).Skipped++
				r.warnf("%s: kept instruction %q: its block in %s was edited locally (use --force to remove)", prev.Target, prev.Name, abs)
				out = append(out, b)
				r.entries = append(r.entries, prev)
			default:
				r.counts(prev.Target).Removed++
			}
		}

		updated := blocks.Render(out, before, after)
		if updated == text || r.opts.DryRun {
			continue
		}
		if strings.TrimSpace(updated) == "" {
			// The file held nothing but blocks, so it goes with them.
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := writeFile(abs, []byte(updated)); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".spellbook-tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hashPath hashes a directory like fsutil.HashDir and a file by content.
func hashPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return fsutil.HashDir(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

// pathKey returns abs relative to root when it lies inside root, and abs
// itself otherwise.
func pathKey(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return abs
	}
	return filepath.ToSlash(rel)
}

func absPath(root, key string) string {
	if filepath.IsAbs(key) {
		return key
	}
	return filepath.Join(root, filepath.FromSlash(key))
}
