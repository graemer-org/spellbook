// Package cli defines the spellbook command line.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/graemer-org/spellbook/internal/autosync"
	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/gitx"
	"github.com/graemer-org/spellbook/internal/lock"
	"github.com/graemer-org/spellbook/internal/registry"
	"github.com/graemer-org/spellbook/internal/state"
	"github.com/graemer-org/spellbook/internal/syncer"
	"github.com/graemer-org/spellbook/internal/target"
)

// Version is set at build time with -ldflags.
var Version = "dev"

// errSyncFailed makes the process exit non-zero after the report was printed.
var errSyncFailed = errors.New("sync finished with errors")

// Execute runs the command line and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	root := newRoot()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		if !errors.Is(err, errSyncFailed) {
			fmt.Fprintln(stderr, "error:", err)
		}
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	var project bool
	root := &cobra.Command{
		Use:           "spellbook",
		Short:         "Share skills and other AI tooling from git registries with Claude Code and GitHub Copilot",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&project, "project", "p", false, "use the project's spellbook.toml instead of the home configuration")

	locate := func() (config.Location, error) {
		if project {
			wd, err := os.Getwd()
			if err != nil {
				return config.Location{}, err
			}
			return config.FindProject(wd)
		}
		return config.HomeLocation()
	}

	root.AddCommand(newInit(&project), newRegistry(locate), newGroup(locate), newSync(locate), newDiff(locate), newList(locate), newAutosync(locate), newDoctor(locate))
	return root
}

func newInit(project *bool) *cobra.Command {
	var targets []string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the configuration file",
		Long: "Create the home configuration, or with --project a spellbook.toml in the\n" +
			"current directory. Both targets are enabled unless --target is given.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var loc config.Location
			var err error
			if *project {
				wd, werr := os.Getwd()
				if werr != nil {
					return werr
				}
				loc, err = config.ProjectLocationAt(wd)
			} else {
				loc, err = config.HomeLocation()
			}
			if err != nil {
				return err
			}
			if config.Exists(loc) {
				return fmt.Errorf("%s already exists", loc.ConfigPath)
			}
			if len(targets) == 0 {
				targets = target.Names()
			}
			cfg := &config.Config{Targets: map[string]config.Target{}}
			for _, name := range target.Names() {
				cfg.Targets[name] = config.Target{}
			}
			for _, name := range targets {
				if !target.Known(name) {
					return fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(target.Names(), ", "))
				}
				cfg.Targets[name] = config.Target{Enabled: true}
			}
			if err := config.Save(loc, cfg); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created %s\n", loc.ConfigPath)
			for _, name := range cfg.EnabledTargets() {
				dir, err := target.SkillsDir(name, cfg.Targets[name], loc)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "  %-8s skills -> %s\n", name, dir)
			}
			fmt.Fprintln(out, "Next: spellbook registry add <name> <git-url>")
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&targets, "target", nil, "target to enable (claude, copilot); repeatable")
	return cmd
}

