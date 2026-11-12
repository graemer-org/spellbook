package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"

	"github.com/graemer-org/spellbook/internal/config"
)

// ManifestFile is the optional file that describes a registry.
const ManifestFile = "registry.toml"

const manifestVersion = 1

// Group is a named set of resources, usually one team's.
type Group struct {
	Description string `toml:"description,omitempty"`
	// Include names other groups whose resources this group also contains.
	Include []string `toml:"include,omitempty"`
	Skills  []string `toml:"skills,omitempty"`
	// Instructions are listed in the order they should be read in.
	Instructions []string `toml:"instructions,omitempty"`
	Agents       []string `toml:"agents,omitempty"`
	Prompts      []string `toml:"prompts,omitempty"`
	MCP          []string `toml:"mcp,omitempty"`
	Hooks        []string `toml:"hooks,omitempty"`
}

// Manifest is the content of a registry.toml file.
type Manifest struct {
	Version int `toml:"version"`
	// DefaultGroups is what a subscriber gets who did not choose a group.
	DefaultGroups []string         `toml:"default_groups,omitempty"`
	Groups        map[string]Group `toml:"groups,omitempty"`
}

// LoadManifest reads registry.toml from dir. It returns nil when the
// registry has none. Keys this version does not know are reported as
// warnings and not as errors, because a registry may already describe
// resource kinds a newer spellbook understands.
func LoadManifest(dir string) (*Manifest, []string, error) {
	path := filepath.Join(dir, ManifestFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var m Manifest
	md, err := toml.Decode(string(data), &m)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	if m.Version > manifestVersion {
		return nil, nil, fmt.Errorf("%s has version %d, this spellbook understands version %d; upgrade spellbook", ManifestFile, m.Version, manifestVersion)
	}
	var warnings []string
	for _, key := range md.Undecoded() {
		warnings = append(warnings, fmt.Sprintf("%s: ignored unknown key %q", ManifestFile, key.String()))
	}
	for name, g := range m.Groups {
		if !config.ValidName(name) {
			return nil, nil, fmt.Errorf("%s: invalid group name %q", ManifestFile, name)
		}
		for _, inc := range g.Include {
			if _, ok := m.Groups[inc]; !ok {
				return nil, nil, fmt.Errorf("%s: group %q includes unknown group %q", ManifestFile, name, inc)
			}
		}
	}
	for _, name := range m.DefaultGroups {
		if _, ok := m.Groups[name]; !ok {
			return nil, nil, fmt.Errorf("%s: default_groups names unknown group %q", ManifestFile, name)
		}
	}
	// Reject include cycles up front so every later resolve terminates.
	for _, name := range m.GroupNames() {
		if _, err := m.Skills([]string{name}); err != nil {
			return nil, nil, err
		}
	}
	return &m, warnings, nil
}

// GroupNames returns the group names in a stable order.
func (m *Manifest) GroupNames() []string {
	names := make([]string, 0, len(m.Groups))
	for n := range m.Groups {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Skills returns the skill names of the given groups, including those of
// the groups they include, without duplicates.
func (m *Manifest) Skills(groups []string) ([]string, error) {
	return m.resolve(groups, func(g Group) []string { return g.Skills })
}

// Instructions returns the instruction names of the given groups. An
// included group comes before the group that includes it, so the general
// guidance of a base group precedes a team's own.
func (m *Manifest) Instructions(groups []string) ([]string, error) {
	return m.resolve(groups, func(g Group) []string { return g.Instructions })
}

// Agents returns the agent names of the given groups.
func (m *Manifest) Agents(groups []string) ([]string, error) {
	return m.resolve(groups, func(g Group) []string { return g.Agents })
}

// Prompts returns the prompt names of the given groups.
func (m *Manifest) Prompts(groups []string) ([]string, error) {
	return m.resolve(groups, func(g Group) []string { return g.Prompts })
}

// MCP returns the MCP server names of the given groups.
func (m *Manifest) MCP(groups []string) ([]string, error) {
	return m.resolve(groups, func(g Group) []string { return g.MCP })
}

// Hooks returns the hook names of the given groups.
func (m *Manifest) Hooks(groups []string) ([]string, error) {
	return m.resolve(groups, func(g Group) []string { return g.Hooks })
}

func (m *Manifest) resolve(groups []string, field func(Group) []string) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	done := map[string]bool{}
	var visit func(name string, trail []string) error
	visit = func(name string, trail []string) error {
		for _, t := range trail {
			if t == name {
				return fmt.Errorf("%s: groups include each other in a cycle: %v", ManifestFile, append(trail, name))
			}
		}
		g, ok := m.Groups[name]
		if !ok {
			return fmt.Errorf("the registry has no group %q (available: %v)", name, m.GroupNames())
		}
		if done[name] {
			return nil
		}
		for _, inc := range g.Include {
			if err := visit(inc, append(trail, name)); err != nil {
				return err
			}
		}
		for _, n := range field(g) {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
		done[name] = true
		return nil
	}
	for _, g := range groups {
		if err := visit(g, nil); err != nil {
			return nil, err
		}
	}
	return names, nil
}
