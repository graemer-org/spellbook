package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/ojson"
	"github.com/graemer-org/spellbook/internal/state"
	"github.com/graemer-org/spellbook/internal/target"
)

// mcpKey is the key of the object that holds the servers in every MCP
// configuration file.
const mcpKey = "mcpServers"

// mcpWant is an MCP server that should be present for one target.
type mcpWant struct {
	name     string
	target   string
	registry string
	commit   string
	// entry is the server definition as canonical JSON.
	entry []byte
}

// claudeBin returns the Claude Code command. SPELLBOOK_CLAUDE_BIN replaces
// it in tests.
func claudeBin() string {
	if bin := os.Getenv("SPELLBOOK_CLAUDE_BIN"); bin != "" {
		return bin
	}
	return "claude"
}

func claude(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, claudeBin(), args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %s", claudeBin(), strings.Join(args[:2], " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// readServers returns the servers stored in an MCP configuration file as
// canonical JSON by name, together with the parsed file.
func readServers(path string) (*ojson.Object, *ojson.Object, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	file, err := ojson.Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s is not valid JSON, so it was left alone: %w", path, err)
	}
	servers := &ojson.Object{}
	if raw, ok := file.Get(mcpKey); ok {
		if servers, err = ojson.Parse(raw); err != nil {
			return nil, nil, fmt.Errorf("%s: %q is not an object, so the file was left alone", path, mcpKey)
		}
	}
	return file, servers, nil
}

func serverHash(servers *ojson.Object, name string) (string, bool) {
	raw, ok := servers.Get(name)
	if !ok {
		return "", false
	}
	canonical, err := ojson.Canonical(raw)
	if err != nil {
		return "", true
	}
	return hashBytes(canonical), true
}

// applyMCP brings the MCP servers of every configuration file in line.
// Only servers recorded in the state file are changed or removed; a server
// of the same name that the user added is left alone.
func (r *run) applyMCP(ctx context.Context, want map[target.MCPPlace][]mcpWant) error {
	places := map[target.MCPPlace]bool{}
	for p := range want {
		places[p] = true
	}
	recorded := map[string][]state.Entry{}
	for key, e := range r.old {
		if e.Kind != KindMCP && e.Kind != KindMCPCLI {
			continue
		}
		i := strings.LastIndex(key, "#")
		if i < 0 {
			continue
		}
		abs := absPath(r.loc.Root, key[:i])
		recorded[abs] = append(recorded[abs], e)
		places[target.MCPPlace{File: abs, ViaCLI: e.Kind == KindMCPCLI}] = true
	}
	ordered := make([]target.MCPPlace, 0, len(places))
	for p := range places {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].File < ordered[j].File })

	for _, place := range ordered {
		if place.TOML {
			continue
		}
		fileKey := pathKey(r.loc.Root, place.File)
		kind := KindMCP
		if place.ViaCLI {
			kind = KindMCPCLI
		}
		wanted := want[place]
		if place.ViaCLI && len(wanted)+len(recorded[place.File]) > 0 {
			if _, err := exec.LookPath(claudeBin()); err != nil {
				// Without the tool nothing can be changed. Keep the record.
				for _, w := range wanted {
					r.counts(w.target).Skipped++
				}
				if len(wanted) > 0 {
					r.warnf("claude: skipped %d MCP servers: the 'claude' command is needed to change user-level servers and was not found", len(wanted))
				}
				r.entries = append(r.entries, recorded[place.File]...)
				continue
			}
		}
		file, servers, err := readServers(place.File)
		if err != nil {
			return err
		}
		changed := false

		// put stores a server and returns the hash of what is on disk
		// afterwards, which a tool may have normalised.
		put := func(w mcpWant, exists bool) (string, error) {
			if r.opts.DryRun {
				return hashBytes(w.entry), nil
			}
			if !place.ViaCLI {
				servers.Set(w.name, json.RawMessage(w.entry))
				changed = true
				return hashBytes(w.entry), nil
			}
			if exists {
				if err := claude(ctx, "mcp", "remove", "--scope", "user", w.name); err != nil {
					return "", err
				}
			}
			if err := claude(ctx, "mcp", "add-json", "--scope", "user", w.name, string(w.entry)); err != nil {
				return "", err
			}
			_, after, err := readServers(place.File)
			if err != nil {
				return "", err
			}
			if h, ok := serverHash(after, w.name); ok {
				return h, nil
			}
			return "", fmt.Errorf("'claude mcp add-json' reported success, but %s has no server %q", place.File, w.name)
		}

		keep := map[string]bool{}
		for _, w := range wanted {
			keep[w.name] = true
			key := fileKey + "#" + w.name
			c := r.counts(w.target)
			source := hashBytes(w.entry)
			prev, managed := r.old[key]
			onDisk, exists := serverHash(servers, w.name)
			entry := state.Entry{Path: key, Kind: kind, Name: w.name, Target: w.target, Registry: w.registry, Commit: w.commit}
			asked := prev.Source
			if asked == "" {
				asked = prev.Hash
			}
			switch {
			case exists && managed && prev.Pending && prev.Base == source && !r.opts.Force:
				// A pushed local change, while the registry still offers
				// the version it was made from.
				c.Unchanged++
				r.entries = append(r.entries, prev)
				continue
			case exists && !managed && onDisk == source:
				// Identical to what would be written: take it over.
				c.Unchanged++
				entry.Hash = onDisk
				r.entries = append(r.entries, entry)
				continue
			case exists && !managed:
				c.Skipped++
				r.warnf("%s: skipped MCP server %q: %s already has a server of that name that spellbook does not manage", w.target, w.name, place.File)
				continue
			case exists && onDisk != prev.Hash && !r.opts.Force:
				c.Skipped++
				r.warnf("%s: skipped MCP server %q: it was edited in %s (use --force to overwrite)", w.target, w.name, place.File)
				r.entries = append(r.entries, prev)
				continue
			case exists && asked == source && onDisk == prev.Hash:
				c.Unchanged++
				prev.Registry, prev.Commit = w.registry, w.commit
				r.entries = append(r.entries, prev)
				continue
			}
			hash, err := put(w, exists)
			if err != nil {
				return err
			}
			if exists {
				c.Updated++
			} else {
				c.Installed++
			}
			entry.Hash = hash
			if hash != source {
				entry.Source = source
			}
			r.entries = append(r.entries, entry)
		}

		for _, e := range recorded[place.File] {
			if keep[e.Name] {
				continue
			}
			onDisk, exists := serverHash(servers, e.Name)
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
				r.warnf("%s: kept MCP server %q: it was edited in %s (use --force to remove)", e.Target, e.Name, place.File)
				r.entries = append(r.entries, e)
			default:
				if !r.opts.DryRun {
					if place.ViaCLI {
						if err := claude(ctx, "mcp", "remove", "--scope", "user", e.Name); err != nil {
							return err
						}
					} else {
						servers.Remove(e.Name)
						changed = true
					}
				}
				r.counts(e.Target).Removed++
			}
		}

		if !changed || place.ViaCLI {
			continue
		}
		if servers.Len() == 0 {
			file.Remove(mcpKey)
		} else {
			file.Set(mcpKey, servers.Marshal())
		}
		if file.Len() == 0 {
			// The file held nothing but these servers, so it goes with them.
			if err := os.Remove(place.File); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		out, err := file.Indented()
		if err != nil {
			return err
		}
		if err := writeFile(place.File, out); err != nil {
			return err
		}
	}
	return nil
}