func newRegistry(locate func() (config.Location, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage subscribed registries",
	}

	var reg config.Registry
	var noSync bool
	add := &cobra.Command{
		Use:   "add <name> <git-url>",
		Short: "Subscribe to a git repository and sync it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := locate()
			if err != nil {
				return err
			}
			cfg, err := config.Load(loc)
			if err != nil {
				return err
			}
			reg.Name, reg.URL = args[0], args[1]
			if _, exists := cfg.Registry(reg.Name); exists {
				return fmt.Errorf("registry %q already exists", reg.Name)
			}
			cfg.Registries = append(cfg.Registries, reg)
			if err := cfg.Validate(); err != nil {
				return err
			}
			// Fetch before saving, so a wrong URL never ends up in the config.
			snap, err := registry.Fetch(cmd.Context(), reg)
			if err != nil {
				removeCheckout(reg)
				return fmt.Errorf("fetch %s: %w", reg.URL, err)
			}
			if err := config.Save(loc, cfg); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Added registry %s (%d skills, %d instructions, %d agents, %d prompts at %s)\n", reg.Name, len(snap.Skills), len(snap.Instructions), len(snap.Agents), len(snap.Prompts), short(snap.Commit))
			if snap.MCPBlocked > 0 {
				fmt.Fprintf(out, "It also offers %d MCP servers. They start programs or connect to services, so they\nare only installed after: spellbook registry allow %s mcp\n", snap.MCPBlocked, reg.Name)
			}
			if snap.HooksBlocked > 0 {
				fmt.Fprintf(out, "It also offers %d hooks. They run commands on your machine during a session, so\nthey are only installed after: spellbook registry allow %s hooks\n", snap.HooksBlocked, reg.Name)
			}
			if noSync {
				return nil
			}
			return runSync(cmd, loc, cfg, syncer.Options{})
		},
	}
	registryFlags(add, &reg)
	add.Flags().BoolVar(&noSync, "no-sync", false, "add the registry without syncing")

	list := &cobra.Command{
		Use:   "list",
		Short: "List subscribed registries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			loc, err := locate()
			if err != nil {
				return err
			}
			cfg, err := config.Load(loc)
			if err != nil {
				return err
			}
			if len(cfg.Registries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No registries. Add one with: spellbook registry add <name> <git-url>")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tURL\tREF\tPATH\tPRIORITY\tGROUPS\tLIMITED TO\tPREFIX")
			for _, r := range cfg.Registries {
				groups := "default"
				if len(r.Groups) > 0 {
					groups = strings.Join(r.Groups, ",")
				}
				var limits []string
				for _, l := range []struct {
					kind  string
					names []string
				}{{"skills", r.Skills}, {"instructions", r.Instructions}, {"agents", r.Agents}, {"prompts", r.Prompts}, {"mcp", r.MCP}, {"hooks", r.Hooks}} {
					if len(l.names) > 0 {
						limits = append(limits, l.kind+": "+strings.Join(l.names, ","))
					}
				}
				if len(r.Only) > 0 {
					limits = append(limits, "only "+strings.Join(r.Only, ","))
				}
				skills := "-"
				if len(limits) > 0 {
					skills = strings.Join(limits, "; ")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n", r.Name, r.URL, dash(r.Ref), dash(r.Path), r.Priority, groups, skills, dash(r.Prefix))
			}
			return w.Flush()
		},
	}

	var force bool
	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Unsubscribe from a registry and remove what it installed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := locate()
			if err != nil {
				return err
			}
			cfg, err := config.Load(loc)
			if err != nil {
				return err
			}
			reg, ok := cfg.Registry(args[0])
			if !ok {
				return fmt.Errorf("no registry named %q", args[0])
			}
			cfg.RemoveRegistry(args[0])
			if err := config.Save(loc, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed registry %s\n", reg.Name)
			removeCheckout(reg)
			return runSync(cmd, loc, cfg, syncer.Options{Force: force})
		},
	}
	remove.Flags().BoolVar(&force, "force", false, "also remove skills that were edited locally")

	setup := &cobra.Command{
		Use:   "setup <directory>",
		Short: "Create the skeleton of a new registry",
		Long: "Create a directory with a registry.toml and an example skill. Turn it into a\n" +
			"git repository and push it, then others can subscribe with 'registry add'.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			manifest := filepath.Join(dir, registry.ManifestFile)
			if _, err := os.Stat(manifest); err == nil {
				return fmt.Errorf("%s already exists", manifest)
			}
			files := map[string]string{
				manifest: setupManifest,
				filepath.Join(dir, "skills", "example", registry.SkillFile):    setupSkill,
				filepath.Join(dir, registry.InstructionsDir, "conventions.md"): setupInstruction,
			}
			for path, content := range files {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created a registry in %s\n", dir)
			fmt.Fprintln(out, "Next: git init, commit and push it, then: spellbook registry add <name> <git-url>")
			return nil
		},
	}

	// permit adds or removes guarded kinds of a registry and syncs.
	permit := func(cmd *cobra.Command, args []string, allow bool) error {
		loc, err := locate()
		if err != nil {
			return err
		}
		cfg, err := config.Load(loc)
		if err != nil {
			return err
		}
		for i := range cfg.Registries {
			r := &cfg.Registries[i]
			if r.Name != args[0] {
				continue
			}
			for _, kind := range args[1:] {
				if !slices.Contains(config.GuardedKinds, kind) {
					return fmt.Errorf("%q is not a kind that needs to be allowed (those are: %s)", kind, strings.Join(config.GuardedKinds, ", "))
				}
				r.Allow = slices.DeleteFunc(r.Allow, func(k string) bool { return k == kind })
				if allow {
					r.Allow = append(r.Allow, kind)
				}
			}
			if err := config.Save(loc, cfg); err != nil {
				return err
			}
			return runSync(cmd, loc, cfg, syncer.Options{})
		}
		return fmt.Errorf("no registry named %q", args[0])
	}
	allow := &cobra.Command{
		Use:   "allow <registry> <kind>...",
		Short: "Let a registry install a guarded kind of resource (mcp, hooks) and sync",
		Long: "MCP servers and hooks run programs on your machine, so a registry only\n" +
			"installs them after you allowed it to.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return permit(cmd, args, true) },
	}
	disallow := &cobra.Command{
		Use:   "disallow <registry> <kind>...",
		Short: "Take the permission back and remove what was installed",
		Args:  cobra.MinimumNArgs(2),
		RunE:  func(cmd *cobra.Command, args []string) error { return permit(cmd, args, false) },
	}

	var edit config.Registry
	var newURL string
	set := &cobra.Command{
		Use:   "set <name>",
		Short: "Change the settings of a registry and sync",
		Long: "Change how a registry is used. Only the flags you pass are changed. Pass an\n" +
			"empty value to clear a setting, for example --ref= or --skill=.",
		Example: "  spellbook registry set acme --ref stable --priority 10\n" +
			"  spellbook registry set acme --group platform --group frontend\n" +
			"  spellbook registry set acme --skill=            # all skills again",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := locate()
			if err != nil {
				return err
			}
			cfg, err := config.Load(loc)
			if err != nil {
				return err
			}
			for i := range cfg.Registries {
				r := &cfg.Registries[i]
				if r.Name != args[0] {
					continue
				}
				changed := 0
				str := func(flag string, dst *string, src string) {
					if cmd.Flags().Changed(flag) {
						*dst = src
						changed++
					}
				}
				list := func(flag string, dst *[]string, src []string) {
					if cmd.Flags().Changed(flag) {
						// An empty value clears the list.
						*dst = slices.DeleteFunc(slices.Clone(src), func(v string) bool { return v == "" })
						changed++
					}
				}
				str("url", &r.URL, newURL)
				str("ref", &r.Ref, edit.Ref)
				str("path", &r.Path, edit.Path)
				str("prefix", &r.Prefix, edit.Prefix)
				if cmd.Flags().Changed("priority") {
					r.Priority = edit.Priority
					changed++
				}
				list("group", &r.Groups, edit.Groups)
				list("only", &r.Only, edit.Only)
				list("allow", &r.Allow, edit.Allow)
				list("skill", &r.Skills, edit.Skills)
				list("instruction", &r.Instructions, edit.Instructions)
				list("agent", &r.Agents, edit.Agents)
				list("prompt", &r.Prompts, edit.Prompts)
				list("mcp-server", &r.MCP, edit.MCP)
				list("hook", &r.Hooks, edit.Hooks)
				if changed == 0 {
					return fmt.Errorf("nothing to change: pass at least one flag, see 'spellbook registry set --help'")
				}
				if err := cfg.Validate(); err != nil {
					return err
				}
				// Fetch before saving, so a setting the registry cannot
				// satisfy never ends up in the config.
				if _, err := registry.Fetch(cmd.Context(), *r); err != nil {
					return fmt.Errorf("registry %s: %w", r.Name, err)
				}
				if err := config.Save(loc, cfg); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Updated registry %s\n", r.Name)
				return runSync(cmd, loc, cfg, syncer.Options{})
			}
			return fmt.Errorf("no registry named %q", args[0])
		},
	}
	set.Flags().StringVar(&newURL, "url", "", "the git URL of the registry")
	registryFlags(set, &edit)

	cmd.AddCommand(add, list, remove, set, setup, allow, disallow)
	return cmd
}

