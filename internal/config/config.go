// Package config loads and saves Spellbook configuration for the two scopes
// it manages: the user's home directory and a single project.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"

	"github.com/BurntSushi/toml"
)

// ProjectFile is the name of the configuration file in a project root.
const ProjectFile = "spellbook.toml"

// LockFile is the name of the file that pins each registry to a commit.
const LockFile = "spellbook.lock"

// Scope says which set of directories a sync writes to.
type Scope string

const (
	ScopeHome    Scope = "home"
	ScopeProject Scope = "project"
)

// Location describes where one scope keeps its files.
type Location struct {
	Scope Scope
	// Root is the directory target paths are resolved against: the user's
	// home directory, or the project root.
	Root       string
	ConfigPath string
	StatePath  string
	LockPath   string
}

// Target holds the per-agent settings.
type Target struct {
	Enabled bool `toml:"enabled"`
	// SkillsPath overrides the default skills directory. "~" expands to the
	// home directory; a relative path is resolved against Location.Root.
	SkillsPath string `toml:"skills_path,omitempty"`
}

// Registry is one subscribed git repository.
type Registry struct {
	Name string `toml:"name"`
	URL  string `toml:"url"`
	// Ref is a branch, tag or commit. Empty means the remote's default branch.
	Ref string `toml:"ref,omitempty"`
	// Path limits the registry to a subdirectory of the repository.
	Path string `toml:"path,omitempty"`
	// Priority decides name collisions: the higher number wins.
	Priority int `toml:"priority,omitzero"`
	// Groups selects groups from the registry's manifest. Empty means the
	// registry's default groups, or everything when it defines none.
	Groups []string `toml:"groups,omitempty"`
	// Skills limits the registry to the named skills. Empty means all.
	Skills []string `toml:"skills,omitempty"`
	// Only limits the registry to some kinds of resources. Empty means all.
	Only []string `toml:"only,omitempty"`
	// Prefix puts the registry name in front of installed names:
	// PrefixAll always, PrefixCollisions only when the plain name is taken.
	Prefix string `toml:"prefix,omitempty"`
}

// Kinds of resources a registry can provide, as used in Registry.Only.
const (
	KindSkills       = "skills"
	KindInstructions = "instructions"
)

// Kinds lists the valid values of Registry.Only.
var Kinds = []string{KindSkills, KindInstructions}

// Wants reports whether the subscriber wants resources of the given kind.
func (r Registry) Wants(kind string) bool {
	return len(r.Only) == 0 || slices.Contains(r.Only, kind)
}

// Values of Registry.Prefix.
const (
	PrefixNone       = ""
	PrefixAll        = "all"
	PrefixCollisions = "collisions"
)

// Config is the content of a config.toml or spellbook.toml file.
type Config struct {
	Targets    map[string]Target `toml:"targets,omitempty"`
	Registries []Registry        `toml:"registry,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidName reports whether s is usable as a registry or skill name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// HomeLocation returns the location of the user-level configuration.
// SPELLBOOK_CONFIG_DIR overrides the directory; otherwise XDG_CONFIG_HOME is
// honoured and ~/.config is the fallback on every platform.
func HomeLocation() (Location, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Location{}, fmt.Errorf("find home directory: %w", err)
	}
	dir := os.Getenv("SPELLBOOK_CONFIG_DIR")
	if dir == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		dir = filepath.Join(base, "spellbook")
	}
	return Location{
		Scope:      ScopeHome,
		Root:       home,
		ConfigPath: filepath.Join(dir, "config.toml"),
		StatePath:  filepath.Join(dir, "state.json"),
		LockPath:   filepath.Join(dir, LockFile),
	}, nil
}

// ProjectLocationAt returns the project location rooted at dir, whether or
// not a config file exists there yet.
func ProjectLocationAt(dir string) (Location, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Location{}, err
	}
	return Location{
		Scope:      ScopeProject,
		Root:       abs,
		ConfigPath: filepath.Join(abs, ProjectFile),
		StatePath:  filepath.Join(abs, ".spellbook", "state.json"),
		LockPath:   filepath.Join(abs, LockFile),
	}, nil
}

// FindProject walks up from start until it finds a spellbook.toml.
func FindProject(start string) (Location, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Location{}, err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ProjectFile)); err == nil {
			return ProjectLocationAt(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Location{}, fmt.Errorf("no %s found in %s or any parent directory; run 'spellbook init --project'", ProjectFile, start)
		}
		dir = parent
	}
}

// Exists reports whether the config file of loc is present.
func Exists(loc Location) bool {
	_, err := os.Stat(loc.ConfigPath)
	return err == nil
}

// Load reads and validates the config file of loc.
func Load(loc Location) (*Config, error) {
	data, err := os.ReadFile(loc.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		hint := "spellbook init"
		if loc.Scope == ScopeProject {
			hint = "spellbook init --project"
		}
		return nil, fmt.Errorf("%s does not exist; run '%s' first", loc.ConfigPath, hint)
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", loc.ConfigPath, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("%s: unknown key %q", loc.ConfigPath, undecoded[0].String())
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", loc.ConfigPath, err)
	}
	return &cfg, nil
}

// Save writes cfg to the config file of loc, creating directories as needed.
func Save(loc Location, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(loc.ConfigPath), 0o755); err != nil {
		return err
	}
	tmp := loc.ConfigPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, loc.ConfigPath)
}

// Validate checks names and required fields.
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for _, r := range c.Registries {
		if !ValidName(r.Name) {
			return fmt.Errorf("invalid registry name %q: use lowercase letters, digits, '.', '_' and '-'", r.Name)
		}
		if seen[r.Name] {
			return fmt.Errorf("registry %q is defined twice", r.Name)
		}
		seen[r.Name] = true
		if r.URL == "" {
			return fmt.Errorf("registry %q has no url", r.Name)
		}
		for _, k := range r.Only {
			if !slices.Contains(Kinds, k) {
				return fmt.Errorf("registry %q: only must name kinds from %v, not %q", r.Name, Kinds, k)
			}
		}
		switch r.Prefix {
		case PrefixNone, PrefixAll, PrefixCollisions:
		default:
			return fmt.Errorf("registry %q: prefix must be %q or %q, not %q", r.Name, PrefixAll, PrefixCollisions, r.Prefix)
		}
		if r.Path != "" && !filepath.IsLocal(filepath.FromSlash(r.Path)) {
			return fmt.Errorf("registry %q: path %q must stay inside the repository", r.Name, r.Path)
		}
	}
	return nil
}

// Registry returns the registry with the given name.
func (c *Config) Registry(name string) (Registry, bool) {
	for _, r := range c.Registries {
		if r.Name == name {
			return r, true
		}
	}
	return Registry{}, false
}

// RemoveRegistry deletes the named registry and reports whether it existed.
func (c *Config) RemoveRegistry(name string) bool {
	for i, r := range c.Registries {
		if r.Name == name {
			c.Registries = append(c.Registries[:i], c.Registries[i+1:]...)
			return true
		}
	}
	return false
}

// EnabledTargets returns the names of the enabled targets in a stable order.
func (c *Config) EnabledTargets() []string {
	var names []string
	for name, t := range c.Targets {
		if t.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
