package syncer

import (
	"context"
	"fmt"
	"sort"

	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/fsutil"
	"github.com/graemer-org/spellbook/internal/gitx"
	"github.com/graemer-org/spellbook/internal/lock"
	"github.com/graemer-org/spellbook/internal/registry"
)

// Diff describes how one registry changed since the commit in the lock file.
type Diff struct {
	Name string
	// From is the locked commit, empty when the registry was never synced.
	From string
	To   string
	// Added, Removed and Changed name the selected resources that differ:
	// skills by their name, instructions as "instructions/<name>".
	Added, Removed, Changed []string
	// Patch is the textual difference, filled when it was asked for.
	Patch string
}

// HasChanges reports whether the registry offers anything different.
func (d Diff) HasChanges() bool {
	return len(d.Added)+len(d.Removed)+len(d.Changed) > 0
}

// Compare fetches the registry at its locked commit and at its newest one
// and reports which of the selected skills differ. It changes neither the
// targets nor the lock file.
func Compare(ctx context.Context, loc config.Location, r config.Registry, patch bool) (*Diff, error) {
	lk, err := lock.Load(loc.LockPath)
	if err != nil {
		return nil, err
	}
	d := &Diff{Name: r.Name}
	old := map[string]string{}
	if e, ok := lk.Get(r.Name); ok && e.URL == r.URL {
		d.From = e.Commit
		at := r
		at.Ref = e.Commit
		snap, err := registry.Fetch(ctx, at)
		if err != nil {
			return nil, fmt.Errorf("fetch locked commit: %w", err)
		}
		if old, err = hashes(snap); err != nil {
			return nil, err
		}
	}
	snap, err := registry.Fetch(ctx, r)
	if err != nil {
		return nil, err
	}
	d.To = snap.Commit
	cur, err := hashes(snap)
	if err != nil {
		return nil, err
	}
	for name, h := range cur {
		if prev, ok := old[name]; !ok {
			d.Added = append(d.Added, name)
		} else if prev != h {
			d.Changed = append(d.Changed, name)
		}
	}
	for name := range old {
		if _, ok := cur[name]; !ok {
			d.Removed = append(d.Removed, name)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Changed)
	if patch && d.From != "" && d.From != d.To {
		if d.Patch, err = gitx.Diff(ctx, snap.Dir, d.From, d.To, r.Path, false); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func hashes(snap *registry.Snapshot) (map[string]string, error) {
	m := map[string]string{}
	for _, s := range snap.Skills {
		h, err := fsutil.HashDir(s.Dir)
		if err != nil {
			return nil, err
		}
		m[s.Name] = h
	}
	for _, in := range snap.Instructions {
		m[registry.InstructionsDir+"/"+in.Name] = in.Hash()
	}
	for _, d := range snap.Agents {
		m[registry.AgentsDir+"/"+d.Name] = d.Hash()
	}
	for _, srv := range snap.MCP {
		m[registry.MCPDir+"/"+srv.Name] = srv.Hash()
	}
	for _, d := range snap.Prompts {
		m[registry.PromptsDir+"/"+d.Name] = d.Hash()
	}
	return m, nil
}