// registryFlags defines the flags that describe how a registry is used.
// 'registry add' and 'registry set' share them.
func registryFlags(cmd *cobra.Command, reg *config.Registry) {
	f := cmd.Flags()
	f.StringVar(&reg.Ref, "ref", "", "branch, tag or commit to follow (default: the remote's default branch)")
	f.StringVar(&reg.Path, "path", "", "only use this subdirectory of the repository")
	f.IntVar(&reg.Priority, "priority", 0, "the registry with the higher number wins a name collision")
	f.StringSliceVar(&reg.Groups, "group", nil, "group of the registry to join; repeatable (default: the registry's default groups)")
	f.StringSliceVar(&reg.Only, "only", nil, "only install this kind of resource: "+strings.Join(config.Kinds, ", ")+"; repeatable")
	f.StringSliceVar(&reg.Allow, "allow", nil, "let the registry install this guarded kind: "+strings.Join(config.GuardedKinds, ", ")+"; repeatable")
	f.StringVar(&reg.Prefix, "prefix", "", "put the registry name in front of installed names: 'all', or 'collisions' for taken names only")
	f.StringSliceVar(&reg.Skills, "skill", nil, "only install this skill; repeatable")
	f.StringSliceVar(&reg.Instructions, "instruction", nil, "only install this instruction; repeatable")
	f.StringSliceVar(&reg.Agents, "agent", nil, "only install this agent; repeatable")
	f.StringSliceVar(&reg.Prompts, "prompt", nil, "only install this prompt; repeatable")
	f.StringSliceVar(&reg.MCP, "mcp-server", nil, "only install this MCP server; repeatable")
	f.StringSliceVar(&reg.Hooks, "hook", nil, "only install this hook; repeatable")
}

