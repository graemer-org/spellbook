package syncer

import (
	"os"
	"strings"

	"github.com/graemer-org/spellbook/internal/blocks"
	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/state"
)

// Problem is an installed resource that no longer matches the state file.
type Problem struct {
	Entry state.Entry
	// Missing is true when the resource is gone, false when it was edited.
	Missing bool
}

// Verify compares what the state file records with what is on disk. It
// returns the number of recorded resources and those that differ.
func Verify(loc config.Location) (int, []Problem, error) {
	st, err := state.Load(loc.StatePath)
	if err != nil {
		return 0, nil, err
	}
	var problems []Problem
	for _, e := range st.Entries {
		if e.Kind == KindBlock {
			i := strings.LastIndex(e.Path, "#")
			if i < 0 {
				continue
			}
			data, err := os.ReadFile(absPath(loc.Root, e.Path[:i]))
			if err != nil && !os.IsNotExist(err) {
				return 0, nil, err
			}
			found, _, _, err := blocks.Parse(string(data))
			if err != nil {
				problems = append(problems, Problem{Entry: e})
				continue
			}
			missing := true
			for _, b := range found {
				if b.ID != e.Path[i+1:] {
					continue
				}
				missing = false
				if hashBytes([]byte(b.Body)) != e.Hash {
					problems = append(problems, Problem{Entry: e})
				}
			}
			if missing {
				problems = append(problems, Problem{Entry: e, Missing: true})
			}
			continue
		}
		if e.Kind == KindMCP || e.Kind == KindMCPCLI {
			i := strings.LastIndex(e.Path, "#")
			if i < 0 {
				continue
			}
			_, servers, err := readServers(absPath(loc.Root, e.Path[:i]))
			if err != nil {
				problems = append(problems, Problem{Entry: e})
				continue
			}
			if hash, ok := serverHash(servers, e.Name); !ok {
				problems = append(problems, Problem{Entry: e, Missing: true})
			} else if hash != e.Hash {
				problems = append(problems, Problem{Entry: e})
			}
			continue
		}
		abs := absPath(loc.Root, e.Path)
		if _, err := os.Lstat(abs); os.IsNotExist(err) {
			problems = append(problems, Problem{Entry: e, Missing: true})
			continue
		}
		hash, err := hashPath(abs)
		if err != nil {
			return 0, nil, err
		}
		if hash != e.Hash {
			problems = append(problems, Problem{Entry: e})
		}
	}
	return len(st.Entries), problems, nil
}
