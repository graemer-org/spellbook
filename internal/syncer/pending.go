package syncer

import (
	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/state"
)

// PendingItem is a local resource that was pushed to a registry.
type PendingItem struct {
	// Abs is the file or directory of the resource.
	Abs string
	// Kind is one of the state kinds, such as KindSkill.
	Kind, Name, Target, Registry string
	// Base is the recorded hash of the registry's version, when the
	// resource came from the registry and was changed locally.
	Base string
	// Key and Hash are set for a resource that is an entry inside a file,
	// such as an MCP server: the state path "<file>#<name>" and the hash
	// of the entry. Abs is then the file.
	Key, Hash string
}

// HashBytes returns the content hash the state file records for an entry.
func HashBytes(b []byte) string { return hashBytes(b) }

// StateKey returns the path the state file records for a file or
// directory.
func StateKey(loc config.Location, abs string) string { return pathKey(loc.Root, abs) }

// HashPath returns the content hash the state file records for a file or
// directory.
func HashPath(path string) (string, error) { return hashPath(path) }

// MarkPending records pushed resources in the state file, with the content
// they have now. From then on the sync treats them as its own: it keeps
// them while the registry does not offer them, replaces them with the
// registry's version once it does, and leaves them alone if they were
// edited in between.
func MarkPending(loc config.Location, items []PendingItem) error {
	st, err := state.Load(loc.StatePath)
	if err != nil {
		return err
	}
	byPath := st.ByPath()
	for _, it := range items {
		hash, key := it.Hash, it.Key
		if key == "" {
			var err error
			if hash, err = hashPath(it.Abs); err != nil {
				return err
			}
			key = pathKey(loc.Root, it.Abs)
		}
		byPath[key] = state.Entry{Path: key, Kind: it.Kind, Name: it.Name, Target: it.Target, Registry: it.Registry, Hash: hash, Pending: true, Base: it.Base}
	}
	st.Entries = st.Entries[:0]
	for _, e := range byPath {
		st.Entries = append(st.Entries, e)
	}
	return st.Save(loc.StatePath)
}

// Managed reports whether the file or directory is recorded in the state
// file, and whether it is only pending.
func Managed(loc config.Location, abs string) (managed, pending bool, err error) {
	st, err := state.Load(loc.StatePath)
	if err != nil {
		return false, false, err
	}
	e, ok := st.ByPath()[pathKey(loc.Root, abs)]
	return ok, e.Pending, nil
}