const setupManifest = `# Describes this registry for spellbook.
version = 1

# What a subscriber gets who did not choose a group.
default_groups = ["base"]

[groups.base]
description = "For everyone"
skills = ["example"]
instructions = ["conventions"]

# A team group that also contains everything from base:
#
# [groups.platform]
# description = "Platform team"
# include = ["base"]
# skills = ["terraform-review"]
# instructions = ["terraform-style"]
`

const setupInstruction = `---
description: Replace this with what the instruction is about.
# Add globs to apply it only to matching files:
# paths:
#   - "src/**/*.ts"
---

# Conventions

Write the standing guidance for the agent here.
`

const setupSkill = `---
name: example
description: Replace this with what the skill does and when to use it.
---

# Example

Write the instructions for the agent here.
`

func newGroup(locate func() (config.Location, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Join and leave the groups a registry defines",
	}

	list := &cobra.Command{
		Use:   "list <registry>",
		Short: "List the groups of a registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := locate()
			if err != nil {
				return err
			}
			cfg, err := config.Load(loc)
			if err != nil {
				return err
			}
			reg, ok := cfg.Registry(args[0])
			if !ok {
				return fmt.Errorf("no registry named %q", args[0])
			}
			snap, err := registry.Fetch(cmd.Context(), reg)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if snap.Manifest == nil || len(snap.Manifest.Groups) == 0 {
				fmt.Fprintf(out, "Registry %s defines no groups.\n", reg.Name)
				return nil
			}
			joined := map[string]bool{}
			for _, g := range snap.Groups {
				joined[g] = true
			}
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "GROUP\tJOINED\tINCLUDES\tSKILLS\tINSTRUCTIONS\tAGENTS\tPROMPTS\tDESCRIPTION")
			for _, name := range snap.Manifest.GroupNames() {
				g := snap.Manifest.Groups[name]
				state := "-"
				if joined[name] {
					state = "yes"
					if len(reg.Groups) == 0 {
						state = "default"
					}
				}
				skills, err := snap.Manifest.Skills([]string{name})
				if err != nil {
					return err
				}
				instructions, err := snap.Manifest.Instructions([]string{name})
				if err != nil {
					return err
				}
				agents, err := snap.Manifest.Agents([]string{name})
				if err != nil {
					return err
				}
				prompts, err := snap.Manifest.Prompts([]string{name})
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n", name, state, dash(strings.Join(g.Include, ",")), len(skills), len(instructions), len(agents), len(prompts), dash(g.Description))
			}
			return w.Flush()
		},
	}

	// change loads the registry, applies edit to its groups, checks the
	// result against the registry and only then saves and syncs.
	// edit also receives the groups currently in effect, which are the
	// registry's defaults when none are configured.
	change := func(cmd *cobra.Command, name string, edit func(groups, effective []string) []string) error {
		loc, err := locate()
		if err != nil {
			return err
		}
		cfg, err := config.Load(loc)
		if err != nil {
			return err
		}
		for i := range cfg.Registries {
			if cfg.Registries[i].Name != name {
				continue
			}
			current, err := registry.Fetch(cmd.Context(), cfg.Registries[i])
			if err != nil {
				return err
			}
			cfg.Registries[i].Groups = edit(cfg.Registries[i].Groups, current.Groups)
			if _, err := registry.Fetch(cmd.Context(), cfg.Registries[i]); err != nil {
				return err
			}
			if err := config.Save(loc, cfg); err != nil {
				return err
			}
			groups := "the registry's default"
			if g := cfg.Registries[i].Groups; len(g) > 0 {
				groups = strings.Join(g, ", ")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Groups of %s: %s\n", name, groups)
			return runSync(cmd, loc, cfg, syncer.Options{})
		}
		return fmt.Errorf("no registry named %q", name)
	}

	join := &cobra.Command{
		Use:   "join <registry> <group>...",
		Short: "Join groups of a registry and sync",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return change(cmd, args[0], func(groups, effective []string) []string {
				if len(groups) == 0 {
					// Joining a group adds to the defaults instead of replacing them.
					groups = slices.Clone(effective)
				}
				for _, g := range args[1:] {
					if !slices.Contains(groups, g) {
						groups = append(groups, g)
					}
				}
				return groups
			})
		},
	}

	leave := &cobra.Command{
		Use:   "leave <registry> <group>...",
		Short: "Leave groups of a registry and sync",
		Long: "Leave groups of a registry and sync. Leaving the last group falls back to the\n" +
			"registry's default groups.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := locate()
			if err != nil {
				return err
			}
			cfg, err := config.Load(loc)
			if err != nil {
				return err
			}
			if reg, ok := cfg.Registry(args[0]); ok {
				for _, g := range args[1:] {
					if !slices.Contains(reg.Groups, g) {
						return fmt.Errorf("registry %q is not in group %q", args[0], g)
					}
				}
			}
			return change(cmd, args[0], func(groups, _ []string) []string {
				return slices.DeleteFunc(groups, func(g string) bool { return slices.Contains(args[1:], g) })
			})
		},
	}

	cmd.AddCommand(list, join, leave)
	return cmd
}

