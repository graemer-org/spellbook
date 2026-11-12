package push

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/graemer-org/spellbook/internal/blocks"
	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/gitx"
	"github.com/graemer-org/spellbook/internal/registry"
	"github.com/graemer-org/spellbook/internal/state"
	"github.com/graemer-org/spellbook/internal/syncer"
	"github.com/graemer-org/spellbook/internal/target"
)

// KindHook is a hook entry in a tool's hooks file.
const KindHook = "hook"

// Options are the choices a user makes for one push.
type Options struct {
	// From limits the choice to one tool.
	From string
	// Plain names keys whose values are no secrets.
	Plain []string
	// Name is the name in the registry for something that has none of its
	// own: a hook, or a whole instruction file.
	Name string
}

// instructionFiles are the files in which a user writes standing
// instructions by hand. One of them can be pushed as a whole.
var instructionFiles = []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "copilot-instructions.md"}

// hookCandidates lists the hooks a user wrote into the hooks file of a
// tool that reads Claude Code's form. Their name is "<Event>:<number>".
func hookCandidates(loc config.Location, cfg *config.Config, add func(Candidate)) {
	seen := map[string]bool{}
	for _, tool := range cfg.EnabledTargets() {
		place, err := target.HookFor(tool, loc)
		if err != nil || place.Owned || target.HooksByName(tool) || seen[place.File] {
			// A file Spellbook owns holds none of the user's hooks, and a
			// tool with its own hook format cannot be generalised.
			continue
		}
		seen[place.File] = true
		hooks, err := syncer.LocalHooks(place.File)
		if err != nil {
			continue
		}
		for _, h := range hooks {
			name := h.Event + ":" + strconv.Itoa(h.Index)
			add(Candidate{Kind: KindHook, Name: name, Target: tool, Path: place.File + "#" + name, entry: h.Raw, StateKind: syncer.KindHook})
		}
	}
}

// blockCandidates lists the instructions Spellbook keeps as blocks in a
// shared file, so a change made inside a block can be pushed.
func blockCandidates(loc config.Location, recorded map[string]state.Entry, add func(Candidate)) {
	for key, e := range recorded {
		i := strings.LastIndex(key, "#")
		if e.Kind != syncer.KindBlock || i < 0 {
			continue
		}
		file := key[:i]
		if !filepath.IsAbs(file) {
			file = filepath.Join(loc.Root, filepath.FromSlash(file))
		}
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		found, _, _, err := blocks.Parse(string(data))
		if err != nil {
			continue
		}
		for _, b := range found {
			if b.ID == key[i+1:] {
				add(Candidate{Kind: KindInstruction, Name: e.Name, Target: e.Target, Path: file + "#" + b.ID, Managed: true, Pending: e.Pending,
					Registry: e.Registry, Hash: e.Hash, StateKind: syncer.KindBlock, entry: []byte(b.Body), base: e.Hash})
			}
		}
	}
}

var (
	assignment = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL)[A-Z0-9_]*)=("[^"]*"|'[^']*'|\S+)`)
	shellRef   = regexp.MustCompile(`\$\{?[A-Za-z_]`)
)

