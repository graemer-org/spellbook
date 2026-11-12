package syncer

import (
	"os"
	"path/filepath"
	"testing"
)

const userTOML = `# my settings
model = "gpt-5" # the default

notes = """
[mcp_servers.fake]
not a table"""
matrix = [
  [1, 2],
  [3, 4],
]

# my own server
[mcp_servers.own]
command = "own"

[mcp_servers."own".env]
A = "1"

[tui]
notifications = true
`

func parse(t *testing.T, text string) *tomlFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := readTOML(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTOMLTablesAreChangedWithoutTouchingTheRest(t *testing.T) {
	f := parse(t, userTOML)
	if got := len(f.sections()); got != 3 {
		t.Fatalf("found %d tables, want 3: lines inside values are not headers", got)
	}
	path := []string{"mcp_servers", "docs"}
	block, err := renderTOMLTable(path, "note", []byte(`{"command":"npx","args":["-y","x"],"startup_timeout_sec":20,"env":{"A B":"c\"d"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.put(path, block); err != nil {
		t.Fatal(err)
	}
	want := userTOML + `
# note
[mcp_servers.docs]
args = ["-y", "x"]
command = "npx"
startup_timeout_sec = 20

[mcp_servers.docs.env]
"A B" = "c\"d"
`
	if f.text != want {
		t.Fatalf("after adding a table:\n%s", f.text)
	}
	added, _ := f.hash(path...)

	// Replacing keeps the place of the table, also when it is not the last.
	if err := f.put([]string{"mcp_servers", "own"}, "[mcp_servers.own]\ncommand = \"new\"\n"); err != nil {
		t.Fatal(err)
	}
	if own, _ := f.table("mcp_servers", "own"); own["command"] != "new" || own["env"] != nil {
		t.Errorf("the table was not replaced with its sub-table: %v", own)
	}
	if again, _ := f.hash(path...); again != added {
		t.Error("replacing one table changed another")
	}

	f = parse(t, want)
	if err := f.remove(path); err != nil {
		t.Fatal(err)
	}
	if f.text != userTOML {
		t.Errorf("removing the table did not give the user's file back:\n%s", f.text)
	}
	if err := f.remove([]string{"mcp_servers", "own"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.table("mcp_servers", "own"); ok {
		t.Error("the table with its comment and sub-table should be gone")
	}
	if _, ok := f.table("tui"); !ok {
		t.Error("the table behind the removed one is gone as well")
	}
}

func TestTOMLTableInAnotherFormIsNotChanged(t *testing.T) {
	f := parse(t, "mcp_servers = { docs = { command = \"x\" } }\n")
	if err := f.remove([]string{"mcp_servers", "docs"}); err == nil {
		t.Error("an inline table cannot be removed as text and must be refused")
	}
	if err := f.put([]string{"mcp_servers", "other"}, "[mcp_servers.other]\ncommand = \"y\"\n"); err == nil {
		t.Error("a change that makes the file invalid must be refused")
	}
	if f.text != "mcp_servers = { docs = { command = \"x\" } }\n" {
		t.Errorf("a refused change altered the text:\n%s", f.text)
	}
}
