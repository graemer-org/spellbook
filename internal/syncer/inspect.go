package syncer

import (
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/blocks"
	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/state"
)

// Sized is an installed resource and how much text it holds.
type Sized struct {
	Name  string
	Bytes int
}

// Load describes what the installed resources cost one tool in every
// session, whatever the task.
type Load struct {
	// AlwaysOn is the text of all instructions that are not limited to
	// certain files, largest first.
	AlwaysOn      []Sized
	AlwaysOnBytes int
	// Skills counts skills and prompts installed as skills. A tool reads
	// the description of each one into every session.
	Skills int
}

// EnvRef is an environment variable an installed MCP server refers to.
type EnvRef struct {
	Server, Target, Var string
}

// Inventory is what Inspect finds out about the installed resources.
type Inventory struct {
	Targets map[string]*Load
	// MissingEnv lists variables MCP servers need that are not set.
	MissingEnv []EnvRef
}

var (
	// envRef matches ${VAR}. The form ${VAR:-default} has a fallback and is
	// not matched.
	envRef    = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	scopedKey = regexp.MustCompile(`(?m)^(paths|applyTo):`)
)

// Inspect reads the installed resources and reports what they add to every
// session and what they still need from the environment. It changes
// nothing.
func Inspect(loc config.Location) (*Inventory, error) {
	st, err := state.Load(loc.StatePath)
	if err != nil {
		return nil, err
	}
	inv := &Inventory{Targets: map[string]*Load{}}
	load := func(t string) *Load {
		if inv.Targets[t] == nil {
			inv.Targets[t] = &Load{}
		}
		return inv.Targets[t]
	}
	alwaysOn := func(e state.Entry, text string) {
		l := load(e.Target)
		l.AlwaysOn = append(l.AlwaysOn, Sized{Name: e.Registry + "/" + e.Name, Bytes: len(text)})
		l.AlwaysOnBytes += len(text)
	}
	seenEnv := map[EnvRef]bool{}

	for _, e := range st.Entries {
		switch e.Kind {
		case KindSkill:
			load(e.Target).Skills++
		case KindPrompt:
			if info, err := os.Stat(absPath(loc.Root, e.Path)); err == nil && info.IsDir() {
				load(e.Target).Skills++
			}
		case KindInstruction:
			data, err := os.ReadFile(absPath(loc.Root, e.Path))
			if err != nil {
				continue
			}
			text := string(data)
			if rest, ok := strings.CutPrefix(text, "---\n"); ok {
				if front, _, found := strings.Cut(rest, "\n---\n"); found && scopedKey.MatchString(front) {
					// Limited to certain files, so not part of every session.
					continue
				}
			}
			alwaysOn(e, text)
		case KindBlock:
			i := strings.LastIndex(e.Path, "#")
			if i < 0 {
				continue
			}
			data, err := os.ReadFile(absPath(loc.Root, e.Path[:i]))
			if err != nil {
				continue
			}
			found, _, _, err := blocks.Parse(string(data))
			if err != nil {
				continue
			}
			for _, b := range found {
				if b.ID == e.Path[i+1:] {
					alwaysOn(e, b.Body)
				}
			}
		case KindMCP, KindMCPCLI:
			i := strings.LastIndex(e.Path, "#")
			if i < 0 {
				continue
			}
			_, servers, err := readServers(absPath(loc.Root, e.Path[:i]))
			if err != nil {
				continue
			}
			raw, ok := servers.Get(e.Name)
			if !ok {
				continue
			}
			for _, m := range envRef.FindAllStringSubmatch(string(raw), -1) {
				ref := EnvRef{Server: e.Name, Target: e.Target, Var: m[1]}
				if _, set := os.LookupEnv(m[1]); !set && !seenEnv[ref] {
					seenEnv[ref] = true
					inv.MissingEnv = append(inv.MissingEnv, ref)
				}
			}
		}
	}
	for _, l := range inv.Targets {
		sort.Slice(l.AlwaysOn, func(i, j int) bool { return l.AlwaysOn[i].Bytes > l.AlwaysOn[j].Bytes })
	}
	sort.Slice(inv.MissingEnv, func(i, j int) bool {
		a, b := inv.MissingEnv[i], inv.MissingEnv[j]
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		if a.Var != b.Var {
			return a.Var < b.Var
		}
		return a.Target < b.Target
	})
	return inv, nil
}
