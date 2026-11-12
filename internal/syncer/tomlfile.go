package syncer

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// tomlFile is a TOML file that a tool and its user also edit. It is
// changed as text, one table at a time, so comments, order and every table
// Spellbook does not manage stay exactly as they are. The decoded form is
// only used to look things up.
type tomlFile struct {
	path string
	text string
	data map[string]any
}

// tomlSection is a table header and the lines that follow it up to the
// next header. Comment lines directly above a header belong to it.
type tomlSection struct {
	// path is the key path of the header, nil when it could not be read.
	path       []string
	start, end int
}

// readTOML reads a TOML file. A missing file is an empty one.
func readTOML(path string) (*tomlFile, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	f := &tomlFile{path: path, text: string(data), data: map[string]any{}}
	if _, err := toml.Decode(f.text, &f.data); err != nil {
		return nil, fmt.Errorf("%s is not valid TOML, so it was left alone: %w", path, err)
	}
	return f, nil
}

// table returns the decoded table at the given key path.
func (f *tomlFile) table(path ...string) (map[string]any, bool) {
	cur := f.data
	for _, key := range path {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// hash identifies the content of the table at path, however it is spelled.
func (f *tomlFile) hash(path ...string) (string, bool) {
	t, ok := f.table(path...)
	if !ok {
		return "", false
	}
	canonical, err := json.Marshal(t)
	if err != nil {
		return "", true
	}
	return hashBytes(canonical), true
}

// sections splits the text at its table headers. The text in front of the
// first header is not a section.
func (f *tomlFile) sections() []tomlSection {
	var out []tomlSection
	var (
		inBasic, inLiteral bool // inside a multi-line string
		depth              int  // open brackets of a multi-line value
		offset             int
		commentStart       = -1 // start of the comment lines right above
	)
	for _, line := range strings.SplitAfter(f.text, "\n") {
		start := offset
		offset += len(line)
		trimmed := strings.TrimSpace(line)
		plain := !inBasic && !inLiteral && depth == 0
		switch {
		case plain && strings.HasPrefix(trimmed, "["):
			if commentStart >= 0 {
				start = commentStart
			}
			if n := len(out); n > 0 {
				out[n-1].end = start
			}
			out = append(out, tomlSection{path: headerPath(trimmed), start: start, end: len(f.text)})
			commentStart = -1
			continue
		case plain && strings.HasPrefix(trimmed, "#"):
			if commentStart < 0 {
				commentStart = start
			}
			continue
		}
		commentStart = -1
		// Follow strings and brackets, so a line inside a multi-line value
		// is not taken for a header.
		for i := 0; i < len(line); i++ {
			rest := line[i:]
			switch {
			case inBasic:
				if rest[0] == '\\' {
					i++
				} else if strings.HasPrefix(rest, `"""`) {
					inBasic = false
					i += 2
					for i+1 < len(line) && line[i+1] == '"' {
						i++
					}
				}
			case inLiteral:
				if strings.HasPrefix(rest, "'''") {
					inLiteral = false
					i += 2
					for i+1 < len(line) && line[i+1] == '\'' {
						i++
					}
				}
			case rest[0] == '#':
				i = len(line)
			case strings.HasPrefix(rest, `"""`):
				inBasic = true
				i += 2
			case strings.HasPrefix(rest, "'''"):
				inLiteral = true
				i += 2
			case rest[0] == '"':
				for i++; i < len(line) && line[i] != '"'; i++ {
					if line[i] == '\\' {
						i++
					}
				}
			case rest[0] == '\'':
				for i++; i < len(line) && line[i] != '\''; i++ {
				}
			case rest[0] == '[' || rest[0] == '{':
				depth++
			case rest[0] == ']' || rest[0] == '}':
				if depth > 0 {
					depth--
				}
			}
		}
	}
	return out
}

// headerPath reads the key path of a header line such as
// [mcp_servers."my server".env] or [[a.b]].
func headerPath(line string) []string {
	s := strings.TrimLeft(line, "[")
	var path []string
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return nil
		}
		var key string
		switch s[0] {
		case '"':
			end := 1
			for end < len(s) && s[end] != '"' {
				if s[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(s) {
				return nil
			}
			var v struct{ K string }
			if _, err := toml.Decode("K = "+s[:end+1], &v); err != nil {
				return nil
			}
			key, s = v.K, s[end+1:]
		case '\'':
			end := strings.IndexByte(s[1:], '\'')
			if end < 0 {
				return nil
			}
			key, s = s[1:end+1], s[end+2:]
		default:
			end := strings.IndexAny(s, ". \t]")
			if end <= 0 {
				return nil
			}
			key, s = s[:end], s[end:]
		}
		path = append(path, key)
		s = strings.TrimLeft(s, " \t")
		switch {
		case strings.HasPrefix(s, "."):
			s = s[1:]
		case strings.HasPrefix(s, "]"):
			return path
		default:
			return nil
		}
	}
}

func hasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i, key := range prefix {
		if path[i] != key {
			return false
		}
	}
	return true
}

// owned returns the sections of the table at path and of its sub-tables.
func (f *tomlFile) owned(path []string) []tomlSection {
	var out []tomlSection
	for _, s := range f.sections() {
		if hasPrefix(s.path, path) {
			out = append(out, s)
		}
	}
	return out
}

// put writes block as the table at path: in the place of the table when it
// has sections of its own already, and at the end of the file otherwise.
// block is complete TOML ending in a newline.
func (f *tomlFile) put(path []string, block string) error {
	old := f.owned(path)
	if len(old) == 0 {
		// A table that is written inline, or with dotted keys, is closed:
		// TOML allows no header that adds to it.
		for n := len(path); n > 0; n-- {
			if _, exists := f.table(path[:n]...); exists && len(f.owned(path[:n])) == 0 {
				return f.notEditable(path[:n])
			}
		}
		text := f.text
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if text != "" && !strings.HasSuffix(text, "\n\n") {
			text += "\n"
		}
		return f.set(text + block)
	}
	// Keep the empty lines that followed the table.
	first := f.text[old[0].start:old[0].end]
	if tail := first[len(strings.TrimRight(first, " \t\r\n")):]; tail != "" {
		block = strings.TrimSuffix(block, "\n") + tail
	}
	text := f.text
	for i := len(old) - 1; i > 0; i-- {
		text = text[:old[i].start] + text[old[i].end:]
	}
	return f.set(text[:old[0].start] + block + text[old[0].end:])
}

// remove deletes the table at path with its sub-tables.
func (f *tomlFile) remove(path []string) error {
	old := f.owned(path)
	if len(old) == 0 {
		return f.notEditable(path)
	}
	text := f.text
	for i := len(old) - 1; i >= 0; i-- {
		text = text[:old[i].start] + text[old[i].end:]
	}
	if old[len(old)-1].end == len(f.text) {
		// The empty line that separated the last table goes with it.
		if trimmed := strings.TrimRight(text, "\n"); trimmed == "" {
			text = ""
		} else {
			text = trimmed + "\n"
		}
	}
	return f.set(text)
}

func (f *tomlFile) notEditable(path []string) error {
	return fmt.Errorf("%s defines %s without a [%s] table of its own, so spellbook cannot change it", f.path, strings.Join(path, "."), strings.Join(path, "."))
}

// set replaces the text after checking that it is still valid TOML.
func (f *tomlFile) set(text string) error {
	data := map[string]any{}
	if _, err := toml.Decode(text, &data); err != nil {
		return fmt.Errorf("%s: the change would not be valid TOML, so the file was left alone: %w", f.path, err)
	}
	f.text, f.data = text, data
	return nil
}

// write stores the file, or removes it when nothing is left in it.
func (f *tomlFile) write() error {
	if strings.TrimSpace(f.text) == "" {
		if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFile(f.path, []byte(f.text))
}

// renderTOMLTable returns a JSON object as the TOML table at path, with a
// comment in front of it.
func renderTOMLTable(path []string, comment string, raw []byte) (string, error) {
	var def map[string]any
	if err := json.Unmarshal(raw, &def); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# " + comment + "\n")
	if err := writeTOMLTable(&b, path, def); err != nil {
		return "", err
	}
	return b.String(), nil
}

// writeTOMLTable writes a table: its header, its values in the order of
// their keys, and then each table it contains.
func writeTOMLTable(b *strings.Builder, path []string, table map[string]any) error {
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	quoted := make([]string, len(path))
	for i, key := range path {
		quoted[i] = tomlKey(key)
	}
	b.WriteString("[" + strings.Join(quoted, ".") + "]\n")
	for _, k := range keys {
		if _, sub := table[k].(map[string]any); sub {
			continue
		}
		v, err := tomlValue(table[k])
		if err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
		b.WriteString(tomlKey(k) + " = " + v + "\n")
	}
	for _, k := range keys {
		if sub, ok := table[k].(map[string]any); ok {
			b.WriteString("\n")
			if err := writeTOMLTable(b, append(path[:len(path):len(path)], k), sub); err != nil {
				return err
			}
		}
	}
	return nil
}

func tomlKey(key string) string {
	bare := key != ""
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			bare = false
		}
	}
	if bare {
		return key
	}
	return tomlString(key)
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlValue writes a value on one line.
func tomlValue(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return tomlString(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			// TOML tells integers and floats apart, JSON does not.
			return strconv.FormatInt(int64(x), 10), nil
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			p, err := tomlValue(e)
			if err != nil {
				return "", err
			}
			parts[i] = p
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			p, err := tomlValue(x[k])
			if err != nil {
				return "", err
			}
			parts[i] = tomlKey(k) + " = " + p
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	return "", fmt.Errorf("TOML has no way to write %v", v)
}