func newDiff(locate func() (config.Location, error)) *cobra.Command {
	var patch bool
	cmd := &cobra.Command{
		Use:   "diff [registry]",
		Short: "Show what changed upstream since the last sync",
		Long: "Compare each registry at the commit of the last sync with its newest commit.\n" +
			"Nothing is installed and the lock file stays as it is.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := locate()
			if err != nil {
				return err
			}
			cfg, err := config.Load(loc)
			if err != nil {
				return err
			}
			regs := cfg.Registries
			if len(args) == 1 {
				reg, ok := cfg.Registry(args[0])
				if !ok {
					return fmt.Errorf("no registry named %q", args[0])
				}
				regs = []config.Registry{reg}
			}
			out := cmd.OutOrStdout()
			failed := false
			for _, r := range regs {
				d, err := syncer.Compare(cmd.Context(), loc, r, patch)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "error: registry %s: %v\n", r.Name, err)
					failed = true
					continue
				}
				switch {
				case d.From == "":
					fmt.Fprintf(out, "%s: not synced yet, newest is %s\n", d.Name, short(d.To))
				case d.From == d.To:
					fmt.Fprintf(out, "%s: up to date at %s\n", d.Name, short(d.To))
					continue
				case !d.HasChanges():
					fmt.Fprintf(out, "%s: %s -> %s, no change to your skills\n", d.Name, short(d.From), short(d.To))
				default:
					fmt.Fprintf(out, "%s: %s -> %s\n", d.Name, short(d.From), short(d.To))
				}
				for _, n := range d.Added {
					fmt.Fprintf(out, "  + %s\n", n)
				}
				for _, n := range d.Changed {
					fmt.Fprintf(out, "  ~ %s\n", n)
				}
				for _, n := range d.Removed {
					fmt.Fprintf(out, "  - %s\n", n)
				}
				if d.Patch != "" {
					fmt.Fprintln(out, d.Patch)
				}
			}
			if failed {
				return errSyncFailed
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&patch, "patch", false, "also print the textual difference")
	return cmd
}

