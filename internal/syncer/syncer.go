// Package syncer brings the target directories in line with the subscribed
// registries.
package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/fsutil"
	"github.com/graemer-org/spellbook/internal/registry"
	"github.com/graemer-org/spellbook/internal/state"
	"github.com/graemer-org/spellbook/internal/target"
)

// KindSkill is the state kind of a skill directory.
const KindSkill = "skill"

// FetchFunc fetches one registry. Tests replace it.
type FetchFunc func(ctx context.Context, r config.Registry) (*registry.Snapshot, error)

// Options control one sync run.
type Options struct {
	// DryRun reports what would happen without writing anything.
	DryRun bool
	// Force overwrites and removes managed resources that were edited locally.
	Force bool
	// Fetch defaults to registry.Fetch.
	Fetch FetchFunc
}

// RegistryReport says how fetching one registry went.
type RegistryReport struct {
	Name   string
	Commit string
	Skills int
	Err    error
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

type desired struct {
	abs      string
	target   string
	name     string
	src      string
	hash     string
	registry string
	commit   string
}

// Run fetches every registry of cfg and installs, updates and removes
// resources so the enabled targets match. It only ever touches paths that
// are recorded in the state file of loc.
func Run(ctx context.Context, loc config.Location, cfg *config.Config, opts Options) (*Report, error) {
	fetch := opts.Fetch
	if fetch == nil {
		fetch = registry.Fetch
	}
	rep := &Report{Targets: map[string]*Counts{}}

	st, err := state.Load(loc.StatePath)
	if err != nil {
		return nil, err
	}

	// Higher priority first; config order breaks ties.
	regs := append([]config.Registry(nil), cfg.Registries...)
	sort.SliceStable(regs, func(i, j int) bool { return regs[i].Priority > regs[j].Priority })

	type winner struct {
		skill    registry.Skill
		registry string
		commit   string
		hash     string
	}
	winners := map[string]*winner{}
	failed := map[string]bool{}
	for _, r := range regs {
		snap, err := fetch(ctx, r)
		if err != nil {
			failed[r.Name] = true
			rep.Registries = append(rep.Registries, RegistryReport{Name: r.Name, Err: err})
			continue
		}
		rep.Registries = append(rep.Registries, RegistryReport{Name: r.Name, Commit: snap.Commit, Skills: len(snap.Skills)})
		for _, w := range snap.Warnings {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: %s", r.Name, w))
		}
		for _, s := range snap.Skills {
			if w, taken := winners[s.Name]; taken {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: skill %q is also provided by %q, which has priority", r.Name, s.Name, w.registry))
				continue
			}
			hash, err := fsutil.HashDir(s.Dir)
			if err != nil {
				return nil, fmt.Errorf("hash skill %q of %s: %w", s.Name, r.Name, err)
			}
			winners[s.Name] = &winner{skill: s, registry: r.Name, commit: snap.Commit, hash: hash}
		}
	}

	want := map[string]desired{}
	for _, tname := range cfg.EnabledTargets() {
		dir, err := target.SkillsDir(tname, cfg.Targets[tname], loc)
		if err != nil {
			return nil, err
		}
		rep.Targets[tname] = &Counts{}
		for name, w := range winners {
			abs := filepath.Join(dir, name)
			key := pathKey(loc.Root, abs)
			if _, dup := want[key]; dup {
				// Two targets configured to share one directory.
				continue
			}
			want[key] = desired{abs: abs, target: tname, name: name, src: w.skill.Dir, hash: w.hash, registry: w.registry, commit: w.commit}
		}
	}

	old := st.ByPath()
	var entries []state.Entry
	counts := func(t string) *Counts {
		if rep.Targets[t] == nil {
			rep.Targets[t] = &Counts{}
		}
		return rep.Targets[t]
	}

	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		d := want[key]
		c := counts(d.target)
		entry := state.Entry{Path: key, Kind: KindSkill, Name: d.name, Target: d.target, Registry: d.registry, Commit: d.commit, Hash: d.hash}
		prev, managed := old[key]

		if _, err := os.Lstat(d.abs); os.IsNotExist(err) {
			if !opts.DryRun {
				if err := fsutil.ReplaceDir(d.src, d.abs); err != nil {
					return nil, fmt.Errorf("install %s: %w", d.abs, err)
				}
			}
			c.Installed++
			entries = append(entries, entry)
			continue
		} else if err != nil {
			return nil, err
		}

		if !managed {
			c.Skipped++
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: skipped %q: %s exists and is not managed by spellbook", d.target, d.name, d.abs))
			continue
		}
		onDisk, err := fsutil.HashDir(d.abs)
		if err != nil {
			return nil, err
		}
		switch {
		case onDisk == d.hash:
			c.Unchanged++
			entries = append(entries, entry)
		case onDisk != prev.Hash && !opts.Force:
			c.Skipped++
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: skipped %q: %s was edited locally (use --force to overwrite)", d.target, d.name, d.abs))
			entries = append(entries, prev)
		default:
			if !opts.DryRun {
				if err := fsutil.ReplaceDir(d.src, d.abs); err != nil {
					return nil, fmt.Errorf("update %s: %w", d.abs, err)
				}
			}
			c.Updated++
			entries = append(entries, entry)
		}
	}

	// Remove what is no longer wanted.
	for key, e := range old {
		if _, keep := want[key]; keep {
			continue
		}
		if failed[e.Registry] {
			// The registry could not be read, so nothing is known about
			// what it still offers. Leave its resources alone.
			entries = append(entries, e)
			continue
		}
		abs := absPath(loc.Root, key)
		if _, err := os.Lstat(abs); os.IsNotExist(err) {
			continue
		}
		c := counts(e.Target)
		onDisk, err := fsutil.HashDir(abs)
		if err != nil {
			return nil, err
		}
		if onDisk != e.Hash && !opts.Force {
			c.Skipped++
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: kept %q: %s was edited locally (use --force to remove)", e.Target, e.Name, abs))
			entries = append(entries, e)
			continue
		}
		if !opts.DryRun {
			if err := os.RemoveAll(abs); err != nil {
				return nil, fmt.Errorf("remove %s: %w", abs, err)
			}
		}
		c.Removed++
	}

	if !opts.DryRun {
		st.Entries = entries
		if err := st.Save(loc.StatePath); err != nil {
			return nil, err
		}
	}
	sort.Strings(rep.Warnings)
	return rep, nil
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
