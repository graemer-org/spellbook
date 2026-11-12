// Package blocks maintains marked sections inside a text file the user
// also edits. Everything outside the markers is left exactly as it is.
package blocks

import (
	"fmt"
	"strings"
)

const (
	beginPrefix = "<!-- spellbook:begin "
	endPrefix   = "<!-- spellbook:end "
	suffix      = " -->"
)

// Block is one marked section.
type Block struct {
	ID string
	// Body is the text between the markers and ends with a newline.
	Body string
}

func marker(line, prefix string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix), true
}

// Parse splits text into the blocks it contains and the text around them:
// before holds everything in front of the first block, after everything
// else that is not part of a block.
func Parse(text string) (found []Block, before, after string, err error) {
	var pre, post, body []string
	open := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		if open != "" {
			if id, ok := marker(line, endPrefix); ok {
				if id != open {
					return nil, "", "", fmt.Errorf("block %q is closed by the end marker of %q", open, id)
				}
				found = append(found, Block{ID: open, Body: strings.Join(body, "")})
				open, body = "", nil
				continue
			}
			if _, ok := marker(line, beginPrefix); ok {
				return nil, "", "", fmt.Errorf("block %q has no end marker", open)
			}
			if !strings.HasSuffix(line, "\n") {
				line += "\n"
			}
			body = append(body, line)
			continue
		}
		if id, ok := marker(line, beginPrefix); ok {
			open = id
			continue
		}
		if len(found) == 0 {
			pre = append(pre, line)
		} else {
			post = append(post, line)
		}
	}
	if open != "" {
		return nil, "", "", fmt.Errorf("block %q has no end marker", open)
	}
	return found, strings.Join(pre, ""), strings.Join(post, ""), nil
}

// Render puts the blocks between before and after. Rendering what Parse
// returned for a rendered text gives the same text again.
func Render(blocks []Block, before, after string) string {
	var b strings.Builder
	before = strings.TrimRight(before, "\n")
	after = strings.Trim(after, "\n")
	if before != "" {
		b.WriteString(before)
		b.WriteString("\n")
		if len(blocks) > 0 {
			b.WriteString("\n")
		}
	}
	for i, blk := range blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		body := blk.Body
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		b.WriteString(beginPrefix + blk.ID + suffix + "\n")
		b.WriteString(body)
		b.WriteString(endPrefix + blk.ID + suffix + "\n")
	}
	if after != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(after)
		b.WriteString("\n")
	}
	return b.String()
}
