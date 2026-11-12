package blocks

import (
	"strings"
	"testing"
)

func TestRenderThenParseIsStable(t *testing.T) {
	in := []Block{{ID: "acme/a", Body: "first\n"}, {ID: "acme/b", Body: "second\nline\n"}}
	text := Render(in, "# Mine\n\nmy text\n", "more of mine\n")

	found, before, after, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0] != in[0] || found[1] != in[1] {
		t.Errorf("blocks changed: %+v", found)
	}
	if again := Render(found, before, after); again != text {
		t.Errorf("rendering twice changes the text:\n%q\n%q", text, again)
	}
	if !strings.HasPrefix(text, "# Mine\n\nmy text\n\n<!-- spellbook:begin acme/a -->\n") {
		t.Errorf("text before the blocks was not kept as it is:\n%s", text)
	}
	if !strings.HasSuffix(text, "<!-- spellbook:end acme/b -->\n\nmore of mine\n") {
		t.Errorf("text after the blocks was not kept as it is:\n%s", text)
	}
}

func TestRemovingAllBlocksLeavesOnlyTheUsersText(t *testing.T) {
	text := Render([]Block{{ID: "x", Body: "body\n"}}, "mine\n", "")
	_, before, after, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if got := Render(nil, before, after); got != "mine\n" {
		t.Errorf("got %q, want the user's text alone", got)
	}
	if got := Render(nil, "", ""); got != "" {
		t.Errorf("a file of blocks only should become empty, got %q", got)
	}
}

func TestParseRejectsBrokenMarkers(t *testing.T) {
	for name, text := range map[string]string{
		"missing end":  "<!-- spellbook:begin a -->\nbody\n",
		"wrong end":    "<!-- spellbook:begin a -->\nbody\n<!-- spellbook:end b -->\n",
		"nested begin": "<!-- spellbook:begin a -->\n<!-- spellbook:begin b -->\n<!-- spellbook:end b -->\n",
	} {
		if _, _, _, err := Parse(text); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