func newSync(locate func() (config.Location, error)) *cobra.Command {
	var opts syncer.Options
	var auto, check bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch all registries and update the targets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if auto {
				runAuto(cmd, locate)
				return nil
			}
			loc, err := locate()
			if err != nil {
				return err
			}
			cfg, err := config.Load(loc)
			if err != nil {
				return err
			}
			if check {
				return runCheck(cmd, loc, cfg)
			}
			return runSync(cmd, loc, cfg, opts)
		},
	}
	cmd.Flags().BoolVar(&auto, "auto", false, "for session hooks: sync at most once per interval, print nothing and never fail")
	cmd.Flags().BoolVar(&check, "check", false, "for CI: change nothing and fail if the installed files differ from spellbook.lock")
	cmd.Flags().BoolVarP(&opts.DryRun, "dry-run", "n", false, "show what would change without writing anything")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "overwrite and remove resources that were edited locally")
	cmd.Flags().BoolVar(&opts.Locked, "locked", false, "install the commits recorded in spellbook.lock instead of the newest ones")
	cmd.MarkFlagsMutuallyExclusive("auto", "check", "dry-run")
	cmd.MarkFlagsMutuallyExclusive("auto", "check", "locked")
	cmd.MarkFlagsMutuallyExclusive("auto", "check", "force")
	return cmd
}

func runSync(cmd *cobra.Command, loc config.Location, cfg *config.Config, opts syncer.Options) error {
	out := cmd.OutOrStdout()
	if len(cfg.EnabledTargets()) == 0 {
		return fmt.Errorf("no target is enabled in %s", loc.ConfigPath)
	}
	rep, err := syncer.Run(cmd.Context(), loc, cfg, opts)
	if err != nil {
		return err
	}
	if opts.DryRun {
		fmt.Fprintln(out, "Dry run: nothing was written.")
	}
	printReport(cmd, rep)
	if rep.Failed() {
		return errSyncFailed
	}
	return nil
}

// runAuto is the sync a session hook starts. It stays silent, because a
// tool may feed a hook's output to the model, and it never fails, because
// a broken sync must not get in the way of a session. The outcome is kept
// for 'spellbook autosync status'.
func runAuto(cmd *cobra.Command, locate func() (config.Location, error)) {
	loc, err := locate()
	if err != nil {
		return
	}
	cfg, err := config.Load(loc)
	if err != nil {
		return
	}
	interval, err := cfg.AutoSync.ParseInterval(autosync.DefaultInterval)
	if err != nil {
		return
	}
	status, err := autosync.Load(loc)
	now := time.Now()
	if err != nil || !status.Due(now, interval) {
		return
	}
	rep, err := syncer.Run(cmd.Context(), loc, cfg, syncer.Options{})
	if err == nil {
		for _, r := range rep.Registries {
			if r.Err != nil {
				err = fmt.Errorf("registry %s: %w", r.Name, r.Err)
				break
			}
		}
	}
	_ = autosync.Record(loc, now, err)
}

// runCheck reports whether a sync of the locked commits would change
// anything, without changing it.
func runCheck(cmd *cobra.Command, loc config.Location, cfg *config.Config) error {
	rep, err := syncer.Run(cmd.Context(), loc, cfg, syncer.Options{DryRun: true, Locked: true})
	if err != nil {
		return err
	}
	drift := 0
	for _, c := range rep.Targets {
		drift += c.Installed + c.Updated + c.Removed + c.Skipped
	}
	if !rep.Failed() && drift == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Up to date with %s.\n", config.LockFile)
		return nil
	}
	printReport(cmd, rep)
	fmt.Fprintf(cmd.ErrOrStderr(), "error: the installed files differ from %s; run 'spellbook sync --locked' and commit the result\n", config.LockFile)
	return errSyncFailed
}

