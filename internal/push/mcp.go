package push

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/gitx"
	"github.com/graemer-org/spellbook/internal/ojson"
	"github.com/graemer-org/spellbook/internal/registry"
	"github.com/graemer-org/spellbook/internal/state"
	"github.com/graemer-org/spellbook/internal/syncer"
	"github.com/graemer-org/spellbook/internal/target"
)

// KindMCP is an MCP server definition.
const KindMCP = "mcp"

// mcpCandidates lists the MCP servers in the configuration files of the
// enabled tools. Their path is "<file>#<name>".
func mcpCandidates(loc config.Location, cfg *config.Config, recorded map[string]state.Entry, add func(Candidate)) {
	for _, tool := range cfg.EnabledTargets() {
		place, err := target.MCPFor(tool, loc)
		if err != nil {
			continue
		}
		if place.TOML {
			// Codex keeps its servers in TOML, in a form of its own. Only
			// the ones Spellbook did not install are offered.
			_, hashes, _ := syncer.TOMLServers(place.File)
			for name, raw := range codexServers(place.File) {
				if _, managed := recorded[syncer.StateKey(loc, place.File)+"#"+name]; managed {
					continue
				}
				add(Candidate{Kind: KindMCP, Name: name, Target: tool, Path: place.File + "#" + name, entry: raw, StateKind: syncer.KindMCPTOML, tomlHash: hashes[name]})
			}
			continue
		}
		data, err := os.ReadFile(place.File)
		if err != nil {
			continue
		}
		file, err := ojson.Parse(data)
		if err != nil {
			continue
		}
		raw, ok := file.Get("mcpServers")
		if !ok {
			continue
		}
		servers, err := ojson.Parse(raw)
		if err != nil {
			continue
		}
		for _, name := range servers.Keys() {
			entry, _ := servers.Get(name)
			c := Candidate{Kind: KindMCP, Name: name, Target: tool, Path: place.File + "#" + name, entry: entry, StateKind: syncer.KindMCP}
			if place.ViaCLI {
				c.StateKind = syncer.KindMCPCLI
			}
			if e, ok := recorded[syncer.StateKey(loc, place.File)+"#"+name]; ok {
				c.Managed, c.Pending, c.Registry, c.Hash, c.StateKind = true, e.Pending, e.Registry, e.Hash, e.Kind
				// What the registry's version looked like for this tool.
				c.base = e.Source
				if c.base == "" {
					c.base = e.Hash
				}
			}
			add(c)
		}
	}
}

func entryHash(entry []byte) string {
	canonical, err := ojson.Canonical(entry)
	if err != nil {
		return ""
	}
	return syncer.HashBytes(canonical)
}

// shared are the fields of a server definition every tool reads alike.
var sharedMCP = []string{"type", "command", "args", "env", "cwd", "url", "headers"}

func buildMCP(c Candidate, plain []string) (Item, error) {
	it := Item{Candidate: c, Dest: registry.MCPDir + "/" + c.Name + ".json"}
	if !config.ValidResourceName(c.Name) {
		return it, fmt.Errorf("%q is not a valid server name", c.Name)
	}
	var def map[string]any
	if err := json.Unmarshal(c.entry, &def); err != nil {
		return it, err
	}
	// Gemini CLI names the transport by the field, the others by a type.
	if u, ok := def["httpUrl"]; ok && c.Target == "gemini" {
		def["type"], def["url"] = "http", u
		delete(def, "httpUrl")
	}
	if problems := secrets(def, plain); len(problems) > 0 {
		return it, fmt.Errorf("it holds values that look like secrets, and a registry is no place for those:\n  - %s\nAfter fixing the definition in %s, push again", strings.Join(problems, "\n  - "), strings.TrimSuffix(c.Path, "#"+c.Name))
	}
	out := map[string]any{}
	own := map[string]any{}
	var moved []string
	for k, v := range def {
		switch {
		case slices.Contains(sharedMCP, k) || target.Known(k):
			out[k] = v
		default:
			own[k] = v
			moved = append(moved, k)
		}
	}
	if existing, ok := out[c.Target].(map[string]any); ok {
		for k, v := range own {
			existing[k] = v
		}
	} else if len(own) > 0 {
		out[c.Target] = own
	}
	if len(moved) > 0 {
		sort.Strings(moved)
		it.Notes = append(it.Notes, fmt.Sprintf("%s only applies to %s (moved under %q)", strings.Join(moved, ", "), c.Target, c.Target))
	}
	it.Notes = append(it.Notes, "people get an MCP server from a registry only after 'spellbook registry allow <registry> mcp'")
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return it, err
	}
	it.Files = []gitx.File{{Path: it.Dest, Data: append(data, '\n')}}
	return it, nil
}