func buildHook(c Candidate, opts Options) (Item, error) {
	it := Item{Candidate: c}
	if opts.Name == "" {
		return it, fmt.Errorf("a hook has no name of its own; give it one with --name")
	}
	if !config.ValidResourceName(opts.Name) {
		return it, fmt.Errorf("%q is not a valid hook name", opts.Name)
	}
	event, _, _ := strings.Cut(c.Name, ":")
	if !slices.Contains(registry.HookEvents, event) {
		return it, fmt.Errorf("the event %s is not one a registry hook can have (%s)", event, strings.Join(registry.HookEvents, ", "))
	}
	var entry struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(c.entry, &entry); err != nil {
		return it, err
	}
	if len(entry.Hooks) != 1 || entry.Hooks[0].Command == "" || (entry.Hooks[0].Type != "" && entry.Hooks[0].Type != "command") {
		return it, fmt.Errorf("a registry hook is one command; this entry has %d handlers or another kind of handler. Split it in the settings file first", len(entry.Hooks))
	}
	command := entry.Hooks[0].Command
	var problems []string
	for _, m := range assignment.FindAllStringSubmatch(command, -1) {
		if !shellRef.MatchString(m[3]) && !slices.Contains(opts.Plain, m[1]) {
			problems = append(problems, fmt.Sprintf("%s is set to a value in the command. Remove the assignment and set %s in your environment instead. If the value is no secret, pass --plain %s", m[1], m[1], m[1]))
		}
	}
	fields := strings.Fields(command)
	for i, f := range fields {
		if secretFlag.MatchString(f) && i+1 < len(fields) && !shellRef.MatchString(fields[i+1]) && !slices.Contains(opts.Plain, f) {
			problems = append(problems, fmt.Sprintf("the value after %s is written out. Write \"$%s\" there and set that variable in your environment", f, envName(f)))
		}
	}
	if len(problems) > 0 {
		return it, fmt.Errorf("its command holds values that look like secrets, and a registry is no place for those:\n  - %s\nAfter fixing the hook in %s, push again", strings.Join(problems, "\n  - "), strings.TrimSuffix(c.Path, "#"+c.Name))
	}

	def := map[string]any{"event": event, "command": command}
	if entry.Matcher != "" {
		def["matcher"] = entry.Matcher
	}
	if entry.Hooks[0].Timeout > 0 {
		def["timeout"] = entry.Hooks[0].Timeout
	}
	data, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return it, err
	}
	it.hookEvent, it.hookIndex = event, 0
	if _, n, ok := strings.Cut(c.Name, ":"); ok {
		it.hookIndex, _ = strconv.Atoi(n)
	}
	it.Name = opts.Name
	it.Dest = registry.HooksDir + "/" + opts.Name + ".json"
	it.Files = []gitx.File{{Path: it.Dest, Data: append(data, '\n')}}
	it.Notes = []string{
		"if the command runs a script of yours, add the script to the pull request as hooks/" + opts.Name + "/ with a hook.json, and refer to it as ${HOOK_DIR}/<script>",
		"your entry in the hooks file gets the comment '# spellbook:<registry>/" + opts.Name + "' behind its command, so a later sync recognises it instead of adding a second one",
		"people get a hook from a registry only after 'spellbook registry allow <registry> hooks'",
	}
	return it, nil
}

// buildWholeFile turns a hand-written instruction file into one always-on
// instruction, leaving out the blocks Spellbook keeps in it.
func buildWholeFile(path string, opts Options) (Item, error) {
	it := Item{Candidate: Candidate{Kind: KindInstruction, Name: opts.Name, Path: path}, noPending: true}
	if opts.Name == "" {
		return it, fmt.Errorf("%s is a whole file of instructions; say what the instruction should be called in the registry with --name", filepath.Base(path))
	}
	if !config.ValidResourceName(opts.Name) {
		return it, fmt.Errorf("%q is not a valid instruction name", opts.Name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return it, err
	}
	_, before, after, err := blocks.Parse(string(data))
	if err != nil {
		return it, err
	}
	body := strings.Trim(strings.TrimRight(before, "\n")+"\n\n"+strings.TrimLeft(after, "\n"), "\n") + "\n"
	if strings.TrimSpace(body) == "" {
		return it, fmt.Errorf("it holds no text of your own")
	}
	it.Dest = registry.InstructionsDir + "/" + opts.Name + ".md"
	it.Files = []gitx.File{{Path: it.Dest, Data: []byte(body)}}
	it.Notes = []string{"once the registry delivers this instruction, remove the text from " + filepath.Base(path) + ", or your agent reads it twice"}
	return it, nil
}

// codexServers reads the MCP servers from Codex's config.toml and returns
// them in the form the other tools use.
func codexServers(file string) map[string][]byte {
	servers, _, err := syncer.TOMLServers(file)
	if err != nil {
		return nil
	}
	out := map[string][]byte{}
	for name, table := range servers {
		def := map[string]any{}
		env := map[string]any{}
		headers := map[string]any{}
		for k, v := range table {
			switch k {
			case "http_headers":
				if m, ok := v.(map[string]any); ok {
					for h, hv := range m {
						headers[h] = hv
					}
				}
			case "env_http_headers":
				if m, ok := v.(map[string]any); ok {
					for h, hv := range m {
						headers[h] = fmt.Sprintf("${%v}", hv)
					}
				}
			case "bearer_token_env_var":
				headers["Authorization"] = fmt.Sprintf("Bearer ${%v}", v)
			case "env_vars":
				if list, ok := v.([]any); ok {
					for _, n := range list {
						env[fmt.Sprint(n)] = fmt.Sprintf("${%v}", n)
					}
				}
			case "env":
				if m, ok := v.(map[string]any); ok {
					for n, nv := range m {
						env[n] = nv
					}
				}
			default:
				def[k] = v
			}
		}
		if len(env) > 0 {
			def["env"] = env
		}
		if len(headers) > 0 {
			def["headers"] = headers
		}
		if _, ok := def["url"]; ok {
			def["type"] = "http"
		}
		if raw, err := json.Marshal(def); err == nil {
			out[name] = raw
		}
	}
	return out
}