func printReport(cmd *cobra.Command, rep *syncer.Report) {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	for _, r := range rep.Registries {
		if r.Err != nil {
			fmt.Fprintf(errOut, "error: registry %s: %v\n", r.Name, r.Err)
			continue
		}
		was := ""
		if r.Previous != "" && r.Previous != r.Commit {
			was = fmt.Sprintf(" (was %s)", short(r.Previous))
		}
		mcp := fmt.Sprintf("%d MCP servers", r.MCP)
		if r.MCPBlocked > 0 {
			mcp = fmt.Sprintf("%d MCP servers not allowed", r.MCPBlocked)
		}
		hooks := fmt.Sprintf("%d hooks", r.Hooks)
		if r.HooksBlocked > 0 {
			hooks = fmt.Sprintf("%d hooks not allowed", r.HooksBlocked)
		}
		fmt.Fprintf(out, "%-12s %d skills, %d instructions, %d agents, %d prompts, %s, %s at %s%s\n", r.Name, r.Skills, r.Instructions, r.Agents, r.Prompts, mcp, hooks, short(r.Commit), was)
	}
	names := make([]string, 0, len(rep.Targets))
	for n := range rep.Targets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := rep.Targets[n]
		fmt.Fprintf(out, "%-12s %d installed, %d updated, %d unchanged, %d removed, %d skipped\n",
			n, c.Installed, c.Updated, c.Unchanged, c.Removed, c.Skipped)
	}
	for _, w := range rep.Warnings {
		fmt.Fprintln(errOut, "warning:", w)
	}
}

func newAutosync(locate func() (config.Location, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autosync",
		Short: "Sync automatically when a session starts",
		Long: "Install a session hook in Claude Code and GitHub Copilot that runs\n" +
			"'spellbook sync --auto'. The sync runs at most once per interval, which is\n" +
			"24h unless autosync.interval in the configuration says otherwise.",
	}

	// each runs fn for every enabled target.
	each := func(cmd *cobra.Command, fn func(loc config.Location, cfg *config.Config, target string) error) error {
		loc, err := locate()
		if err != nil {
			return err
		}
		cfg, err := config.Load(loc)
		if err != nil {
			return err
		}
		if len(cfg.EnabledTargets()) == 0 {
			return fmt.Errorf("no target is enabled in %s", loc.ConfigPath)
		}
		for _, t := range cfg.EnabledTargets() {
			err := fn(loc, cfg, t)
			if errors.Is(err, autosync.ErrUnsupported) {
				fmt.Fprintf(cmd.OutOrStdout(), "%-8s %v\n", t, err)
				continue
			}
			if err != nil {
				return err
			}
		}
		return nil
	}

	enable := &cobra.Command{
		Use:   "enable",
		Short: "Install the session hooks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return each(cmd, func(loc config.Location, _ *config.Config, t string) error {
				if err := autosync.Enable(t, loc); err != nil {
					return err
				}
				file, _ := autosync.HookFile(t, loc)
				fmt.Fprintf(cmd.OutOrStdout(), "%-8s hook installed in %s\n", t, file)
				return nil
			})
		},
	}

	disable := &cobra.Command{
		Use:   "disable",
		Short: "Remove the session hooks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return each(cmd, func(loc config.Location, _ *config.Config, t string) error {
				if err := autosync.Disable(t, loc); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-8s hook removed\n", t)
				return nil
			})
		},
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show the hooks and the last automatic sync",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			var loc config.Location
			var cfg *config.Config
			err := each(cmd, func(l config.Location, c *config.Config, t string) error {
				loc, cfg = l, c
				on, err := autosync.Installed(t, l)
				if err != nil {
					return err
				}
				state := "no hook"
				if on {
					state = "hook installed"
				}
				fmt.Fprintf(out, "%-8s %s\n", t, state)
				return nil
			})
			if err != nil {
				return err
			}
			interval, err := cfg.AutoSync.ParseInterval(autosync.DefaultInterval)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "interval %s\n", interval)
			st, err := autosync.Load(loc)
			if err != nil {
				return err
			}
			switch {
			case st.LastAttempt.IsZero():
				fmt.Fprintln(out, "last run never")
			case st.LastError != "":
				fmt.Fprintf(out, "last run %s, failed: %s\n", st.LastAttempt.Format(time.RFC3339), st.LastError)
			default:
				fmt.Fprintf(out, "last run %s, ok\n", st.LastSuccess.Format(time.RFC3339))
			}
			return nil
		},
	}

	cmd.AddCommand(enable, disable, status)
	return cmd
}