var (
	reference  = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*(:-[^}]*)?\}`)
	secretFlag = regexp.MustCompile(`(?i)^--?[\w-]*(key|token|secret|password|passwd|credential)[\w-]*$`)
	secretName = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|credential|auth)`)
)

// secrets returns what in a server definition looks like a secret written
// out, each with how to fix it. A value is fine when it refers to an
// environment variable, or when the user named its key as plain.
func secrets(def map[string]any, plain []string) []string {
	var problems []string
	literal := func(v any) (string, bool) {
		s, ok := v.(string)
		return s, ok && s != "" && !reference.MatchString(s)
	}
	for _, field := range []string{"env", "headers"} {
		values, _ := def[field].(map[string]any)
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, bad := literal(values[k]); !bad || slices.Contains(plain, k) {
				continue
			}
			fix := fmt.Sprintf("%q: \"${%s}\"", k, k)
			if field == "headers" {
				name := strings.ToUpper(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(k, "_")) + "_VALUE"
				fix = fmt.Sprintf("%q: \"${%s}\"", k, name)
				if strings.EqualFold(k, "Authorization") {
					fix = `"Authorization": "Bearer ${API_TOKEN}"`
				}
			}
			problems = append(problems, fmt.Sprintf("%s.%s is written out. Write %s and set that variable in your environment. If the value is no secret, pass --plain %s", field, k, fix, k))
		}
	}
	args, _ := def["args"].([]any)
	for i, a := range args {
		s, _ := a.(string)
		if flag, value, ok := strings.Cut(s, "="); ok && secretFlag.MatchString(flag) {
			if _, bad := literal(value); bad && !slices.Contains(plain, flag) {
				problems = append(problems, fmt.Sprintf("args: %s carries a value. Write %s=${%s} and set that variable in your environment", flag, flag, envName(flag)))
			}
			continue
		}
		if secretFlag.MatchString(s) && i+1 < len(args) {
			if _, bad := literal(args[i+1]); bad && !slices.Contains(plain, s) {
				problems = append(problems, fmt.Sprintf("args: the value after %s is written out. Write \"${%s}\" there and set that variable in your environment", s, envName(s)))
			}
		}
	}
	if s, ok := def["url"].(string); ok {
		if u, err := url.Parse(s); err == nil {
			if u.User != nil {
				problems = append(problems, "url contains a user name or password. Remove it and send credentials in a header that refers to a variable")
			}
			for k, vs := range u.Query() {
				if secretName.MatchString(k) && len(vs) > 0 && !reference.MatchString(vs[0]) && !slices.Contains(plain, k) {
					problems = append(problems, fmt.Sprintf("url carries %s in the query. Write %s=${%s} and set that variable in your environment", k, k, envName(k)))
				}
			}
		}
	}
	return problems
}

func envName(flag string) string {
	return strings.ToUpper(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(strings.TrimLeft(flag, "-"), "_"))
}

// reconcileMCP makes an item an update when the registry has a server of
// that name, keeping the registry's settings for the other tools.
func reconcileMCP(snap *registry.Snapshot, it *Item) error {
	i := slices.IndexFunc(snap.AllMCP, func(m registry.MCPServer) bool { return m.Name == it.Name })
	if i < 0 {
		return nil
	}
	orig := snap.AllMCP[i]
	if orig.File == "" {
		return fmt.Errorf("the registry lists this server in a shared mcp.json, not in a file of its own; change it there")
	}
	var theirs, ours map[string]any
	if err := json.Unmarshal(orig.Raw, &theirs); err != nil {
		return err
	}
	if err := json.Unmarshal(it.Files[0].Data, &ours); err != nil {
		return err
	}
	var kept []string
	for k, v := range theirs {
		if _, has := ours[k]; !has && target.Known(k) && k != it.Target {
			ours[k] = v
			kept = append(kept, k)
		}
	}
	if len(kept) > 0 {
		sort.Strings(kept)
		it.Notes = append(it.Notes, fmt.Sprintf("kept from the registry's version, because your %s copy does not hold them: %s", it.Target, strings.Join(kept, ", ")))
	}
	data, err := json.MarshalIndent(ours, "", "  ")
	if err != nil {
		return err
	}
	it.Dest, it.Update = orig.File, true
	it.Files = []gitx.File{{Path: orig.File, Data: append(data, '\n')}}
	return nil
}
