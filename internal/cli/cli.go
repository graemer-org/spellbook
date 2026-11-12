// Package cli defines the spellbook command line.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/graemer-org/spellbook/internal/config"
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

	root.AddCommand(newInit(&project), newRegistry(locate), newSync(locate), newList(locate))
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
			fmt.Fprintf(out, "Added registry %s (%d skills at %s)\n", reg.Name, len(snap.Skills), short(snap.Commit))
			if noSync {
				return nil
			}
			return runSync(cmd, loc, cfg, syncer.Options{})
		},
	}
	add.Flags().StringVar(&reg.Ref, "ref", "", "branch, tag or commit to follow (default: the remote's default branch)")
	add.Flags().StringVar(&reg.Path, "path", "", "only use this subdirectory of the repository")
	add.Flags().IntVar(&reg.Priority, "priority", 0, "the registry with the higher number wins a name collision")
	add.Flags().StringSliceVar(&reg.Skills, "skill", nil, "only install this skill; repeatable")
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
			fmt.Fprintln(w, "NAME\tURL\tREF\tPATH\tPRIORITY\tSKILLS")
			for _, r := range cfg.Registries {
				skills := "all"
				if len(r.Skills) > 0 {
					skills = strings.Join(r.Skills, ",")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", r.Name, r.URL, dash(r.Ref), dash(r.Path), r.Priority, skills)
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

	cmd.AddCommand(add, list, remove)
	return cmd
}

func newSync(locate func() (config.Location, error)) *cobra.Command {
	var opts syncer.Options
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch all registries and update the targets",
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
			return runSync(cmd, loc, cfg, opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.DryRun, "dry-run", "n", false, "show what would change without writing anything")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "overwrite and remove skills that were edited locally")
	return cmd
}

func runSync(cmd *cobra.Command, loc config.Location, cfg *config.Config, opts syncer.Options) error {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
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
	for _, r := range rep.Registries {
		if r.Err != nil {
			fmt.Fprintf(errOut, "error: registry %s: %v\n", r.Name, r.Err)
			continue
		}
		fmt.Fprintf(out, "%-12s %d skills at %s\n", r.Name, r.Skills, short(r.Commit))
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
	if rep.Failed() {
		return errSyncFailed
	}
	return nil
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