func newDoctor(locate func() (config.Location, error)) *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the setup and explain what is wrong",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			failed := false
			ok := func(format string, args ...any) { fmt.Fprintf(out, "ok    "+format+"\n", args...) }
			warn := func(format string, args ...any) { fmt.Fprintf(out, "warn  "+format+"\n", args...) }
			fail := func(format string, args ...any) {
				failed = true
				fmt.Fprintf(out, "FAIL  "+format+"\n", args...)
			}
			finish := func() error {
				if failed {
					return errSyncFailed
				}
				return nil
			}

			if v, err := gitx.Version(cmd.Context()); err != nil {
				fail("git is not installed or not on PATH")
			} else {
				ok("git %s", v)
			}

			loc, err := locate()
			if err != nil {
				fail("%v", err)
				return finish()
			}
			cfg, err := config.Load(loc)
			if err != nil {
				fail("%v", err)
				return finish()
			}
			ok("configuration %s", loc.ConfigPath)

			targets := cfg.EnabledTargets()
			if len(targets) == 0 {
				fail("no target is enabled")
			}
			for _, t := range targets {
				dir, err := target.SkillsDir(t, cfg.Targets[t], loc)
				if err != nil {
					fail("%s: %v", t, err)
					continue
				}
				ok("target %s, skills in %s", t, dir)
				if on, err := autosync.Installed(t, loc); err == nil && !on {
					warn("target %s has no automatic sync; run 'spellbook autosync enable'", t)
				}
			}

			if len(cfg.Registries) == 0 {
				warn("no registries; add one with 'spellbook registry add <name> <git-url>'")
			}
			lk, err := lock.Load(loc.LockPath)
			if err != nil {
				fail("%v", err)
			}
			for _, r := range cfg.Registries {
				if lk != nil {
					if _, synced := lk.Get(r.Name); !synced {
						warn("registry %s was never synced; run 'spellbook sync'", r.Name)
					}
				}
				if offline {
					continue
				}
				if err := gitx.Reachable(cmd.Context(), r.URL); err != nil {
					fail("registry %s cannot be reached: %v", r.Name, err)
				} else {
					ok("registry %s is reachable", r.Name)
				}
			}

			total, problems, err := syncer.Verify(loc)
			if err != nil {
				fail("%v", err)
				return finish()
			}
			if len(problems) == 0 {
				ok("%d installed resources match what was synced", total)
			}
			for _, p := range problems {
				what := "was edited locally; 'spellbook sync --force' restores it"
				if p.Missing {
					what = "is missing; 'spellbook sync' restores it"
				}
				warn("%s %q for %s (%s) %s", p.Entry.Kind, p.Entry.Name, p.Entry.Target, p.Entry.Path, what)
			}

			if st, err := autosync.Load(loc); err == nil && st.LastError != "" {
				warn("the last automatic sync failed: %s", st.LastError)
			}
			return finish()
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "do not contact the registries")
	return cmd
}

func newList(locate func() (config.Location, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List what spellbook installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			loc, err := locate()
			if err != nil {
				return err
			}
			st, err := state.Load(loc.StatePath)
			if err != nil {
				return err
			}
			if len(st.Entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing installed. Run: spellbook sync")
				return nil
			}
			entries := st.Entries
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].Name != entries[j].Name {
					return entries[i].Name < entries[j].Name
				}
				return entries[i].Target < entries[j].Target
			})
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tKIND\tTARGET\tREGISTRY\tCOMMIT\tPATH")
			for _, e := range entries {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Name, e.Kind, e.Target, e.Registry, short(e.Commit), e.Path)
			}
			return w.Flush()
		},
	}
}

// removeCheckout deletes the cached clone of a registry. The cache is only
// an optimisation, so a failure here is not reported.
func removeCheckout(r config.Registry) {
	if dir, err := registry.CheckoutDir(r); err == nil {
		os.RemoveAll(dir)
	}
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
