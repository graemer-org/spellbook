package syncer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/state"
	"github.com/graemer-org/spellbook/internal/target"
)

// tomlServers is the table that holds the MCP servers in a TOML
// configuration file.
const tomlServers = "mcp_servers"

// applyMCPTOML brings the MCP servers of every TOML configuration file in
// line. The ownership rules are those of applyMCP: only a server recorded
// in the state file is changed or removed, and never one that was edited.
// Everything else in the file, comments included, stays as it is.
func (r *run) applyMCPTOML(want map[target.MCPPlace][]mcpWant) error {
	files := map[string][]mcpWant{}
	for p, w := range want {
		if p.TOML {
			files[p.File] = w
		}
	}
	recorded := map[string][]state.Entry{}
	for key, e := range r.old {
		if e.Kind != KindMCPTOML {
			continue
		}
		i := strings.LastIndex(key, "#")
		if i < 0 {
			continue
		}
		abs := absPath(r.loc.Root, key[:i])
		recorded[abs] = append(recorded[abs], e)
		if _, ok := files[abs]; !ok {
			files[abs] = nil
		}
	}
	ordered := make([]string, 0, len(files))
	for abs := range files {
		ordered = append(ordered, abs)
	}
	sort.Strings(ordered)

	for _, abs := range ordered {
		fileKey := pathKey(r.loc.Root, abs)
		f, err := readTOML(abs)
		if err != nil {
			return err
		}
		changed := false
		keep := map[string]bool{}
		for _, w := range files[abs] {
			keep[w.name] = true
			key := fileKey + "#" + w.name
			path := []string{tomlServers, w.name}
			c := r.counts(w.target)
			source := hashBytes(w.entry)
			prev, managed := r.old[key]
			onDisk, exists := f.hash(path...)
			asked := prev.Source
			if asked == "" {
				asked = prev.Hash
			}
			switch {
			case exists && !managed:
				c.Skipped++
				r.warnf("%s: skipped MCP server %q: %s already has a server of that name that spellbook does not manage", w.target, w.name, abs)
				continue
			case exists && onDisk != prev.Hash && !r.opts.Force:
				c.Skipped++
				r.warnf("%s: skipped MCP server %q: it was edited in %s (use --force to overwrite)", w.target, w.name, abs)
				r.entries = append(r.entries, prev)
				continue
			case exists && asked == source && onDisk == prev.Hash:
				c.Unchanged++
				prev.Registry, prev.Commit = w.registry, w.commit
				r.entries = append(r.entries, prev)
				continue
			}
			block, err := renderTOMLTable(path, fmt.Sprintf("Installed by spellbook from registry %q. Change it there, not here.", w.registry), w.entry)
			if err == nil {
				err = f.put(path, block)
			}
			if err != nil {
				c.Skipped++
				r.warnf("%s: skipped MCP server %q: %v", w.target, w.name, err)
				if managed {
					r.entries = append(r.entries, prev)
				}
				continue
			}
			changed = true
			if exists {
				c.Updated++
			} else {
				c.Installed++
			}
			entry := state.Entry{Path: key, Kind: KindMCPTOML, Name: w.name, Target: w.target, Registry: w.registry, Commit: w.commit}
			// TOML may hold a value in another form than JSON, so the hash
			// is taken from the file.
			entry.Hash, _ = f.hash(path...)
			if entry.Hash != source {
				entry.Source = source
			}
			r.entries = append(r.entries, entry)
		}

		for _, e := range recorded[abs] {
			if keep[e.Name] {
				continue
			}
			path := []string{tomlServers, e.Name}
			onDisk, exists := f.hash(path...)
			switch {
			case r.failed[e.Registry]:
				r.entries = append(r.entries, e)
			case !exists:
				// Already gone.
			case e.Pending:
				// Pushed to the registry, which does not offer it yet, or
				// may not install it here.
				r.entries = append(r.entries, e)
			case onDisk != e.Hash && !r.opts.Force:
				r.counts(e.Target).Skipped++
				r.warnf("%s: kept MCP server %q: it was edited in %s (use --force to remove)", e.Target, e.Name, abs)
				r.entries = append(r.entries, e)
			default:
				if err := f.remove(path); err != nil {
					r.counts(e.Target).Skipped++
					r.warnf("%s: kept MCP server %q: %v", e.Target, e.Name, err)
					r.entries = append(r.entries, e)
					continue
				}
				changed = true
				r.counts(e.Target).Removed++
			}
		}
		if changed && !r.opts.DryRun {
			if err := f.write(); err != nil {
				return err
			}
		}
	}
	return nil
}

// TOMLServers returns the MCP servers in a TOML configuration file as the
// tables the tool reads, and the hash the state file records for each.
func TOMLServers(file string) (map[string]map[string]any, map[string]string, error) {
	f, err := readTOML(file)
	if err != nil {
		return nil, nil, err
	}
	all, _ := f.table(tomlServers)
	servers := map[string]map[string]any{}
	hashes := map[string]string{}
	for name, v := range all {
		if table, ok := v.(map[string]any); ok {
			servers[name] = table
			hashes[name], _ = f.hash(tomlServers, name)
		}
	}
	return servers, hashes, nil
}
